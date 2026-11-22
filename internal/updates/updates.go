// Package updates forwards a small set of release operations to the local installer.
package updates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"time"
)

type Request struct {
	Action  string `json:"action"`
	Version string `json:"version,omitempty"`
}

type Response struct {
	Code  int             `json:"code"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

var stable = regexp.MustCompile(`^v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

func Parse(r *http.Request) (Request, error) {
	var q Request
	if r.Method == http.MethodGet {
		q.Action = "status"
		return q, nil
	}
	if r.Method != http.MethodPost {
		return q, errors.New("use GET or POST")
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 1025))
	if err != nil || len(b) > 1024 {
		return q, errors.New("update request exceeds 1 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&q); err != nil || d.Decode(new(any)) != io.EOF {
		return q, errors.New("invalid update request")
	}
	if q.Action == "check" && q.Version == "" || q.Action == "apply" && stable.MatchString(q.Version) {
		return q, nil
	}
	return q, errors.New("use check or apply with the selected stable version")
}

func Local(ctx context.Context, q Request) Response {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", "/run/mcm-release.sock")
	if err != nil {
		return Response{Code: 503, Error: "release updater is unavailable; this host needs a setup.py managed installation"}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(45 * time.Second))
	var reply Response
	if json.NewEncoder(conn).Encode(q) != nil || json.NewDecoder(io.LimitReader(conn, 16384)).Decode(&reply) != nil || reply.Code < 200 || reply.Code > 599 {
		return Response{Code: 502, Error: "invalid release updater response"}
	}
	return reply
}

func (reply Response) Write(w http.ResponseWriter) {
	if reply.Error != "" {
		http.Error(w, reply.Error, reply.Code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(reply.Code)
	_, _ = w.Write(reply.Data)
}

func Handle(w http.ResponseWriter, r *http.Request) {
	q, err := Parse(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	Local(r.Context(), q).Write(w)
}
