package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxPluginBytes int64 = 16 << 20

var pluginNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}\.jar$`)

type pluginInfo struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func pluginDirectory(root string) (string, error) {
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	p := filepath.Join(base, "plugins")
	info, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("invalid plugins directory")
	}
	return p, nil
}
func pluginFile(dir, name string) (string, error) {
	if !pluginNamePattern.MatchString(name) || strings.HasPrefix(name, ".") {
		return "", errors.New("invalid plugin filename")
	}
	return filepath.Join(dir, name), nil
}
func inspectPlugin(dir, name string) (pluginInfo, error) {
	if _, err := pluginFile(dir, name); err != nil {
		return pluginInfo{}, err
	}
	root, err := os.OpenRoot(filepath.Dir(dir))
	if err != nil {
		return pluginInfo{}, err
	}
	defer root.Close()
	b, err := readBounded(root, filepath.Join(filepath.Base(dir), name), maxPluginBytes)
	if err != nil {
		return pluginInfo{}, err
	}
	return pluginInfo{Name: name, Size: int64(len(b)), SHA256: sum(b)}, nil
}
func (a agent) plugins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	dir, err := pluginDirectory(a.root)
	if err != nil {
		http.Error(w, "plugins directory unavailable", 503)
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "cannot list plugins", 500)
		return
	}
	result := []pluginInfo{}
	for _, entry := range entries {
		if !pluginNamePattern.MatchString(entry.Name()) {
			continue
		}
		item, err := inspectPlugin(dir, entry.Name())
		if err == nil {
			result = append(result, item)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
func (a agent) plugin(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/plugins/")
	dir, err := pluginDirectory(a.root)
	if err != nil {
		http.Error(w, "plugins directory unavailable", 503)
		return
	}
	_, err = pluginFile(dir, name)
	if err != nil {
		http.Error(w, "invalid plugin name", 400)
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := inspectPlugin(dir, name)
		if err != nil {
			versions, e := a.versions("plugins/" + name)
			if e == nil && len(versions) > 0 {
				jsonReply(w, 200, map[string]any{"plugin": nil, "versions": versions})
				return
			}
			http.NotFound(w, r)
			return
		}
		versions, e := a.versions("plugins/" + name)
		if e != nil {
			http.Error(w, "history unavailable", 503)
			return
		}
		w.Header().Set("ETag", `"`+item.SHA256+`"`)
		jsonReply(w, 200, map[string]any{"plugin": item, "versions": versions})
	case http.MethodPut, http.MethodPost, http.MethodDelete:
		a.mutatePlugin(w, r, name)
	default:
		http.Error(w, "method not allowed", 405)
	}
}
