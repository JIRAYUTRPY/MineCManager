package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type bridgeTransport func(*http.Request) (*http.Response, error)

func (f bridgeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPaperProxyLimitsAndCredentials(t *testing.T) {
	t.Setenv("MCM_PAPER_TOKEN", strings.Repeat("p", 32))
	calls := 0
	a := agent{paperHTTP: &http.Client{Transport: bridgeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "127.0.0.1:9082" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("p", 32) || r.Header.Get("If-Match") != `"hash"` {
			t.Fatal("incorrect bridge proxy")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"slot":0}`))}, nil
	})}}
	uuid := "00000000-0000-0000-0000-000000000001"
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{{"PUT", "players/" + uuid + "/inventory/0", `{"material":"STONE","amount":1}`, 200}, {"PUT", "players/" + uuid + "/inventory/41", "{}", 404}, {"GET", "../status", "", 404}, {"PUT", "players/" + uuid + "/inventory/0", strings.Repeat("x", 16385), 413}} {
		r := httptest.NewRequest(tc.method, "/v1/paper/"+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer external-key")
		r.Header.Set("If-Match", `"hash"`)
		w := httptest.NewRecorder()
		a.paper(w, r)
		if w.Code != tc.want {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal("unexpected dispatch")
	}
}
