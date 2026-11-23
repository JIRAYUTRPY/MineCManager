package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JIRAYUTRPY/MineCManager/internal/accounts"
	"github.com/JIRAYUTRPY/MineCManager/internal/fleet"
	"github.com/JIRAYUTRPY/MineCManager/internal/lock"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type keyConfig struct {
	ID        string     `json:"id"`
	SHA256    string     `json:"sha256"`
	Scopes    []string   `json:"scopes"`
	Nodes     []string   `json:"nodes,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Revoked   bool       `json:"revoked,omitempty"`
}
type nodeConfig struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	TokenEnv string `json:"token_env"`
}
type config struct {
	Keys  []keyConfig  `json:"keys"`
	Nodes []nodeConfig `json:"nodes"`
}
type server struct {
	accounts    *accounts.Store
	attempts    loginLimiter
	cfg         config
	client      *http.Client
	audit       *os.File
	mu          sync.Mutex
	cfgMu       sync.RWMutex
	configPath  string
	fleet       *fleet.Service
	auditFailed error
}
type auditEvent struct {
	At        time.Time `json:"at"`
	RequestID string    `json:"request_id"`
	Phase     string    `json:"phase"`
	KeyID     string    `json:"key_id"`
	NodeID    string    `json:"node_id"`
	Action    string    `json:"action"`
	Status    int       `json:"status"`
}

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var scopes = map[string]bool{"admin:fleet": true, "nodes:read": true, "nodes:control": true, "plugins:read": true, "plugins:write": true, "configs:read": true, "configs:write": true, "backups:read": true, "backups:write": true, "backups:restore": true, "admin:keys": true, "admin:nodes": true, "audit:read": true, "players:read": true, "inventory:read": true, "inventory:write": true, "resources:read": true}

func validate(c config) error {
	if len(c.Keys) == 0 {
		return errors.New("at least one key required")
	}
	keys := map[string]bool{}
	digests := map[string]bool{}
	nodes := map[string]bool{}
	for _, k := range c.Keys {
		if !validID.MatchString(k.ID) || keys[k.ID] {
			return errors.New("duplicate or invalid key ID")
		}
		keys[k.ID] = true
		b, e := hex.DecodeString(k.SHA256)
		if e != nil || len(b) != 32 || digests[strings.ToLower(k.SHA256)] {
			return fmt.Errorf("invalid or duplicate SHA256 for key %s", k.ID)
		}
		digests[strings.ToLower(k.SHA256)] = true
		if len(k.Scopes) == 0 {
			return fmt.Errorf("missing scopes for key %s", k.ID)
		}
		for _, scope := range k.Scopes {
			if !scopes[scope] {
				return fmt.Errorf("unsupported scope %s", scope)
			}
			if len(k.Nodes) > 0 && strings.HasPrefix(scope, "admin:") {
				return errors.New("administrative keys cannot be node restricted")
			}
		}
		for _, id := range k.Nodes {
			if !validID.MatchString(id) {
				return errors.New("invalid key node restriction")
			}
		}
	}
	for _, n := range c.Nodes {
		if !validID.MatchString(n.ID) || nodes[n.ID] || n.TokenEnv == "" {
			return errors.New("duplicate or invalid node")
		}
		nodes[n.ID] = true
		u, e := url.Parse(n.URL)
		if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Hostname() == "" {
			return fmt.Errorf("invalid URL for node %s", n.ID)
		}
		ip := net.ParseIP(u.Hostname())
		local := ip != nil && ip.IsLoopback()
		if u.Scheme != "https" && !(u.Scheme == "http" && local) {
			return fmt.Errorf("node %s needs HTTPS or loopback HTTP", n.ID)
		}
		if len(os.Getenv(n.TokenEnv)) < 32 {
			return fmt.Errorf("node %s token must contain at least 32 characters", n.ID)
		}
	}
	return nil
}
func (s *server) authorize(r *http.Request, scope string) (string, bool) {
	if u := identity(r); u != nil {
		return "user:" + u.Name, u.Has(scope)
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(header, "Bearer ")
	if len(token) < 32 {
		return "", false
	}
	hash := sha256.Sum256([]byte(token))
	for _, k := range s.cfg.Keys {
		expected, _ := hex.DecodeString(k.SHA256)
		if subtle.ConstantTimeCompare(hash[:], expected) == 1 {
			if k.Revoked || (k.ExpiresAt != nil && !time.Now().Before(*k.ExpiresAt)) {
				return k.ID, false
			}
			for _, sc := range k.Scopes {
				if sc == scope {
					return k.ID, true
				}
			}
			return k.ID, false
		}
	}
	return "", false
}
func (s *server) allowedNode(key, node string) bool {
	// Human roles grant node scopes across the Panel. API keys may restrict node IDs.
	if strings.HasPrefix(key, "user:") {
		return true
	}
	for _, k := range s.cfg.Keys {
		if k.ID == key {
			if len(k.Nodes) == 0 {
				return true
			}
			for _, n := range k.Nodes {
				if n == node {
					return true
				}
			}
		}
	}
	return false
}
func (s *server) record(ev auditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.auditFailed != nil {
		return s.auditFailed
	}
	ev.At = time.Now().UTC()
	data, e := json.Marshal(ev)
	if e != nil {
		return e
	}
	if s.audit == nil {
		s.auditFailed = errors.New("audit unavailable")
		return s.auditFailed
	}
	if _, e = s.audit.Write(append(data, '\n')); e != nil {
		s.auditFailed = e
		return e
	}
	s.auditFailed = s.audit.Sync()
	return s.auditFailed
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func requestID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func (s *server) handleAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(r.URL.Path, "/v1/fleet/") {
		s.cfgMu.RLock()
		id, ok := s.authorize(r, "admin:fleet")
		s.cfgMu.RUnlock()
		if !ok {
			http.Error(w, "forbidden: admin:fleet required", 403)
			return
		}
		if r.URL.Path == "/v1/fleet/updates" || r.URL.Path == "/v1/fleet/update-targets" {
			s.releaseUpdates(w, r, id)
			return
		}
		if s.fleet == nil {
			http.Error(w, "fleet not configured", 503)
			return
		}
		s.fleet.Handle(w, r, id)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/keys") || r.URL.Path == "/v1/audit" || (r.URL.Path == "/v1/nodes" && r.Method != "GET") || strings.HasPrefix(r.URL.Path, "/v1/registrations/") {
		s.admin(w, r)
		return
	}
	// Config generations are immutable: admin writes publish a deep-copied value.
	// Never hold this lock across request-body reads or a remote agent call.
	s.cfgMu.RLock()
	view := &server{cfg: s.cfg}
	s.cfgMu.RUnlock()
	event := auditEvent{RequestID: requestID(), Phase: "intent", Action: r.Method + " " + r.URL.Path}
	w.Header().Set("X-Request-ID", event.RequestID)
	if r.Method == "GET" && r.URL.Path == "/v1/nodes" {
		id, ok := view.authorize(r, "nodes:read")
		event.KeyID = id
		if !ok {
			event.Status = 403
			_ = s.record(event)
			http.Error(w, "forbidden", 403)
			return
		}
		if e := s.record(event); e != nil {
			http.Error(w, "audit unavailable", 503)
			return
		}
		out := []map[string]string{}
		for _, n := range view.cfg.Nodes {
			if view.allowedNode(id, n.ID) {
				out = append(out, map[string]string{"id": n.ID})
			}
		}
		writeJSON(w, 200, out)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "v1" || parts[1] != "nodes" || !validID.MatchString(parts[2]) {
		http.NotFound(w, r)
		return
	}
	nodeID := parts[2]
	endpoint := strings.Join(parts[3:], "/")
	scope := ""
	limit := int64(1 << 20)
	switch {
	case endpoint == "paper/players" && r.Method == "GET":
		scope = "players:read"
	case endpoint == "paper/resources" && r.Method == "GET":
		scope = "resources:read"
	case len(parts) == 7 && parts[3] == "paper" && parts[4] == "players" && parts[6] == "inventory" && r.Method == "GET":
		scope = "inventory:read"
	case len(parts) == 8 && parts[3] == "paper" && parts[4] == "players" && parts[6] == "inventory" && r.Method == "PUT":
		scope = "inventory:write"
		limit = 16384
	case endpoint == "status" && r.Method == "GET":
		scope = "nodes:read"
	case endpoint == "metrics" && r.Method == "GET":
		scope = "nodes:read"
	case endpoint == "files" && r.Method == "GET":
		scope = "nodes:read"
	case endpoint == "actions" && r.Method == "POST":
		scope = "nodes:control"
	case (endpoint == "plugins" || len(parts) == 5 && parts[3] == "plugins") && r.Method == "GET":
		scope = "plugins:read"
	case len(parts) == 5 && parts[3] == "plugins" && (r.Method == "PUT" || r.Method == "POST" || r.Method == "DELETE"):
		scope = "plugins:write"
		limit = 16 << 20
	case endpoint == "config" && r.Method == "GET":
		scope = "configs:read"
	case endpoint == "config" && (r.Method == "PUT" || r.Method == "POST"):
		scope = "configs:write"
	case (endpoint == "backup/manifest" || endpoint == "backups") && r.Method == "GET":
		scope = "backups:read"
	case endpoint == "backups" && r.Method == "POST":
		scope = "backups:write"
	case len(parts) == 6 && parts[3] == "backups" && parts[5] == "restore" && r.Method == "POST":
		scope = "backups:restore"
	default:
		http.NotFound(w, r)
		return
	}
	id, ok := view.authorize(r, scope)
	event.KeyID = id
	event.NodeID = nodeID
	if endpoint == "config" {
		event.Action += " path=" + r.URL.Query().Get("path")
	}
	if !ok || !view.allowedNode(id, nodeID) {
		event.Status = 403
		_ = s.record(event)
		http.Error(w, "forbidden", 403)
		return
	}
	var node *nodeConfig
	for i := range view.cfg.Nodes {
		if view.cfg.Nodes[i].ID == nodeID {
			node = &view.cfg.Nodes[i]
			break
		}
	}
	if node == nil {
		http.NotFound(w, r)
		return
	}
	var body io.Reader
	if r.Method != "GET" {
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
		if e != nil {
			http.Error(w, "request too large", 413)
			return
		}
		if endpoint == "actions" {
			var v struct {
				Action string `json:"action"`
			}
			if json.Unmarshal(b, &v) != nil || (v.Action != "start" && v.Action != "stop" && v.Action != "restart") {
				http.Error(w, "invalid action", 400)
				return
			}
			event.Action = v.Action
		}
		body = strings.NewReader(string(b))
	}
	target := node.URL + "/v1/" + endpoint
	query := url.Values{}
	if endpoint == "config" || endpoint == "files" {
		query.Set("path", r.URL.Query().Get("path"))
	}
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, e := http.NewRequestWithContext(r.Context(), r.Method, target, body)
	if e != nil {
		http.Error(w, "request failed", 500)
		return
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv(node.TokenEnv))
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	req.Header.Set("If-Match", r.Header.Get("If-Match"))
	req.Header.Set("If-None-Match", r.Header.Get("If-None-Match"))
	req.Header.Set("X-Request-ID", event.RequestID)
	if e = s.record(event); e != nil {
		http.Error(w, "audit unavailable; request not dispatched", 503)
		return
	}
	resp, e := s.client.Do(req)
	event.Phase = "result"
	if e != nil {
		event.Status = 502
		_ = s.record(event)
		http.Error(w, "agent unavailable; mutation outcome may be unknown; inspect state before retry", 502)
		return
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if e != nil || len(b) > 32<<20 {
		event.Status = 502
		_ = s.record(event)
		http.Error(w, "invalid agent response; inspect state before retry", 502)
		return
	}
	event.Status = resp.StatusCode
	if e = s.record(event); e != nil {
		http.Error(w, "audit result failed; operation may have completed; inspect state before retry", 503)
		return
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("ETag", resp.Header.Get("ETag"))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(b)
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		if e := initialize(); e != nil {
			log.Fatal(e)
		}
		return
	}
	path := os.Getenv("MCM_PANEL_CONFIG")
	if path == "" {
		log.Fatal("MCM_PANEL_CONFIG required")
	}
	lease, e := lock.Acquire(path + ".lock")
	if e != nil {
		log.Fatal(e)
	}
	defer lease.Close()
	data, e := os.ReadFile(path)
	if e != nil {
		log.Fatal(e)
	}
	var cfg config
	if e = json.Unmarshal(data, &cfg); e != nil {
		log.Fatal(e)
	}
	if e = validate(cfg); e != nil {
		log.Fatal(e)
	}
	auditPath := os.Getenv("MCM_AUDIT_PATH")
	if auditPath == "" {
		log.Fatal("MCM_AUDIT_PATH required")
	}
	f, e := openAudit(auditPath)
	if e != nil {
		log.Fatal(e)
	}
	defer f.Close()
	client, e := agentClient()
	if e != nil {
		log.Fatal(e)
	}
	dbPath := os.Getenv("MCM_ACCOUNTS_DB")
	if dbPath == "" {
		dbPath = filepath.Join(filepath.Dir(path), "accounts.sqlite3")
	}
	users, e := accounts.Open(dbPath, scopes)
	if e != nil {
		log.Fatal("open panel account database: ", e)
	}
	defer users.Close()
	s := &server{cfg: cfg, audit: f, configPath: path, client: client, accounts: users}
	if vaultPath := os.Getenv("MCM_FLEET_VAULT"); vaultPath != "" {
		key, e := os.ReadFile(os.Getenv("MCM_VAULT_KEY_FILE"))
		if e != nil {
			log.Fatal(e)
		}
		store, e := fleet.Open(vaultPath, key)
		if e != nil {
			log.Fatal(e)
		}
		s.fleet = &fleet.Service{Store: store, Runner: fleet.SSHRunner{}, Guard: s.fleetGuard, Audit: func(actor, host, action, phase string, status int) error {
			return s.record(auditEvent{RequestID: requestID(), KeyID: actor, NodeID: host, Action: action, Phase: phase, Status: status})
		}}
		go s.fleet.Run(context.Background())
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/", s.handle)
	mux.HandleFunc("/", s.ui)
	listen := os.Getenv("MCM_PANEL_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:9080"
	}
	log.Fatal((&http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 120 * time.Second, WriteTimeout: 11 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8192}).ListenAndServe())
}

func (s *server) fleetGuard(actor string) bool {
	if name, ok := strings.CutPrefix(actor, "user:"); ok {
		if s.accounts == nil {
			return false
		}
		u, err := s.accounts.User(context.Background(), name)
		return err == nil && u != nil && u.Has("admin:fleet")
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	for _, k := range s.cfg.Keys {
		if k.ID == actor && !k.Revoked && (k.ExpiresAt == nil || time.Now().Before(*k.ExpiresAt)) {
			for _, scope := range k.Scopes {
				if scope == "admin:fleet" {
					return true
				}
			}
		}
	}
	return false
}
