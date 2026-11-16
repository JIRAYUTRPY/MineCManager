package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Guard is called again at job dispatch, so revoked credentials cannot run queued jobs.
type Guard func(actor string) bool
type Audit func(actor, host, action, phase string, status int) error
type Service struct {
	Store  *Store
	Runner Runner
	Guard  Guard
	Audit  Audit
	Client *http.Client
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
var actions = map[string]bool{"metrics": true, "servers": true, "deploy": true, "files": true, "read": true, "write": true, "service": true, "command": true, "backup": true, "backups": true, "restore": true, "dns": true, "r2-list": true, "r2-download": true, "r2-restore": true}

func newID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}
func validHost(h Host) error {
	if !idPattern.MatchString(h.ID) || net.ParseIP(h.Address) == nil || h.Port < 1 || h.Port > 65535 || !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`).MatchString(h.User) {
		return errors.New("valid ID, IP address, SSH port and user required")
	}
	for _, ip := range []string{h.PublicIP, h.PrivateIP} {
		if ip != "" && net.ParseIP(ip) == nil {
			return errors.New("invalid public/private IP")
		}
	}
	if !strings.HasPrefix(h.Fingerprint, "SHA256:") || len(h.Fingerprint) != 50 {
		return errors.New("confirmed SHA256 SSH host fingerprint required")
	}
	if h.Password == "" && h.PrivateKey == "" {
		return errors.New("SSH password or private key required")
	}
	if strings.ContainsAny(h.Password, "\r\n") || len(h.PrivateKey) > 32768 || len(h.Password) > 4096 {
		return errors.New("invalid SSH credentials")
	}
	return nil
}
func (s *Service) Handle(w http.ResponseWriter, r *http.Request, actor string) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/fleet/")
	if e := s.Audit(actor, "", r.Method+" fleet/"+path, "intent", 0); e != nil {
		http.Error(w, "audit unavailable", 503)
		return
	}
	if path == "discover" && r.Method == "POST" {
		var q struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
		}
		if decode(w, r, &q) != nil || net.ParseIP(q.Address) == nil || q.Port < 1 || q.Port > 65535 {
			http.Error(w, "IP and port required", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		fp, e := Discover(ctx, Host{Address: q.Address, Port: q.Port})
		if e != nil {
			http.Error(w, "SSH host key discovery failed", 502)
			return
		}
		reply(w, 200, map[string]string{"fingerprint": fp, "notice": "Compare with VM console before saving. Discovery alone is not identity verification."})
		return
	}
	if path == "zones" && r.Method == "GET" {
		s.Store.mu.Lock()
		cloud := s.Store.data.Cloud
		s.Store.mu.Unlock()
		b, e := s.cloudflare(r.Context(), cloud, "GET", "/zones?per_page=50", nil)
		if e != nil {
			http.Error(w, e.Error(), 502)
			return
		}
		reply(w, 200, b)
		return
	}
	s.Store.mu.Lock()
	defer s.Store.mu.Unlock()
	if s.Store.failed != nil {
		http.Error(w, "vault persistence failed; restart after repair", 503)
		return
	}
	var result any
	status := 200
	mutate := false
	switch {
	case path == "hosts" && r.Method == "GET":
		out := []Host{}
		for _, h := range s.Store.data.Hosts {
			out = append(out, publicHost(h))
		}
		result = out
	case path == "hosts" && (r.Method == "POST" || r.Method == "PUT"):
		var h Host
		if decode(w, r, &h) != nil {
			http.Error(w, "invalid host request", 400)
			return
		}
		if e := validHost(h); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		_, exists := s.Store.data.Hosts[h.ID]
		if exists && r.Method == "POST" {
			http.Error(w, "host ID exists; PUT explicitly replaces credentials", 409)
			return
		}
		s.Store.data.Hosts[h.ID] = h
		result = publicHost(h)
		mutate = true
		status = 201
	case strings.HasPrefix(path, "hosts/") && r.Method == "DELETE":
		id := strings.TrimPrefix(path, "hosts/")
		for _, j := range s.Store.data.Jobs {
			if j.Host == id && (j.Status == "queued" || j.Status == "running") {
				http.Error(w, "host has pending jobs", 409)
				return
			}
		}
		delete(s.Store.data.Hosts, id)
		for k, sc := range s.Store.data.Schedules {
			if sc.Host == id {
				delete(s.Store.data.Schedules, k)
			}
		}
		result = map[string]bool{"removed": true}
		mutate = true
	case path == "jobs" && r.Method == "GET":
		out := []Job{}
		start := len(s.Store.data.Jobs) - 100
		if start < 0 {
			start = 0
		}
		for i := len(s.Store.data.Jobs) - 1; i >= start; i-- {
			out = append(out, publicJob(s.Store.data.Jobs[i]))
		}
		result = out
	case strings.HasPrefix(path, "jobs/") && r.Method == "GET":
		for _, j := range s.Store.data.Jobs {
			if j.ID == strings.TrimPrefix(path, "jobs/") {
				result = publicJob(j)
				break
			}
		}
		if result == nil {
			http.NotFound(w, r)
			return
		}
	case path == "jobs" && r.Method == "POST":
		var q struct {
			Host   string         `json:"host"`
			Action string         `json:"action"`
			Input  map[string]any `json:"input"`
		}
		if decode(w, r, &q) != nil || !actions[q.Action] {
			http.Error(w, "invalid action", 400)
			return
		}
		if q.Action != "dns" && q.Action != "r2-list" && q.Action != "r2-download" {
			if _, ok := s.Store.data.Hosts[q.Host]; !ok {
				http.Error(w, "unknown host", 400)
				return
			}
		}
		pending := 0
		for _, j := range s.Store.data.Jobs {
			if j.Status == "queued" || j.Status == "running" {
				pending++
			}
		}
		if pending >= 100 {
			http.Error(w, "job queue full", 429)
			return
		}
		if q.Input == nil {
			q.Input = map[string]any{}
		}
		delete(q.Input, "upload_url")
		delete(q.Input, "r2_key")
		delete(q.Input, "download_url")
		q.Input["action"] = q.Action
		j := Job{ID: newID(), Host: q.Host, Action: q.Action, Input: q.Input, Status: "queued", Actor: actor, Created: time.Now().UTC()}
		s.Store.data.Jobs = append(s.Store.data.Jobs, j)
		result = publicJob(j)
		mutate = true
		status = 202
	case path == "cloud" && r.Method == "GET":
		c := s.Store.data.Cloud
		result = map[string]any{"account": c.Account, "bucket": c.Bucket, "dns_configured": c.Token != "", "r2_configured": c.AccessKey != "" && c.SecretKey != ""}
	case path == "cloud" && r.Method == "PUT":
		var c Cloud
		if decode(w, r, &c) != nil {
			http.Error(w, "invalid cloud configuration", 400)
			return
		}
		if c.Account != "" && !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(c.Account) {
			http.Error(w, "invalid Cloudflare account ID", 400)
			return
		}
		if c.Bucket != "" && !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`).MatchString(c.Bucket) {
			http.Error(w, "invalid bucket", 400)
			return
		}
		// Empty secret fields retain the previous secret so forms never echo credentials.
		old := s.Store.data.Cloud
		if c.Token == "" {
			c.Token = old.Token
		}
		if c.AccessKey == "" {
			c.AccessKey = old.AccessKey
		}
		if c.SecretKey == "" {
			c.SecretKey = old.SecretKey
		}
		s.Store.data.Cloud = c
		result = map[string]bool{"saved": true}
		mutate = true
	case path == "schedules" && r.Method == "GET":
		result = s.Store.data.Schedules
	case path == "schedules" && r.Method == "PUT":
		var sc Schedule
		if decode(w, r, &sc) != nil || !idPattern.MatchString(sc.ID) || !idPattern.MatchString(sc.Server) || sc.EveryHours < 1 || sc.EveryHours > 8760 || sc.Retain < 1 || sc.Retain > 365 {
			http.Error(w, "schedule requires ID, server, interval 1..8760 hours and retain 1..365", 400)
			return
		}
		if _, ok := s.Store.data.Hosts[sc.Host]; !ok {
			http.Error(w, "unknown host", 400)
			return
		}
		sc.Actor = actor
		sc.Next = time.Now().UTC().Add(time.Duration(sc.EveryHours) * time.Hour)
		s.Store.data.Schedules[sc.ID] = sc
		result = sc
		mutate = true
	case strings.HasPrefix(path, "schedules/") && r.Method == "DELETE":
		delete(s.Store.data.Schedules, strings.TrimPrefix(path, "schedules/"))
		result = map[string]bool{"removed": true}
		mutate = true
	default:
		http.NotFound(w, r)
		return
	}
	if mutate {
		if e := s.Store.save(); e != nil {
			http.Error(w, "vault persistence failed; inspect state before retry", 503)
			return
		}
	}
	if e := s.Audit(actor, "", r.Method+" fleet/"+path, "result", status); e != nil {
		http.Error(w, "audit failed; change may be persisted", 503)
		return
	}
	reply(w, status, result)
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}
func (s *Service) tick(ctx context.Context) {
	s.Store.mu.Lock()
	if s.Store.failed != nil {
		s.Store.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	dirty := false
	for id, sc := range s.Store.data.Schedules {
		if !sc.Enabled || now.Before(sc.Next) {
			continue
		}
		dirty = true
		sc.Next = now.Add(time.Duration(sc.EveryHours) * time.Hour)
		s.Store.data.Schedules[id] = sc
		if !s.Guard(sc.Actor) {
			sc.Enabled = false
			s.Store.data.Schedules[id] = sc
			continue
		}
		busy := false
		for _, j := range s.Store.data.Jobs {
			if j.Host == sc.Host && (j.Status == "queued" || j.Status == "running") {
				busy = true
				break
			}
		}
		if busy {
			continue
		}
		s.Store.data.Jobs = append(s.Store.data.Jobs, Job{ID: newID(), Host: sc.Host, Action: "backup", Status: "queued", Actor: sc.Actor, Created: now, Input: map[string]any{"action": "backup", "server": sc.Server, "retain": sc.Retain, "r2": sc.R2}})
	}
	index := -1
	for i, j := range s.Store.data.Jobs {
		if j.Status == "queued" {
			index = i
			break
		}
	}
	if index < 0 {
		if dirty {
			_ = s.Store.save()
		}
		s.Store.mu.Unlock()
		return
	}
	s.Store.data.Jobs[index].Status = "running"
	j := s.Store.data.Jobs[index]
	h, exists := s.Store.data.Hosts[j.Host]
	cloud := s.Store.data.Cloud
	if e := s.Store.save(); e != nil {
		s.Store.mu.Unlock()
		return
	}
	s.Store.mu.Unlock()
	var result json.RawMessage
	var e error
	if !s.Guard(j.Actor) {
		e = errors.New("job creator no longer has admin:fleet permission")
	} else {
		e = s.Audit(j.Actor, j.Host, "job:"+j.ID+":"+j.Action, "intent", 0)
	}
	if e == nil {
		jobCtx, cancel := context.WithTimeout(ctx, 45*time.Minute)
		switch j.Action {
		case "dns":
			result, e = s.dns(jobCtx, cloud, j.Input)
		case "r2-list", "r2-download":
			result, e = s.r2Read(jobCtx, cloud, j.Input, j.Action)
		default:
			if !exists {
				e = errors.New("host removed")
				break
			}
			input := map[string]any{}
			for k, v := range j.Input {
				input[k] = v
			}
			if j.Action == "backup" && input["r2"] == true {
				server, _ := input["server"].(string)
				if !idPattern.MatchString(server) {
					e = errors.New("invalid server ID")
					break
				}
				key := "minecmanager/" + h.ID + "/" + server + "/" + j.ID + ".tar.gz"
				var u string
				u, e = uploadURL(jobCtx, cloud, key)
				if e != nil {
					break
				}
				input["upload_url"] = u
				input["r2_key"] = key
			}
			if j.Action == "r2-restore" {
				var raw json.RawMessage
				raw, e = s.r2Read(jobCtx, cloud, input, "r2-download")
				if e != nil {
					break
				}
				var link map[string]string
				if e = json.Unmarshal(raw, &link); e != nil {
					break
				}
				input["download_url"] = link["url"]
			}
			result, e = s.Runner.Run(jobCtx, h, input)
		}
		cancel()
	}
	status := 200
	if e != nil {
		status = 502
	}
	if ae := s.Audit(j.Actor, j.Host, "job:"+j.ID+":"+j.Action, "result", status); ae != nil {
		e = errors.New("audit result failed; remote outcome may have succeeded")
	}
	s.Store.mu.Lock()
	defer s.Store.mu.Unlock()
	finished := time.Now().UTC()
	s.Store.data.Jobs[index].Finished = &finished
	s.Store.data.Jobs[index].Input = nil
	if e != nil {
		s.Store.data.Jobs[index].Status = "failed"
		s.Store.data.Jobs[index].Error = e.Error()
	} else {
		s.Store.data.Jobs[index].Status = "succeeded"
		s.Store.data.Jobs[index].Result = result
	}
	// Keep a bounded recent history. Audit remains the durable operation ledger.
	if len(s.Store.data.Jobs) > 1000 {
		s.Store.data.Jobs = s.Store.data.Jobs[len(s.Store.data.Jobs)-1000:]
	}
	_ = s.Store.save()
}
func stringField(m map[string]any, k string) string { v, _ := m[k].(string); return v }
