package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseRequestsUseFleetAuthorityAndRegisteredAgent(t *testing.T) {
	token := strings.Repeat("r", 40)
	nodeToken := strings.Repeat("a", 40)
	t.Setenv("MCM_TEST_UPDATE_TOKEN", nodeToken)
	calls := 0
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/updates" || r.Header.Get("Authorization") != "Bearer "+nodeToken {
			t.Error("unexpected proxy target or credential")
		}
		writeJSON(w, 202, map[string]string{"phase": "queued"})
	}))
	defer agent.Close()
	audit, err := os.Create(filepath.Join(t.TempDir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	s := &server{audit: audit, client: agent.Client(), cfg: config{
		Keys:  []keyConfig{{ID: "key", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(token))), Scopes: []string{"nodes:control"}}},
		Nodes: []nodeConfig{{ID: "survival", URL: agent.URL, TokenEnv: "MCM_TEST_UPDATE_TOKEN"}},
	}}
	call := func(path, body string) int {
		r := httptest.NewRequest("POST", "/v1/fleet/updates?node="+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.handle(w, r)
		return w.Code
	}
	apply := `{"action":"apply","version":"v1.2.0"}`
	if got := call("survival", apply); got != 403 {
		t.Fatalf("node operator got %d", got)
	}
	s.cfg.Keys[0].Scopes = []string{"admin:fleet"}
	if got := call("missing", apply); got != 404 {
		t.Fatal(got)
	}
	if got := call("survival", `{"action":"apply","version":"main"}`); got != 400 {
		t.Fatal(got)
	}
	if got := call("survival", apply); got != 202 || calls != 1 {
		t.Fatalf("status %d, remote calls %d", got, calls)
	}
}
