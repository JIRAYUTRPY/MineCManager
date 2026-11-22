package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	calls int
	input map[string]any
	err   error
}

func (f *fakeRunner) Run(_ context.Context, _ Host, q map[string]any) (json.RawMessage, error) {
	f.calls++
	f.input = q
	if q["action"] == "metrics" {
		return json.RawMessage(`{"cpu_count":2,"ram_total":1024,"disk_total":4096}`), f.err
	}
	return json.RawMessage(`{"ok":true}`), f.err
}
func fixture(t *testing.T) (*Service, *fakeRunner) {
	t.Helper()
	store, e := Open(filepath.Join(t.TempDir(), "state"), bytes.Repeat([]byte{1}, 32))
	if e != nil {
		t.Fatal(e)
	}
	runner := &fakeRunner{}
	return &Service{Store: store, Runner: runner, Guard: func(string) bool { return true }, Audit: func(string, string, string, string, int) error { return nil }}, runner
}
func call(s *Service, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, "/v1/fleet/"+path, strings.NewReader(body))
	s.Handle(w, r, "admin")
	return w
}
func hostJSON() string {
	return `{"id":"game","public_ip":"203.0.113.1","private_ip":"10.0.0.1","address":"10.0.0.1","port":22,"user":"root","fingerprint":"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","password":"super-secret"}`
}
func addHost(t *testing.T, s *Service) {
	t.Helper()
	if w := call(s, "POST", "hosts", hostJSON()); w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
func TestVaultEncryptsAndAuthenticates(t *testing.T) {
	s, _ := fixture(t)
	addHost(t, s)
	b, e := os.ReadFile(s.Store.path)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(b, []byte("super-secret")) {
		t.Fatal("plaintext secret")
	}
	opened, e := Open(s.Store.path, bytes.Repeat([]byte{1}, 32))
	if e != nil || opened.data.Hosts["game"].Password != "super-secret" {
		t.Fatal("round trip", e)
	}
	if _, e = Open(s.Store.path, bytes.Repeat([]byte{2}, 32)); e == nil {
		t.Fatal("wrong key accepted")
	}
	b[len(b)-1] ^= 1
	os.WriteFile(s.Store.path, b, 0600)
	if _, e = Open(s.Store.path, bytes.Repeat([]byte{1}, 32)); e == nil {
		t.Fatal("tamper accepted")
	}
}
func TestPublicResponsesHideSecrets(t *testing.T) {
	s, _ := fixture(t)
	addHost(t, s)
	w := call(s, "GET", "hosts", "")
	if strings.Contains(w.Body.String(), "super-secret") || strings.Contains(w.Body.String(), "password") {
		t.Fatal(w.Body)
	}
	w = call(s, "POST", "jobs", `{"host":"game","action":"command","input":{"command":"secret-command","confirm":"ROOT"}}`)
	if w.Code != 202 || strings.Contains(w.Body.String(), "secret-command") {
		t.Fatal(w.Code, w.Body)
	}
	w = call(s, "GET", "jobs", "")
	if strings.Contains(w.Body.String(), "secret-command") {
		t.Fatal("job input leaked")
	}
}
func TestRevokePreventsQueuedDispatch(t *testing.T) {
	s, r := fixture(t)
	addHost(t, s)
	call(s, "POST", "jobs", `{"host":"game","action":"metrics"}`)
	s.Guard = func(string) bool { return false }
	s.tick(context.Background())
	if r.calls != 1 || s.Store.data.Jobs[0].Status != "failed" {
		t.Fatal("revoked job ran")
	}
}
func TestAuditFailurePreventsDispatch(t *testing.T) {
	s, r := fixture(t)
	addHost(t, s)
	call(s, "POST", "jobs", `{"host":"game","action":"metrics"}`)
	s.Audit = func(string, string, string, string, int) error { return errors.New("disk failed") }
	s.tick(context.Background())
	if r.calls != 1 {
		t.Fatal("unaudited command ran")
	}
}
func TestJobLifecycleAndUntrustedUploadURL(t *testing.T) {
	s, r := fixture(t)
	addHost(t, s)
	w := call(s, "POST", "jobs", `{"host":"game","action":"backup","input":{"server":"survival","upload_url":"https://attacker.invalid","r2_key":"bad"}}`)
	if w.Code != 202 {
		t.Fatal(w.Body)
	}
	s.tick(context.Background())
	if r.calls != 2 || r.input["upload_url"] != nil || s.Store.data.Jobs[0].Status != "succeeded" || s.Store.data.Jobs[0].Input != nil {
		t.Fatal("invalid lifecycle")
	}
}
func TestRestartDoesNotReplayRunningJobs(t *testing.T) {
	s, _ := fixture(t)
	s.Store.data.Jobs = []Job{{ID: "x", Status: "running"}, {ID: "y", Status: "queued"}}
	if e := s.Store.save(); e != nil {
		t.Fatal(e)
	}
	store, e := Open(s.Store.path, bytes.Repeat([]byte{1}, 32))
	if e != nil {
		t.Fatal(e)
	}
	if store.data.Jobs[0].Status != "interrupted" || store.data.Jobs[1].Status != "queued" {
		t.Fatal(store.data.Jobs)
	}
}
func TestScheduleAndRevocation(t *testing.T) {
	s, r := fixture(t)
	addHost(t, s)
	sc := Schedule{ID: "daily", Host: "game", Server: "survival", EveryHours: 24, Retain: 7, Enabled: true, Actor: "admin", Next: time.Now().Add(-time.Hour)}
	s.Store.data.Schedules[sc.ID] = sc
	s.tick(context.Background())
	if r.calls != 2 || r.input["action"] != "backup" {
		t.Fatal("schedule did not dispatch")
	}
	s.Guard = func(string) bool { return false }
	sc.Next = time.Now().Add(-time.Hour)
	s.Store.data.Schedules[sc.ID] = sc
	s.tick(context.Background())
	if s.Store.data.Schedules[sc.ID].Enabled {
		t.Fatal("revoked schedule not disabled")
	}
}
func TestHostValidationAndFailureLatch(t *testing.T) {
	s, _ := fixture(t)
	bad := strings.Replace(hostJSON(), "10.0.0.1", "$(touch /tmp/oops)", -1)
	if w := call(s, "POST", "hosts", bad); w.Code != 400 {
		t.Fatal("invalid address accepted")
	}
	s.Store.path = filepath.Join(t.TempDir(), "missing", "vault")
	if w := call(s, "POST", "hosts", hostJSON()); w.Code != 503 {
		t.Fatal(w.Code)
	}
	if w := call(s, "GET", "hosts", ""); w.Code != 503 {
		t.Fatal("persistence failure not latched")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDNSDefaultsToUnproxiedAndRejectsBadIP(t *testing.T) {
	s, _ := fixture(t)
	s.Client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.cloudflare.com" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatal("wrong endpoint/auth")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["proxied"] != false {
			t.Fatal("game proxied")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"success":true}`))}, nil
	})}
	q := map[string]any{"zone": strings.Repeat("a", 32), "name": "mc.example.com", "type": "A", "content": "203.0.113.1"}
	if _, e := s.dns(context.Background(), Cloud{Token: "token"}, q); e != nil {
		t.Fatal(e)
	}
	q["content"] = "bad"
	if _, e := s.dns(context.Background(), Cloud{Token: "token"}, q); e == nil {
		t.Fatal("bad IP accepted")
	}
}

func TestR2PresignDoesNotLeakAccountSecret(t *testing.T) {
	c := Cloud{Account: strings.Repeat("a", 32), Bucket: "backups", AccessKey: "access-key", SecretKey: "TOP-SECRET"}
	u, e := uploadURL(context.Background(), c, "minecmanager/game/survival/job.tar.gz")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(u, c.SecretKey) || !strings.Contains(u, "r2.cloudflarestorage.com/backups/minecmanager/") || !strings.Contains(u, "X-Amz-SignedHeaders=host") {
		t.Fatal("unexpected signed upload URL", u)
	}
}

func TestSchedulesRespectQueueCapacity(t *testing.T) {
	s, r := fixture(t)
	for i := 0; i < 105; i++ {
		id := newID()
		s.Store.data.Hosts[id] = Host{ID: id}
		s.Store.data.Schedules[id] = Schedule{ID: id, Host: id, Server: "survival", Enabled: true, EveryHours: 24, Retain: 7, Actor: "admin", Next: time.Now().Add(-time.Hour)}
	}
	s.tick(context.Background())
	if len(s.Store.data.Jobs) != 100 || r.calls != 1 {
		t.Fatalf("jobs=%d calls=%d", len(s.Store.data.Jobs), r.calls)
	}
}
