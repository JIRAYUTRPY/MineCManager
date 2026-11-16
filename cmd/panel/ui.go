package main

import (
	"embed"
	"net/http"
)

//go:embed ui.html fleet.html
var uiFiles embed.FS

func (s *server) ui(w http.ResponseWriter, r *http.Request) {
	if (r.URL.Path != "/" && r.URL.Path != "/fleet") || r.Method != "GET" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	name := "ui.html"
	if r.URL.Path == "/fleet" {
		name = "fleet.html"
	}
	b, _ := uiFiles.ReadFile(name)
	_, _ = w.Write(b)
}
