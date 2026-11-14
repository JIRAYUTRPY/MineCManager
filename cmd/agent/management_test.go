package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jarBytes(t *testing.T, version string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, e := z.Create("plugin.yml")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.Write([]byte("name: Example\nversion: '" + version + "'\nmain: example.Main\n"))
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func testAgent(t *testing.T) agent {
	t.Helper()
	root := t.TempDir()
	if e := os.Mkdir(filepath.Join(root, "plugins"), 0750); e != nil {
		t.Fatal(e)
	}
	t.Setenv("MCM_STATE_ROOT", t.TempDir())
	return agent{root: root, command: func(context.Context, string) ([]byte, error) { return []byte("inactive"), nil }}
}
func request(t *testing.T, handler func(*httptest.ResponseRecorder), want int) {
	t.Helper()
	w := httptest.NewRecorder()
	handler(w)
	if w.Code != want {
		t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
	}
}
func TestConfigVersionAndConflict(t *testing.T) {
	a := testAgent(t)
	path := filepath.Join(a.root, "plugins", "Example")
	os.Mkdir(path, 0750)
	original := []byte("enabled: true\n")
	os.WriteFile(filepath.Join(path, "config.yml"), original, 0640)
	call := func(method, body, etag string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, "/v1/config?path=plugins/Example/config.yml", strings.NewReader(body))
		r.Header.Set("If-Match", etag)
		request(t, func(w *httptest.ResponseRecorder) { a.configFile(w, r) }, want)
	}
	call("PUT", "enabled: false\n", "", 412)
	call("PUT", "x: [", `"`+sum(original)+`"`, 400)
	call("PUT", "enabled: false\n", `"`+sum(original)+`"`, 200)
	call("PUT", "enabled: true\n", `"`+sum(original)+`"`, 412)
	call("POST", `{"version":"`+sum(original)+`"}`, `"`+sum([]byte("enabled: false\n"))+`"`, 200)
	got, _ := os.ReadFile(filepath.Join(path, "config.yml"))
	if !bytes.Equal(got, original) {
		t.Fatal("rollback mismatch")
	}
	a.command = func(context.Context, string) ([]byte, error) { return []byte("active"), nil }
	call("PUT", "x: 1", `"`+sum(original)+`"`, 409)
}
func TestPluginReplacementAndRollback(t *testing.T) {
	a := testAgent(t)
	one, two := jarBytes(t, "1"), jarBytes(t, "2")
	call := func(method string, b []byte, etag string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, "/v1/plugins/Example.jar", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/java-archive")
		r.Header.Set("If-Match", etag)
		request(t, func(w *httptest.ResponseRecorder) { a.plugin(w, r) }, want)
	}
	call("PUT", []byte("fake jar"), "", 400)
	call("PUT", one, "", 201)
	call("PUT", two, "", 412)
	call("PUT", two, `"`+sum(one)+`"`, 200)
	call("POST", []byte(`{"version":"`+sum(one)+`"}`), `"`+sum(two)+`"`, 200)
	b, _ := os.ReadFile(filepath.Join(a.root, "plugins", "Example.jar"))
	if !bytes.Equal(b, one) {
		t.Fatal("plugin rollback mismatch")
	}
}
func TestSnapshotRestoreVerification(t *testing.T) {
	a := testAgent(t)
	os.Mkdir(filepath.Join(a.root, "custom-world"), 0750)
	os.WriteFile(filepath.Join(a.root, "custom-world", "level.dat"), []byte("original world"), 0640)
	w := httptest.NewRecorder()
	a.backups(w, httptest.NewRequest("POST", "/v1/backups", strings.NewReader("{}")))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var snap snapshot
	if e := json.Unmarshal(w.Body.Bytes(), &snap); e != nil {
		t.Fatal(e)
	}
	world := filepath.Join(a.root, "custom-world", "level.dat")
	os.WriteFile(world, []byte("changed"), 0640)
	restore := func(want int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		a.backups(w, httptest.NewRequest("POST", "/v1/backups/"+snap.ID+"/restore", strings.NewReader(`{"confirm":"`+snap.ID+`"}`)))
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
		return w
	}
	snapshotFile := filepath.Join(os.Getenv("MCM_STATE_ROOT"), "backups", snap.ID, "data", "custom-world", "level.dat")
	os.WriteFile(snapshotFile, []byte("corrupt"), 0640)
	restore(400)
	b, _ := os.ReadFile(world)
	if string(b) != "changed" {
		t.Fatal("failed restore changed live root")
	}
	os.WriteFile(snapshotFile, []byte("original world"), 0640)
	w = restore(200)
	b, _ = os.ReadFile(world)
	if string(b) != "original world" {
		t.Fatal("restore mismatch")
	}
	var result struct {
		Previous string `json:"previous_root"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	defer os.RemoveAll(result.Previous)
	b, _ = os.ReadFile(filepath.Join(result.Previous, "custom-world", "level.dat"))
	if string(b) != "changed" {
		t.Fatal("previous root lost")
	}
}
func TestSnapshotRejectsSymlink(t *testing.T) {
	a := testAgent(t)
	os.Symlink("/etc/passwd", filepath.Join(a.root, "escape"))
	request(t, func(w *httptest.ResponseRecorder) { a.backups(w, httptest.NewRequest("POST", "/v1/backups", nil)) }, 500)
}
func TestConfigPathBoundaries(t *testing.T) {
	for _, p := range []string{"../secret.json", "/etc/a.yml", "plugins/../server.properties", "plugins/x.jar", "ops.json", "plugins/a/../../x.yml"} {
		if configPath(p) {
			t.Errorf("accepted %s", p)
		}
	}
}
func TestPluginDeleteAndRestore(t *testing.T) {
	a := testAgent(t)
	b := jarBytes(t, "1")
	os.WriteFile(filepath.Join(a.root, "plugins", "Example.jar"), b, 0640)
	r := httptest.NewRequest("DELETE", "/v1/plugins/Example.jar", nil)
	r.Header.Set("If-Match", `"`+sum(b)+`"`)
	request(t, func(w *httptest.ResponseRecorder) { a.plugin(w, r) }, 204)
	if _, e := os.Stat(filepath.Join(a.root, "plugins", "Example.jar")); !os.IsNotExist(e) {
		t.Fatal("not deleted")
	}
	r = httptest.NewRequest("POST", "/v1/plugins/Example.jar", strings.NewReader(`{"version":"`+sum(b)+`"}`))
	r.Header.Set("If-None-Match", "*")
	request(t, func(w *httptest.ResponseRecorder) { a.plugin(w, r) }, 201)
}
func TestSnapshotGroupWritePermissions(t *testing.T) {
	source := t.TempDir()
	dest := t.TempDir()
	os.Mkdir(filepath.Join(source, "world"), 0700)
	os.WriteFile(filepath.Join(source, "world", "level.dat"), []byte("data"), 0600)
	if _, e := copySnapshot(source, dest, nil); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"world", "world/level.dat"} {
		st, e := os.Stat(filepath.Join(dest, p))
		if e != nil || st.Mode().Perm()&0020 == 0 {
			t.Fatalf("restored %s is not group writable", p)
		}
	}
}
