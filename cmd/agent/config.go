package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const maxConfigBytes = 1 << 20

func configPath(p string) bool {
	if !filepath.IsLocal(p) || filepath.ToSlash(filepath.Clean(p)) != p || strings.Contains(p, "\\") {
		return false
	}
	if p == "server.properties" || p == "bukkit.yml" || p == "spigot.yml" {
		return true
	}
	if !strings.HasPrefix(p, "plugins/") && !strings.HasPrefix(p, "config/") {
		return false
	}
	switch filepath.Ext(p) {
	case ".yml", ".yaml", ".json", ".properties":
		return true
	}
	return false
}
func validateConfig(path string, b []byte) error {
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return errors.New("UTF-8 text required")
	}
	switch filepath.Ext(path) {
	case ".json":
		if !json.Valid(b) {
			return errors.New("invalid JSON")
		}
	case ".yml", ".yaml":
		d := yaml.NewDecoder(bytes.NewReader(b))
		var v any
		if err := d.Decode(&v); err != nil {
			return err
		}
		if d.Decode(new(any)) != io.EOF {
			return errors.New("one YAML document required")
		}
	}
	return nil
}
func (a agent) saveVersion(path string, b []byte) error {
	state, err := a.stateDirectory()
	if err != nil {
		return err
	}
	dir := filepath.Join(state, "versions", sum([]byte(path)))
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err = atomicWrite(filepath.Join(dir, sum(b)), b); err != nil {
		return err
	}
	// Persist the first-created history directories before changing the live file.
	if err = syncDir(filepath.Join(state, "versions")); err != nil {
		return err
	}
	return syncDir(state)
}
func (a agent) version(path, hash string) ([]byte, error) {
	if !digestPattern.MatchString(hash) {
		return nil, errors.New("invalid version")
	}
	state, err := a.stateDirectory()
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(state)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	b, err := readBounded(root, filepath.Join("versions", sum([]byte(path)), hash), maxPluginBytes)
	if err == nil && sum(b) != hash {
		return nil, errors.New("version checksum mismatch")
	}
	return b, err
}
func (a agent) versions(path string) ([]string, error) {
	state, err := a.stateDirectory()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(state, "versions", sum([]byte(path))))
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if digestPattern.MatchString(e.Name()) && e.Type().IsRegular() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// Write through a root descriptor; rename never follows a substituted destination symlink.
func replaceFile(root *os.Root, path string, b []byte) error {
	if err := regularPath(root, path); err != nil {
		return err
	}
	f, tmp, err := createStaging(root, filepath.Dir(path), 0660)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	defer f.Close()
	if err = f.Chmod(0660); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = root.Rename(tmp, path); err != nil {
		return err
	}
	d, err := root.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (a agent) configFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if !configPath(path) {
		http.Error(w, "unsupported config path", 400)
		return
	}
	operationMu.Lock()
	defer operationMu.Unlock()
	root, err := os.OpenRoot(a.root)
	if err != nil {
		http.Error(w, "root unavailable", 503)
		return
	}
	defer root.Close()
	current, err := readBounded(root, path, maxConfigBytes)
	if err != nil {
		http.Error(w, "config not found or unsupported", 404)
		return
	}
	hash := sum(current)
	w.Header().Set("ETag", `"`+hash+`"`)
	if r.Method == "GET" {
		versions, err := a.versions(path)
		if err != nil {
			http.Error(w, "history unavailable", 503)
			return
		}
		jsonReply(w, 200, map[string]any{"path": path, "content": string(current), "sha256": hash, "versions": versions})
		return
	}
	if r.Method != "PUT" && r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	if r.Header.Get("If-Match") != `"`+hash+`"` {
		http.Error(w, "If-Match must match current SHA-256", 412)
		return
	}
	if err := a.stopped(r.Context()); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	var b []byte
	if r.Method == "PUT" {
		b, err = io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigBytes))
	} else {
		var v struct {
			Version string `json:"version"`
		}
		err = strictJSON(w, r, &v)
		if err == nil {
			b, err = a.version(path, v.Version)
		}
	}
	if err != nil || len(b) > maxConfigBytes {
		http.Error(w, "invalid config or version", 400)
		return
	}
	if err = validateConfig(path, b); err != nil {
		http.Error(w, "invalid config syntax: "+err.Error(), 400)
		return
	}
	if err = a.saveVersion(path, current); err != nil {
		http.Error(w, "cannot persist rollback version", 503)
		return
	}
	if err = replaceFile(root, path, b); err != nil {
		http.Error(w, "config write failed", 500)
		return
	}
	w.Header().Set("ETag", `"`+sum(b)+`"`)
	jsonReply(w, 200, map[string]any{"sha256": sum(b), "restart_required": true})
}
