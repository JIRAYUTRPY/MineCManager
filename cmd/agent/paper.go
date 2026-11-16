package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

var playerInventoryRoute = regexp.MustCompile(`^players/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}/inventory(?:/(?:[0-9]|[1-3][0-9]|40))?$`)

func (a agent) paper(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/paper/")
	valid := r.Method == "GET" && (path == "players" || path == "resources" || playerInventoryRoute.MatchString(path) && strings.HasSuffix(path, "/inventory"))
	if r.Method == "PUT" && playerInventoryRoute.MatchString(path) && !strings.HasSuffix(path, "/inventory") {
		valid = true
	}
	if !valid {
		http.NotFound(w, r)
		return
	}
	token := os.Getenv("MCM_PAPER_TOKEN")
	if len(token) < 32 {
		http.Error(w, "Paper bridge not configured", 503)
		return
	}
	body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 16384))
	if e != nil {
		http.Error(w, "request exceeds 16 KiB", 413)
		return
	}
	operationMu.Lock()
	defer operationMu.Unlock()
	req, e := http.NewRequestWithContext(r.Context(), r.Method, "http://127.0.0.1:9082/v1/"+path, bytes.NewReader(body))
	if e != nil {
		http.Error(w, "invalid bridge request", 400)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", r.Header.Get("If-Match"))
	req.Header.Set("X-Request-ID", r.Header.Get("X-Request-ID"))
	client := a.paperHTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, e := client.Do(req)
	if e != nil {
		http.Error(w, "Paper bridge unavailable; inspect inventory before retrying writes", 502)
		return
	}
	defer response.Body.Close()
	b, e := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if e != nil || len(b) > 4<<20 {
		http.Error(w, "invalid bridge response", 502)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(b)
}
