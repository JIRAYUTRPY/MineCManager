package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JIRAYUTRPY/MineCManager/internal/accounts"
)

func TestHumanAccountLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.sqlite3")
	open := func() *accounts.Store {
		db, err := accounts.Open(path, scopes)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	audit, err := openAudit(filepath.Join(dir, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	t.Setenv("MCM_BOOTSTRAP_KEY_ID", "")
	const primary = "original-test-key-00000000000000000"
	const other = "another-test-key-000000000000000000"
	hash := func(token string) string { sum := sha256.Sum256([]byte(token)); return hex.EncodeToString(sum[:]) }
	s := &server{accounts: open(), audit: audit, cfg: config{Keys: []keyConfig{
		{ID: "bootstrap", SHA256: hash(primary), Scopes: []string{"admin:keys", "nodes:read", "admin:fleet"}},
		{ID: "another", SHA256: hash(other), Scopes: []string{"admin:keys"}},
	}}}
	defer func() { s.accounts.Close() }()
	request := func(method, path, body string, c *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://panel.example"+path, strings.NewReader(body))
		r.Header.Set("X-MCM-Request", "1")
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c)
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		if strings.HasPrefix(path, "/v1/") {
			s.handle(w, r)
		} else {
			s.ui(w, r)
		}
		return w
	}
	check := func(w *httptest.ResponseRecorder, want int) *httptest.ResponseRecorder {
		t.Helper()
		if w.Code != want {
			t.Fatalf("want %d, got %d: %s", want, w.Code, w.Body.String())
		}
		return w
	}
	call := func(method, path, body string, c *http.Cookie, want int) *httptest.ResponseRecorder {
		t.Helper()
		return check(request(method, path, body, c, nil), want)
	}
	const setup = "/v1/auth/bootstrap"
	const login = "/v1/auth/login"
	const manage = "/v1/manage/users"
	const roles = "/v1/manage/roles"
	const prefs = "/v1/me/preferences/"
	bootstrap := func(key string) string {
		return `{"admin_key":"` + key + `","username":"owner","password":"owner test passphrase"}`
	}
	if location := call("GET", "/nodes", "", nil, 303).Header().Get("Location"); !strings.HasPrefix(location, "/setup?") {
		t.Fatal(location)
	}
	call("POST", setup, bootstrap(other), nil, 403)
	check(request("POST", setup, bootstrap(primary), nil, map[string]string{"X-MCM-Request": ""}), 403)
	check(request("POST", setup, bootstrap(primary), nil, map[string]string{"Origin": "https://other.example", "Sec-Fetch-Site": "cross-site"}), 403)
	owner := call("POST", setup, bootstrap(primary), nil, 201).Result().Cookies()[0]
	if !owner.HttpOnly || !owner.Secure || owner.SameSite != http.SameSiteStrictMode {
		t.Fatal("owner cookie flags")
	}
	call("POST", setup, bootstrap(primary), nil, 409)
	if loc := call("GET", "/setup", "", nil, 303).Header().Get("Location"); !strings.HasPrefix(loc, "/login?") {
		t.Fatal(loc)
	}
	for _, p := range pages {
		call("GET", p.Path, "", owner, 200)
	}
	call("GET", "/v1/keys", "", owner, 200)
	check(request("GET", manage, "", nil, map[string]string{"Authorization": "Bearer " + primary}), 403)
	check(request("GET", "/v1/nodes", "", nil, map[string]string{"Authorization": "Bearer " + primary}), 200)
	call("PATCH", manage+"/owner", `{"enabled":false}`, owner, 403)
	call("POST", roles, `{"name":"keyadmin","scopes":["admin:keys"]}`, owner, 400)
	call("POST", roles, `{"name":"useradmin","scopes":["users:manage"]}`, owner, 400)
	call("POST", roles, `{"name":"helper","scopes":["nodes:read","plugins:read"]}`, owner, 201)
	call("POST", manage, `{"username":"staff","role":"helper","password":"staff test passphrase"}`, owner, 201)
	const staffLogin = `{"username":"staff","password":"staff test passphrase"}`
	staff := call("POST", login, staffLogin, nil, 200).Result().Cookies()[0]
	call("GET", manage, "", staff, 403)
	call("GET", "/v1/keys", "", staff, 403)
	call("GET", "/v1/fleet/hosts", "", staff, 403)
	call("GET", "/admin/users", "", staff, 403)
	call("GET", "/nodes/plugins", "", staff, 200)
	call("GET", "/v1/nodes", "", staff, 200)
	call("POST", "/v1/nodes/game/actions", `{"action":"restart"}`, staff, 403)
	if s.fleetGuard("user:staff") || !s.fleetGuard("user:owner") || !s.fleetGuard("bootstrap") {
		t.Fatal("fleet authorizations")
	}
	call("PUT", prefs+"nodes", `{"node":"alpha"}`, owner, 200)
	call("PUT", prefs+"nodes", `{"node":"beta"}`, staff, 200)
	call("PUT", prefs+"plugins", `{"node":"gamma"}`, staff, 200)
	call("PUT", prefs+"nodes", `{"password":"private"}`, staff, 400)
	call("PUT", prefs+"jobs", `{"jobStatus":"failed"}`, staff, 403)
	if err = s.accounts.Close(); err != nil {
		t.Fatal(err)
	}
	s.accounts = open()
	for _, tc := range []struct {
		page, expected string
		user           *http.Cookie
	}{
		{"nodes", "alpha", owner}, {"nodes", "beta", staff}, {"plugins", "gamma", staff},
	} {
		got := call("GET", prefs+tc.page, "", tc.user, 200).Body.String()
		if !strings.Contains(got, `"node":"`+tc.expected+`"`) {
			t.Fatalf("restored prefs: %s", got)
		}
	}
	call("PUT", roles+"/helper", `{"scopes":["admin:fleet","admin:nodes"]}`, owner, 200)
	call("GET", "/fleet", "", staff, 200)
	call("GET", "/admin/nodes", "", staff, 200)
	call("GET", "/admin/keys", "", staff, 403)
	call("GET", manage, "", staff, 403)
	call("PUT", prefs+"jobs", `{"jobStatus":"failed"}`, staff, 200)
	if !s.fleetGuard("user:staff") {
		t.Fatal("accepted Fleet role was not recognized at job dispatch")
	}
	call("PUT", roles+"/helper", `{"scopes":["plugins:read"]}`, owner, 200)
	if s.fleetGuard("user:staff") {
		t.Fatal("removed Fleet scope survived at job dispatch")
	}
	call("GET", "/fleet", "", staff, 403)
	call("GET", "/nodes", "", staff, 403)
	call("GET", "/v1/nodes", "", staff, 403)
	call("PATCH", manage+"/staff", `{"role":"viewer"}`, owner, 200)
	call("GET", prefs+"plugins", "", staff, 401)
	staff = call("POST", login, staffLogin, nil, 200).Result().Cookies()[0]
	call("POST", "/v1/me/password", `{"current_password":"staff test passphrase","new_password":"updated staff passphrase"}`, staff, 200)
	call("GET", prefs+"nodes", "", staff, 401)
	call("POST", login, staffLogin, nil, 403)
	const changedLogin = `{"username":"staff","password":"updated staff passphrase"}`
	staff = call("POST", login, changedLogin, nil, 200).Result().Cookies()[0]
	call("PATCH", manage+"/staff", `{"enabled":false}`, owner, 200)
	call("GET", prefs+"nodes", "", staff, 401)
	call("POST", login, changedLogin, nil, 403)
	call("POST", "/v1/auth/logout", "{}", owner, 200)
	call("GET", manage, "", owner, 401)
	log, err := os.ReadFile(audit.Name())
	if err != nil || !strings.Contains(string(log), "user:owner") {
		t.Fatal("missing user audit", err)
	}
	for _, secret := range []string{primary, "staff test passphrase", owner.Value} {
		if strings.Contains(string(log), secret) {
			t.Fatal("secret in audit")
		}
	}
}
