package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/JIRAYUTRPY/MineCManager/internal/accounts"
)

type browserIdentity struct{}

const sessionCookie = "mcm_session"

func identity(r *http.Request) *accounts.User {
	u, _ := r.Context().Value(browserIdentity{}).(*accounts.User)
	return u
}

func (s *server) browserUser(r *http.Request) (*accounts.User, error) {
	c, err := r.Cookie(sessionCookie)
	if errors.Is(err, http.ErrNoCookie) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.accounts == nil {
		return nil, errors.New("account database unavailable")
	}
	return s.accounts.Session(r.Context(), c.Value)
}

func localHost(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host == "localhost" || net.ParseIP(strings.Trim(host, "[]")).IsLoopback()
}

func session(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	// Public HTTPS reverse proxies may terminate TLS before forwarding to the Panel.
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true,
		Secure: r.TLS != nil || !localHost(r), SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}

func browserAPI(path string) bool {
	return strings.HasPrefix(path, "/v1/auth/") || strings.HasPrefix(path, "/v1/me/") || strings.HasPrefix(path, "/v1/manage/")
}

var originProtection = http.NewCrossOriginProtection()

// Authorization headers remain the interface for machine clients. Cookie requests are
// checked before dispatch, including raw uploads and the existing administrative APIs.
func (s *server) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	web := browserAPI(r.URL.Path)
	if r.Header.Get("Authorization") != "" {
		if web {
			http.Error(w, "browser session required", 403)
			return
		}
		s.handleAPI(w, r)
		return
	}
	_, cookieErr := r.Cookie(sessionCookie)
	if web || cookieErr == nil {
		if r.Method != "GET" && (r.Header.Get("X-MCM-Request") != "1" || originProtection.Check(r) != nil) {
			http.Error(w, "same-origin browser request required", 403)
			return
		}
		u, err := s.browserUser(r)
		if err != nil {
			http.Error(w, "account database unavailable", 503)
			return
		}
		if u != nil {
			r = r.WithContext(context.WithValue(r.Context(), browserIdentity{}, u))
		}
		if web {
			s.accountAPI(w, r)
			return
		}
		if u == nil {
			http.Error(w, "session expired", 401)
			return
		}
	}
	s.handleAPI(w, r)
}

type attemptBucket struct {
	until time.Time
	count int
}
type loginLimiter struct {
	mu    sync.Mutex
	peers map[string]attemptBucket
	sweep time.Time
}

func (l *loginLimiter) allow(r *http.Request) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.peers == nil {
		l.peers = map[string]attemptBucket{}
	}
	if !now.Before(l.sweep) {
		for k, v := range l.peers {
			if !now.Before(v.until) {
				delete(l.peers, k)
			}
		}
		l.sweep = now.Add(time.Minute)
	}
	b, ok := l.peers[peer]
	if !ok && len(l.peers) >= 4096 {
		return false
	}
	if !now.Before(b.until) {
		b = attemptBucket{until: now.Add(time.Minute)}
	}
	if b.count >= 20 {
		return false
	}
	b.count++
	l.peers[peer] = b
	return true
}

func (s *server) bootstrapKey(key string) bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	want := os.Getenv("MCM_BOOTSTRAP_KEY_ID")
	if want == "" {
		want = "bootstrap-admin"
		for _, k := range s.cfg.Keys {
			if k.ID == "bootstrap" {
				want = k.ID
				break
			}
		}
	}
	r := &http.Request{Header: make(http.Header)}
	r.Header.Set("Authorization", "Bearer "+key)
	id, ok := s.authorize(r, "admin:keys")
	return ok && id == want
}

func smallJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if decodeJSON(w, r, dst) != nil {
		return accounts.ErrInvalid
	}
	return nil
}

func accountError(err error) (int, string) {
	switch {
	case errors.Is(err, accounts.ErrDenied):
		return 403, "invalid credentials or insufficient permission"
	case errors.Is(err, accounts.ErrInvalid):
		return 400, "invalid fields, role, or password (minimum 12 characters)"
	case errors.Is(err, accounts.ErrConflict):
		return 409, "already configured, duplicate name, unknown role, or protected account"
	case errors.Is(err, accounts.ErrFull):
		return 429, "try again shortly"
	default:
		log.Printf("account storage failure: %v", err)
		return 503, "account storage unavailable"
	}
}

func (s *server) accountAPI(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		http.Error(w, "account database unavailable", 503)
		return
	}
	ctx := r.Context()
	u := identity(r)
	path := r.URL.Path
	if path == "/v1/auth/session" && r.Method == "GET" {
		done, err := s.accounts.Bootstrapped(ctx)
		if err != nil {
			http.Error(w, "account database unavailable", 503)
			return
		}
		writeJSON(w, 200, map[string]any{"user": u, "needs_setup": !done})
		return
	}
	open := path == "/v1/auth/bootstrap" || path == "/v1/auth/login"
	if !open && u == nil {
		http.Error(w, "login required", 401)
		return
	}
	if strings.HasPrefix(path, "/v1/manage/") && !u.Has("users:manage") {
		http.Error(w, "user administrator required", 403)
		return
	}
	if open && r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	if open && !s.attempts.allow(r) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many attempts", 429)
		return
	}
	ev := auditEvent{RequestID: requestID(), Action: r.Method + " " + path, Phase: "intent"}
	if u != nil {
		ev.KeyID = "user:" + u.Name
	}
	w.Header().Set("X-Request-ID", ev.RequestID)
	if r.Method != "GET" && s.record(ev) != nil {
		http.Error(w, "audit unavailable", 503)
		return
	}
	status := 200
	var result any = map[string]bool{"saved": true}
	var err error
	var token string
	switch {
	case open:
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Key      string `json:"admin_key"`
		}
		if err = smallJSON(w, r, &in); err != nil {
			break
		}
		if path == "/v1/auth/bootstrap" {
			var done bool
			if done, err = s.accounts.Bootstrapped(ctx); err != nil {
				break
			}
			if done {
				err = accounts.ErrConflict
				break
			}
			if !s.bootstrapKey(in.Key) {
				err = accounts.ErrDenied
				break
			}
			token, err = s.accounts.Bootstrap(ctx, in.Username, in.Password)
			status = 201
		} else {
			token, err = s.accounts.Login(ctx, in.Username, in.Password)
		}
		if err == nil {
			ev.KeyID = "user:" + strings.ToLower(strings.TrimSpace(in.Username))
		}
	case path == "/v1/auth/logout" && r.Method == "POST":
		c, _ := r.Cookie(sessionCookie)
		err = s.accounts.Logout(ctx, c.Value)
	case path == "/v1/me/password" && r.Method == "POST":
		var in struct {
			Old string `json:"current_password"`
			New string `json:"new_password"`
		}
		if err = smallJSON(w, r, &in); err == nil {
			err = s.accounts.Password(ctx, u.Name, in.Old, in.New)
		}
	case strings.HasPrefix(path, "/v1/me/preferences/"):
		id := strings.TrimPrefix(path, "/v1/me/preferences/")
		p := pageByID(id)
		if p == nil || !p.allowed(u) {
			err = accounts.ErrDenied
			break
		}
		switch r.Method {
		case "GET":
			result, err = s.accounts.Preferences(ctx, u.Name, id)
		case "PUT":
			var vals map[string]string
			if err = smallJSON(w, r, &vals); err == nil {
				if !p.validPreferences(vals) {
					err = accounts.ErrInvalid
				} else {
					err = s.accounts.UpdatePreferences(ctx, u.Name, id, vals)
				}
			}
		default:
			status = 405
		}
	case path == "/v1/manage/users" && r.Method == "GET":
		result, err = s.accounts.Users(ctx)
	case path == "/v1/manage/users" && r.Method == "POST":
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err = smallJSON(w, r, &in); err == nil {
			err = s.accounts.Create(ctx, in.Username, in.Password, in.Role)
			status = 201
			if validID.MatchString(in.Username) {
				ev.Action += " " + in.Username
			}
		}
	case strings.HasPrefix(path, "/v1/manage/users/") && r.Method == "PATCH":
		var in accounts.Change
		if err = smallJSON(w, r, &in); err == nil {
			err = s.accounts.Change(ctx, strings.TrimPrefix(path, "/v1/manage/users/"), in)
		}
	case path == "/v1/manage/roles" && r.Method == "GET":
		var roles []accounts.Role
		roles, err = s.accounts.Roles(ctx)
		result = map[string]any{"roles": roles, "assignable": s.accounts.Assignable()}
	case path == "/v1/manage/roles" && r.Method == "POST" || strings.HasPrefix(path, "/v1/manage/roles/") && r.Method == "PUT":
		var in struct {
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		}
		if err = smallJSON(w, r, &in); err == nil {
			create := r.Method == "POST"
			if !create {
				in.Name = strings.TrimPrefix(path, "/v1/manage/roles/")
			} else {
				status = 201
			}
			err = s.accounts.SaveRole(ctx, in.Name, in.Scopes, create)
			if create && validID.MatchString(in.Name) {
				ev.Action += " " + in.Name
			}
		}
	default:
		status = 404
	}
	msg := ""
	if err != nil {
		status, msg = accountError(err)
	}
	if r.Method != "GET" {
		ev.Phase = "result"
		ev.Status = status
		if s.record(ev) != nil {
			http.Error(w, "audit result unavailable; check current state", 503)
			return
		}
	}
	if msg != "" {
		http.Error(w, msg, status)
		return
	}
	if status >= 400 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	if token != "" {
		session(w, r, token, int(accounts.SessionLife.Seconds()))
	}
	if path == "/v1/auth/logout" || path == "/v1/me/password" {
		session(w, r, "", -1)
	}
	writeJSON(w, status, result)
}
