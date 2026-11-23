package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/JIRAYUTRPY/MineCManager/internal/updates"
)

func (s *server) releaseUpdates(w http.ResponseWriter, r *http.Request, actor string) {
	if r.URL.Path == "/v1/fleet/update-targets" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.cfgMu.RLock()
		ids := make([]string, 0, len(s.cfg.Nodes))
		for _, node := range s.cfg.Nodes {
			ids = append(ids, node.ID)
		}
		s.cfgMu.RUnlock()
		writeJSON(w, 200, ids)
		return
	}
	q, err := updates.Parse(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	id := r.URL.Query().Get("node")
	event := auditEvent{RequestID: requestID(), KeyID: actor, NodeID: id, Phase: "intent", Action: "release " + q.Action + " " + q.Version}
	w.Header().Set("X-Request-ID", event.RequestID)
	if s.record(event) != nil {
		http.Error(w, "audit unavailable", 503)
		return
	}
	var reply updates.Response
	if id == "" {
		reply = updates.Local(r.Context(), q)
	} else {
		reply = s.nodeUpdate(r, id, q)
	}
	event.Phase, event.Status = "result", reply.Code
	if s.record(event) != nil {
		http.Error(w, "audit result unavailable; check the update status", 503)
		return
	}
	reply.Write(w)
}

func (s *server) nodeUpdate(r *http.Request, id string, q updates.Request) updates.Response {
	var node nodeConfig
	s.cfgMu.RLock()
	for _, n := range s.cfg.Nodes {
		if n.ID == id {
			node = n
			break
		}
	}
	s.cfgMu.RUnlock()
	if node.ID == "" {
		return updates.Response{Code: 404, Error: "unknown node"}
	}
	var body io.Reader
	if r.Method == http.MethodPost {
		b, _ := json.Marshal(q)
		body = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 55*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, r.Method, node.URL+"/v1/updates", body)
	if err != nil {
		return updates.Response{Code: 500, Error: "invalid agent configuration"}
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv(node.TokenEnv))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return updates.Response{Code: 502, Error: "agent unavailable; retry status when the service is ready"}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
	if err != nil || len(b) > 16384 {
		return updates.Response{Code: 502, Error: "invalid agent response"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return updates.Response{Code: resp.StatusCode, Error: string(b)}
	}
	if !json.Valid(b) {
		return updates.Response{Code: 502, Error: "invalid agent update status"}
	}
	return updates.Response{Code: resp.StatusCode, Data: b}
}
