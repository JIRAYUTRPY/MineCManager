package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type enrollmentProbe func(context.Context, Host, map[string]any) (json.RawMessage, error)

func (p enrollmentProbe) Run(ctx context.Context, host Host, input map[string]any) (json.RawMessage, error) {
	return p(ctx, host, input)
}

func TestProbeDoesNotPersistAndFailurePreservesCredentials(t *testing.T) {
	s, runner := fixture(t)
	w := call(s, "POST", "hosts/test", hostJSON())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"cpu_count":2`) || strings.Contains(w.Body.String(), "super-secret") {
		t.Fatal(w.Code, w.Body)
	}
	if len(s.Store.data.Hosts) != 0 {
		t.Fatal("test enrolled a VM")
	}
	if _, err := os.Stat(s.Store.path); !os.IsNotExist(err) {
		t.Fatal("test wrote the vault")
	}

	runner.err = errors.New("remote message containing a credential")
	w = call(s, "POST", "hosts", hostJSON())
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "credential") || len(s.Store.data.Hosts) != 0 {
		t.Fatal("failed verification affected enrollment or exposed remote text", w.Code)
	}
	runner.err = nil
	addHost(t, s)
	before, err := os.ReadFile(s.Store.path)
	if err != nil {
		t.Fatal(err)
	}
	runner.err = errHostKeyMismatch
	w = call(s, "PUT", "hosts", strings.Replace(hostJSON(), "super-secret", "replacement", 1))
	after, err := os.ReadFile(s.Store.path)
	if err != nil || string(before) != string(after) || w.Code != http.StatusBadGateway || s.Store.data.Hosts["game"].Password != "super-secret" {
		t.Fatal("failed rotation replaced credentials", err)
	}
}

func TestVerificationDoesNotLockOtherHostsOrOverwriteConcurrentChanges(t *testing.T) {
	s, _ := fixture(t)
	addHost(t, s)
	started, resume := make(chan struct{}), make(chan struct{})
	s.Runner = enrollmentProbe(func(ctx context.Context, _ Host, _ map[string]any) (json.RawMessage, error) {
		close(started)
		select {
		case <-resume:
			return json.RawMessage(`{"cpu_count":1,"ram_total":1024,"disk_total":4096}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	defer close(resume)
	finished := make(chan int, 1)
	go func() { finished <- call(s, "PUT", "hosts", hostJSON()).Code }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("SSH probe never started")
	}
	removed := make(chan int, 1)
	go func() { removed <- call(s, "DELETE", "hosts/game", "").Code }()
	select {
	case code := <-removed:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SSH probe holds vault lock")
	}
	resume <- struct{}{}
	if code := <-finished; code != http.StatusConflict || len(s.Store.data.Hosts) != 0 {
		t.Fatal("racing verification recreated a deleted host", code)
	}
}

func TestEnrollmentRechecksScopeAndAuditsFailedVerification(t *testing.T) {
	s, runner := fixture(t)
	type event struct {
		target, phase string
		status        int
	}
	var events []event
	s.Audit = func(_ string, host string, _ string, phase string, status int) error {
		events = append(events, event{host, phase, status})
		return nil
	}
	s.Guard = func(string) bool { return false }
	w := call(s, "POST", "hosts", hostJSON())
	if w.Code != 403 || len(s.Store.data.Hosts) != 0 || len(events) != 2 || events[0] != (event{"game", "intent", 0}) || events[1] != (event{"game", "result", 403}) {
		t.Fatal(w.Code, events)
	}
	s.Audit = func(string, string, string, string, int) error { return errors.New("audit offline") }
	calls := runner.calls
	if call(s, "POST", "hosts/test", hostJSON()).Code != 503 || runner.calls != calls {
		t.Fatal("SSH dispatched without audit")
	}
}

func TestReplacementCannotRedirectAQueuedJob(t *testing.T) {
	s, _ := fixture(t)
	addHost(t, s)
	if w := call(s, "POST", "jobs", `{"host":"game","action":"metrics"}`); w.Code != 202 {
		t.Fatal(w.Code)
	}
	moved := strings.ReplaceAll(hostJSON(), "10.0.0.1", "10.0.0.2")
	if w := call(s, "PUT", "hosts", moved); w.Code != 409 || s.Store.data.Hosts["game"].Address != "10.0.0.1" {
		t.Fatal("queued job retargeted", w.Code)
	}
	s.tick(context.Background())
	if w := call(s, "PUT", "hosts", moved); w.Code != 201 || s.Store.data.Hosts["game"].Address != "10.0.0.2" {
		t.Fatal("idle host did not move", w.Code)
	}
}
