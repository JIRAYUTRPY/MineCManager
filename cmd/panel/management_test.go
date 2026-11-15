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
	"sync/atomic"
	"testing"
	"time"
)

const adminToken = "administrator-secret-12345678901234567890"

func managementPanel(t *testing.T, handler http.Handler) (*server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if handler != nil {
			handler.ServeHTTP(w, r)
		} else {
			w.WriteHeader(204)
		}
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("TEST_AGENT_TOKEN", strings.Repeat("a", 32))
	h := sha256.Sum256([]byte(adminToken))
	audit, e := os.OpenFile(filepath.Join(t.TempDir(), "audit"), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { audit.Close() })
	s := &server{cfg: config{Keys: []keyConfig{{ID: "admin", SHA256: hex.EncodeToString(h[:]), Scopes: []string{"admin:keys", "admin:nodes", "audit:read", "nodes:read", "nodes:control", "plugins:write", "configs:write", "backups:restore"}}}, Nodes: []nodeConfig{{ID: "one", URL: upstream.URL, TokenEnv: "TEST_AGENT_TOKEN"}, {ID: "two", URL: upstream.URL, TokenEnv: "TEST_AGENT_TOKEN"}}}, client: upstream.Client(), audit: audit, configPath: filepath.Join(t.TempDir(), "panel.json")}
	if e := persist(s.configPath, s.cfg); e != nil {
		t.Fatal(e)
	}
	return s, &calls
}
func panelRequest(s *server, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/java-archive")
	r.Header.Set("If-Match", `"checksum"`)
	w := httptest.NewRecorder()
	s.handle(w, r)
	return w
}
func TestAuditFailurePreventsDispatch(t *testing.T) {
	s, calls := managementPanel(t, nil)
	s.audit.Close()
	w := panelRequest(s, "POST", "/v1/nodes/one/actions", adminToken, `{"action":"restart"}`)
	if w.Code != 503 || calls.Load() != 0 {
		t.Fatalf("status %d calls %d", w.Code, calls.Load())
	}
}
func TestKeyLifecyclePersistsAndRestrictsNodes(t *testing.T) {
	s, calls := managementPanel(t, nil)
	w := panelRequest(s, "POST", "/v1/keys", adminToken, `{"id":"reader","scopes":["nodes:read"],"nodes":["one"]}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var key struct {
		Token string `json:"token"`
	}
	json.Unmarshal(w.Body.Bytes(), &key)
	if len(key.Token) < 32 {
		t.Fatal("missing key")
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"GET", "/v1/nodes/one/status", 204}, {"GET", "/v1/nodes/two/status", 403}, {"PUT", "/v1/nodes/one/plugins/Test.jar", 403}} {
		w = panelRequest(s, tc.method, tc.path, key.Token, "")
		if w.Code != tc.want {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("unauthorized dispatch")
	}
	w = panelRequest(s, "GET", "/v1/nodes", key.Token, "")
	if strings.Contains(w.Body.String(), "two") {
		t.Fatal("node leaked")
	}
	data, _ := os.ReadFile(s.configPath)
	if strings.Contains(string(data), key.Token) {
		t.Fatal("raw token persisted")
	}
	w = panelRequest(s, "DELETE", "/v1/keys/reader", adminToken, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	data, _ = os.ReadFile(s.configPath)
	var restored config
	json.Unmarshal(data, &restored)
	s.cfg = restored
	if w = panelRequest(s, "GET", "/v1/nodes/one/status", key.Token, ""); w.Code != 403 {
		t.Fatal("revocation lost")
	}
	if w = panelRequest(s, "DELETE", "/v1/keys/admin", adminToken, ""); w.Code != 409 {
		t.Fatal("last admin revoked")
	}
}
func TestExpiredKeyDenied(t *testing.T) {
	s, _ := managementPanel(t, nil)
	past := time.Now().Add(-time.Hour)
	s.cfg.Keys[0].ExpiresAt = &past
	if w := panelRequest(s, "GET", "/v1/nodes", adminToken, ""); w.Code != 403 {
		t.Fatal("expired key allowed")
	}
}
func TestManagementProxyForwardsHeadersAndQuery(t *testing.T) {
	s, _ := managementPanel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/config" || r.URL.Query().Get("path") != "plugins/Example/config.yml" || r.Header.Get("If-Match") != `"checksum"` || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 32) {
			t.Errorf("incorrect proxy request %s %v", r.URL, r.Header)
		}
		w.WriteHeader(200)
	}))
	w := panelRequest(s, "PUT", "/v1/nodes/one/config?path=plugins%2FExample%2Fconfig.yml", adminToken, "test: true")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestNodeRegistrationAndAuditPagination(t *testing.T) {
	s, _ := managementPanel(t, nil)
	w := panelRequest(s, "POST", "/v1/nodes", adminToken, `{"id":"remote","url":"https://agent.example:9081","token_env":"TEST_AGENT_TOKEN"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = panelRequest(s, "DELETE", "/v1/registrations/remote", adminToken, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = panelRequest(s, "GET", "/v1/audit?offset=0&limit=2", adminToken, "")
	var page struct {
		Records []json.RawMessage `json:"records"`
		Next    int               `json:"next_offset"`
	}
	if json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Records) != 2 || page.Next != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestPersistenceFailureDoesNotIssueKey(t *testing.T) {
	s, _ := managementPanel(t, nil)
	s.configPath = filepath.Join(t.TempDir(), "missing", "panel.json")
	w := panelRequest(s, "POST", "/v1/keys", adminToken, `{"id":"new","scopes":["nodes:read"]}`)
	if w.Code != 503 || len(s.cfg.Keys) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestInventoryWriteRequiresSeparateScope(t *testing.T) {
	s, calls := managementPanel(t, nil)
	path := "/v1/nodes/one/paper/players/00000000-0000-0000-0000-000000000001/inventory/0"
	w := panelRequest(s, "PUT", path, adminToken, `{"material":"STONE","amount":1}`)
	if w.Code != 403 || calls.Load() != 0 {
		t.Fatal("node control key allowed item write")
	}
	s.cfg.Keys[0].Scopes = append(s.cfg.Keys[0].Scopes, "inventory:write")
	w = panelRequest(s, "PUT", path, adminToken, `{"material":"STONE","amount":1}`)
	if w.Code != 204 || calls.Load() != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
}
