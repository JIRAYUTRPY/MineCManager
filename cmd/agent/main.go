package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/JIRAYUTRPY/MineCManager/internal/lock"
	"github.com/JIRAYUTRPY/MineCManager/internal/transport"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type agent struct {
	token     string
	root      string
	service   string
	command   func(context.Context, string) ([]byte, error)
	paperHTTP *http.Client
}

func (a agent) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		expected := "Bearer " + a.token
		if subtle.ConstantTimeCompare([]byte(h), []byte(expected)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
func (a agent) execute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}
	switch body.Action {
	case "start", "stop", "restart":
	default:
		http.Error(w, "invalid action", 400)
		return
	}
	operationMu.Lock()
	defer operationMu.Unlock()
	if output, err := a.serviceCommand(r.Context(), body.Action); err != nil {
		log.Printf("systemctl %s failed: %v %s", body.Action, err, output)
		http.Error(w, "service action failed", 502)
		return
	}
	w.WriteHeader(204)
}
func (a agent) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	output, err := a.serviceCommand(r.Context(), "is-active")
	state := strings.TrimSpace(string(output))
	if err != nil && state == "" {
		state = "unknown"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"state": state})
}
func safePath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", errors.New("absolute path denied")
	}
	clean := filepath.Clean(relative)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errors.New("path traversal denied")
	}
	full := filepath.Join(root, clean)
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("path outside root")
	}
	return resolved, nil
}
func (a agent) files(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	root, err := os.OpenRoot(a.root)
	if err != nil {
		http.Error(w, "root unavailable", 503)
		return
	}
	defer root.Close()
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}
	if !filepath.IsLocal(path) {
		http.Error(w, "invalid path", 400)
		return
	}
	f, err := root.Open(path)
	if err != nil {
		http.Error(w, "invalid path", 400)
		return
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		http.Error(w, "cannot list directory", 400)
		return
	}
	type item struct {
		Name      string `json:"name"`
		Directory bool   `json:"directory"`
	}
	result := make([]item, 0, len(entries))
	for _, e := range entries {
		result = append(result, item{e.Name(), e.IsDir()})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
func main() {
	token := os.Getenv("MCM_AGENT_TOKEN")
	root := os.Getenv("MCM_SERVER_ROOT")
	service := os.Getenv("MCM_SYSTEMD_UNIT")
	if len(token) < 32 || root == "" || service == "" {
		log.Fatal("MCM_AGENT_TOKEN (>=32 chars), MCM_SERVER_ROOT and MCM_SYSTEMD_UNIT required")
	}
	if !identifier.MatchString(service) || !strings.HasSuffix(service, ".service") || strings.ContainsAny(service, "/ \\") {
		log.Fatal("invalid service unit")
	}
	if !filepath.IsAbs(root) {
		log.Fatal("server root must be absolute")
	}
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		log.Fatal("server root must exist")
	}
	a := agent{token: token, root: root, service: service}
	state, err := a.stateDirectory()
	if err != nil {
		log.Fatal(err)
	}
	lease, err := lock.Acquire(filepath.Join(state, "agent.lock"))
	if err != nil {
		log.Fatal(err)
	}
	defer lease.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", a.auth(a.status))
	mux.HandleFunc("/v1/metrics", a.auth(a.metrics))
	mux.HandleFunc("/v1/paper/", a.auth(a.paper))
	mux.HandleFunc("/v1/actions", a.auth(a.execute))
	mux.HandleFunc("/v1/files", a.auth(a.files))
	mux.HandleFunc("/v1/plugins", a.auth(a.plugins))
	mux.HandleFunc("/v1/plugins/", a.auth(a.plugin))
	mux.HandleFunc("/v1/backup/manifest", a.auth(a.backupManifest))
	mux.HandleFunc("/v1/config", a.auth(a.configFile))
	mux.HandleFunc("/v1/backups", a.auth(a.backups))
	mux.HandleFunc("/v1/backups/", a.auth(a.backups))
	s := &http.Server{Addr: "127.0.0.1:9081", Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 120 * time.Second, WriteTimeout: 10 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8192}
	addr := os.Getenv("MCM_AGENT_ADDR")
	if addr != "" {
		s.Addr = addr
	}
	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		log.Fatal(err)
	}
	ip := net.ParseIP(host)
	if os.Getenv("MCM_SERVER_CERT") != "" {
		s.TLSConfig, err = transport.Config(os.Getenv("MCM_SERVER_CERT"), os.Getenv("MCM_SERVER_KEY"), os.Getenv("MCM_CA_CERT"), true)
		if err != nil {
			log.Fatal(err)
		}
		log.Fatal(s.ListenAndServeTLS("", ""))
	}
	if ip == nil || !ip.IsLoopback() {
		log.Fatal("non-loopback agent requires mutual TLS")
	}
	log.Fatal(s.ListenAndServe())
}
