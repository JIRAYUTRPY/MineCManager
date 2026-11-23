package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/JIRAYUTRPY/MineCManager/internal/accounts"
	"github.com/JIRAYUTRPY/MineCManager/internal/fleet"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

type browserRunner struct {
	mu      sync.Mutex
	content string
}

func (b *browserRunner) Run(_ context.Context, _ fleet.Host, q map[string]any) (json.RawMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]any{"ok": true}
	switch q["action"] {
	case "metrics":
		out = map[string]any{"cpu_count": 4, "cpu_percent": 12, "ram_total": 8 << 30, "ram_available": 6 << 30, "disk_total": 80 << 30, "disk_free": 60 << 30, "uptime_seconds": 3600}
	case "read":
		h := sha256.Sum256([]byte(b.content))
		out = map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(b.content)), "sha256": hex.EncodeToString(h[:])}
	case "write":
		raw, _ := base64.StdEncoding.DecodeString(q["content"].(string))
		b.content = string(raw)
		out = map[string]any{"sha256": "updated", "previous_copy": "/var/lib/minecmanager/file-history/test"}
	case "deploy":
		out = map[string]any{"server": q["server"], "platform": q["platform"], "note": "service started"}
	case "backup":
		out = map[string]any{"backup": "20261116T010000Z-12345678.tar.gz", "sha256": "test-checksum"}
	}
	raw, e := json.Marshal(out)
	return raw, e
}
func TestFleetRequiresSeparatePrivilege(t *testing.T) {
	token := "12345678901234567890123456789012"
	digest := sha256.Sum256([]byte(token))
	s := &server{cfg: config{Keys: []keyConfig{{ID: "legacy", SHA256: hex.EncodeToString(digest[:]), Scopes: []string{"admin:nodes", "nodes:control"}}}}}
	r := httptest.NewRequest("GET", "/v1/fleet/hosts", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.handle(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestInitializePreservesSecrets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCM_PANEL_CONFIG", filepath.Join(dir, "panel.json"))
	t.Setenv("MCM_VAULT_KEY_FILE", filepath.Join(dir, "vault.key"))
	if e := initialize(); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "vault.key"))
	if e := initialize(); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "vault.key"))
	if !bytes.Equal(before, after) {
		t.Fatal("key regenerated")
	}
	os.Remove(filepath.Join(dir, "vault.key"))
	if e := initialize(); e == nil {
		t.Fatal("missing original vault key accepted")
	}
}
func TestFleetBrowser(t *testing.T) {
	if os.Getenv("MCM_FLEET_UI_TEST") == "" {
		t.Skip("set MCM_FLEET_UI_TEST=1 with Playwright installed")
	}
	dir := t.TempDir()
	store, e := fleet.Open(filepath.Join(dir, "fleet.vault"), bytes.Repeat([]byte{3}, 32))
	if e != nil {
		t.Fatal(e)
	}
	f, e := openAudit(filepath.Join(dir, "audit"))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	token := "browser-admin-key-12345678901234567890"
	digest := sha256.Sum256([]byte(token))
	users, e := accounts.Open(filepath.Join(dir, "accounts.sqlite3"), scopes)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { users.Close() })
	s := &server{accounts: users, audit: f, cfg: config{Keys: []keyConfig{{ID: "bootstrap", SHA256: hex.EncodeToString(digest[:]), Scopes: []string{"admin:keys", "admin:fleet"}}}}}
	runner := &browserRunner{content: "motd=before\n"}
	s.fleet = &fleet.Service{Store: store, Runner: runner, Guard: s.fleetGuard, Audit: func(actor, host, action, phase string, status int) error {
		return s.record(auditEvent{KeyID: actor, NodeID: host, Action: action, Phase: phase, Status: status})
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.fleet.Run(ctx)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/", s.handle)
	mux.HandleFunc("/", s.ui)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()
	cmd := exec.Command("node", "../../scripts/fleet-ui-smoke.cjs")
	cmd.Env = append(os.Environ(), "MCM_FLEET_URL="+httpServer.URL, "MCM_UI_KEY="+token)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("%v\n%s", e, out)
	} else {
		t.Log(string(out))
	}
}
