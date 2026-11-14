package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func persist(path string, c config) error {
	if path == "" {
		return errors.New("persistent config not configured")
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".panel-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (s *server) admin(w http.ResponseWriter, r *http.Request) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	scope := "admin:keys"
	if r.URL.Path == "/v1/nodes" || strings.HasPrefix(r.URL.Path, "/v1/registrations/") {
		scope = "admin:nodes"
	}
	if r.URL.Path == "/v1/audit" {
		scope = "audit:read"
	}
	id, ok := s.authorize(r, scope)
	ev := auditEvent{RequestID: requestID(), Phase: "intent", KeyID: id, Action: r.Method + " " + r.URL.Path}
	w.Header().Set("X-Request-ID", ev.RequestID)
	if !ok {
		ev.Status = 403
		_ = s.record(ev)
		http.Error(w, "forbidden", 403)
		return
	}
	if e := s.record(ev); e != nil {
		http.Error(w, "audit unavailable", 503)
		return
	}
	if r.URL.Path == "/v1/audit" && r.Method == "GET" {
		s.auditPage(w, r)
		return
	}
	if r.URL.Path == "/v1/keys" && r.Method == "GET" {
		out := []map[string]any{}
		for _, k := range s.cfg.Keys {
			out = append(out, map[string]any{"id": k.ID, "scopes": k.Scopes, "nodes": k.Nodes, "expires_at": k.ExpiresAt, "revoked": k.Revoked})
		}
		writeJSON(w, 200, out)
		return
	}
	// Deep copy prevents failed writes from mutating live authorization state.
	raw, _ := json.Marshal(s.cfg)
	var next config
	_ = json.Unmarshal(raw, &next)
	var result any = map[string]bool{"updated": true}
	status := 200
	switch {
	case r.URL.Path == "/v1/keys" && r.Method == "POST":
		var input struct {
			ID        string     `json:"id"`
			Token     string     `json:"token"`
			Scopes    []string   `json:"scopes"`
			Nodes     []string   `json:"nodes"`
			ExpiresAt *time.Time `json:"expires_at"`
		}
		if decodeJSON(w, r, &input) != nil {
			http.Error(w, "invalid key request", 400)
			return
		}
		if input.Token == "" {
			var b [32]byte
			if _, e := rand.Read(b[:]); e != nil {
				http.Error(w, "random source unavailable", 500)
				return
			}
			input.Token = hex.EncodeToString(b[:])
		}
		if len(input.Token) < 32 || len(input.Token) > 1024 || strings.TrimSpace(input.Token) != input.Token || input.ExpiresAt != nil && !time.Now().Before(*input.ExpiresAt) {
			http.Error(w, "invalid token length or expiration", 400)
			return
		}
		h := sha256.Sum256([]byte(input.Token))
		next.Keys = append(next.Keys, keyConfig{ID: input.ID, SHA256: hex.EncodeToString(h[:]), Scopes: input.Scopes, Nodes: input.Nodes, ExpiresAt: input.ExpiresAt})
		result = map[string]string{"id": input.ID, "token": input.Token}
		status = 201
	case strings.HasPrefix(r.URL.Path, "/v1/keys/") && r.Method == "DELETE":
		keyID := strings.TrimPrefix(r.URL.Path, "/v1/keys/")
		found := false
		for i := range next.Keys {
			if next.Keys[i].ID == keyID {
				next.Keys[i].Revoked = true
				found = true
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
		admins := 0
		for _, k := range next.Keys {
			if !k.Revoked && (k.ExpiresAt == nil || time.Now().Before(*k.ExpiresAt)) {
				for _, sc := range k.Scopes {
					if sc == "admin:keys" {
						admins++
					}
				}
			}
		}
		if admins == 0 {
			http.Error(w, "cannot revoke last active key administrator", 409)
			return
		}
	case r.URL.Path == "/v1/nodes" && r.Method == "POST":
		var n nodeConfig
		if decodeJSON(w, r, &n) != nil {
			http.Error(w, "invalid node", 400)
			return
		}
		next.Nodes = append(next.Nodes, n)
		status = 201
	case strings.HasPrefix(r.URL.Path, "/v1/registrations/") && r.Method == "DELETE":
		nodeID := strings.TrimPrefix(r.URL.Path, "/v1/registrations/")
		found := false
		for i, n := range next.Nodes {
			if n.ID == nodeID {
				next.Nodes = append(next.Nodes[:i], next.Nodes[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	if e := validate(next); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	if e := persist(s.configPath, next); e != nil {
		// A rename may have succeeded before directory fsync failed. Reload disk state
		// rather than keep a revoked key usable in this process.
		if b, re := os.ReadFile(s.configPath); re == nil {
			var actual config
			if json.Unmarshal(b, &actual) == nil && validate(actual) == nil {
				s.cfg = actual
			}
		}
		http.Error(w, "config persistence failed; inspect current state", 503)
		return
	}
	s.cfg = next
	ev.Phase = "result"
	ev.Status = status
	if e := s.record(ev); e != nil {
		http.Error(w, "change persisted but audit result failed", 503)
		return
	}
	writeJSON(w, status, result)
}
func (s *server) auditPage(w http.ResponseWriter, r *http.Request) {
	offset, e := strconv.Atoi(r.URL.Query().Get("offset"))
	if r.URL.Query().Get("offset") == "" {
		offset = 0
		e = nil
	}
	if e != nil || offset < 0 {
		http.Error(w, "invalid offset", 400)
		return
	}
	limit, e := strconv.Atoi(r.URL.Query().Get("limit"))
	if r.URL.Query().Get("limit") == "" {
		limit = 100
		e = nil
	}
	if e != nil || limit < 1 || limit > 1000 {
		http.Error(w, "limit must be 1..1000", 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, e := os.Open(s.audit.Name())
	if e != nil {
		http.Error(w, "audit unavailable", 503)
		return
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 1<<20)
	records := []json.RawMessage{}
	line := 0
	for scan.Scan() {
		if line >= offset && len(records) < limit {
			b := append([]byte(nil), scan.Bytes()...)
			if !json.Valid(b) {
				http.Error(w, "audit contains incomplete record", 500)
				return
			}
			records = append(records, b)
		}
		line++
		if len(records) == limit {
			break
		}
	}
	if scan.Err() != nil {
		http.Error(w, "audit read failed", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"records": records, "next_offset": offset + len(records)})
}
