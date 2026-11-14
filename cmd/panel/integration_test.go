package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPanelAgentProxyAndAudit(t *testing.T) {
	const agentToken = "agent-secret-123456789012345678901234567890"
	const readToken = "read-secret-123456789012345678901234567890"
	const controlToken = "control-secret-123456789012345678901234567890"
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+agentToken {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path == "/v1/status" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"state":"active"}`))
			return
		}
		if r.URL.Path == "/v1/actions" && r.Method == http.MethodPost {
			var payload struct {
				Action string `json:"action"`
			}
			if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Action != "restart" {
				http.Error(w, "bad action", 400)
				return
			}
			w.WriteHeader(204)
			return
		}
		http.NotFound(w, r)
	}))
	defer agent.Close()
	// The production panel accepts loopback-only nodes; httptest uses 127.0.0.1.
	t.Setenv("MCM_INTEGRATION_AGENT_TOKEN", agentToken)
	digest := func(token string) string { h := sha256.Sum256([]byte(token)); return hex.EncodeToString(h[:]) }
	cfg := config{
		Keys: []keyConfig{
			{ID: "reader", SHA256: digest(readToken), Scopes: []string{"nodes:read"}},
			{ID: "operator", SHA256: digest(controlToken), Scopes: []string{"nodes:control"}},
		},
		Nodes: []nodeConfig{{ID: "test-node", URL: agent.URL, TokenEnv: "MCM_INTEGRATION_AGENT_TOKEN"}},
	}
	if err := validate(cfg); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	audit, err := os.OpenFile(auditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	panel := server{cfg: cfg, client: &http.Client{Timeout: time.Second}, audit: audit}
	test := func(method, path, token, body string, want int) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		panel.handle(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: got %d want %d body=%s", method, path, rec.Code, want, rec.Body.String())
		}
	}
	test("GET", "/v1/nodes", readToken, "", 200)
	test("GET", "/v1/nodes/test-node/status", readToken, "", 200)
	test("POST", "/v1/nodes/test-node/actions", readToken, `{"action":"restart"}`, 403)
	test("POST", "/v1/nodes/test-node/actions", controlToken, `{"action":"restart"}`, 204)
	test("POST", "/v1/nodes/test-node/actions", controlToken, `{"action":"shell"}`, 400)
	test("GET", "/v1/nodes/test-node/status", "", "", 403)
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"action":"restart"`) {
		t.Fatalf("missing restart audit event: %s", data)
	}
	if strings.Contains(string(data), controlToken) || strings.Contains(string(data), agentToken) {
		t.Fatal("audit leaked bearer token")
	}
}
