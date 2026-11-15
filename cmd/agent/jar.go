package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func validateJAR(b []byte) error {
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return errors.New("invalid JAR archive")
	}
	if len(z.File) > 10000 {
		return errors.New("too many archive entries")
	}
	found := false
	seen := map[string]bool{}
	var total uint64
	for _, f := range z.File {
		if seen[f.Name] || !filepath.IsLocal(f.Name) || strings.Contains(f.Name, "\\") || f.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe archive entry")
		}
		seen[f.Name] = true
		if f.UncompressedSize64 > 64<<20 {
			return errors.New("archive entry too large")
		}
		total += f.UncompressedSize64
		if total > 256<<20 {
			return errors.New("archive too large")
		}
		if f.FileInfo().IsDir() {
			continue
		}
		reader, e := f.Open()
		if e != nil {
			return e
		}
		if f.Name == "plugin.yml" || f.Name == "paper-plugin.yml" {
			content, e := io.ReadAll(io.LimitReader(reader, 65537))
			reader.Close()
			if e != nil || len(content) > 65536 {
				return errors.New("invalid plugin descriptor")
			}
			var meta struct {
				Name    string `yaml:"name"`
				Version string `yaml:"version"`
				Main    string `yaml:"main"`
			}
			if yaml.Unmarshal(content, &meta) != nil || meta.Name == "" || meta.Version == "" || meta.Main == "" {
				return errors.New("descriptor requires name, version and main")
			}
			found = true
		} else {
			_, e = io.Copy(io.Discard, io.LimitReader(reader, int64(f.UncompressedSize64)+1))
			reader.Close()
			if e != nil {
				return e
			}
		}
	}
	if !found {
		return errors.New("plugin.yml or paper-plugin.yml required")
	}
	return nil
}
func (a agent) mutatePlugin(w http.ResponseWriter, r *http.Request, name string) {
	operationMu.Lock()
	defer operationMu.Unlock()
	root, err := os.OpenRoot(a.root)
	if err != nil {
		http.Error(w, "root unavailable", 503)
		return
	}
	defer root.Close()
	path := "plugins/" + name
	current, readErr := readBounded(root, path, maxPluginBytes)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		http.Error(w, "unsupported plugin target", 400)
		return
	}
	if exists && r.Header.Get("If-Match") != `"`+sum(current)+`"` {
		http.Error(w, "existing plugin requires matching If-Match", 412)
		return
	}
	if !exists && r.Method != "PUT" && r.Method != "POST" {
		http.NotFound(w, r)
		return
	}
	if err = a.stopped(r.Context()); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if r.Method == "POST" && !exists && r.Header.Get("If-None-Match") != "*" {
		http.Error(w, "restoring deleted plugin requires If-None-Match: *", 412)
		return
	}
	if r.Method == "DELETE" {
		if err = a.saveVersion(path, current); err != nil {
			http.Error(w, "cannot persist rollback version", 503)
			return
		}
		if err = root.Remove(path); err != nil {
			http.Error(w, "cannot remove plugin", 500)
			return
		}
		d, e := root.Open("plugins")
		if e == nil {
			e = d.Sync()
			d.Close()
		}
		if e != nil {
			http.Error(w, "plugin removed but directory sync failed", 500)
			return
		}
		w.WriteHeader(204)
		return
	}

	var b []byte
	switch r.Method {
	case "PUT":
		if r.Header.Get("Content-Type") != "application/java-archive" {
			http.Error(w, "expected application/java-archive", 415)
			return
		}
		b, err = io.ReadAll(http.MaxBytesReader(w, r.Body, maxPluginBytes))
	case "POST":
		var v struct {
			Version string `json:"version"`
		}
		err = strictJSON(w, r, &v)
		if err == nil {
			b, err = a.version(path, v.Version)
		}
	default:
		http.Error(w, "method not allowed", 405)
		return
	}
	if err != nil {
		http.Error(w, "invalid upload or version", 400)
		return
	}
	if err = validateJAR(b); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	// Every mutation requires durable version storage, including the first install.
	if err = a.saveVersion(path, b); err != nil {
		http.Error(w, "version storage unavailable", 503)
		return
	}
	if exists {
		if err = a.saveVersion(path, current); err == nil {
			err = replaceFile(root, path, b)
		}
	} else {
		// Stage and publish with an atomic no-overwrite hard link inside the opened root.
		f, tmp, e := createStaging(root, "plugins", 0640)
		err = e
		if e == nil {
			defer root.Remove(tmp)
			_, err = f.Write(b)
			if err == nil {
				err = f.Sync()
			}
			ce := f.Close()
			if err == nil {
				err = ce
			}
			if err == nil {
				err = root.Link(tmp, path)
			}
			if err == nil {
				d, e := root.Open("plugins")
				err = e
				if e == nil {
					err = d.Sync()
					d.Close()
				}
			}
		}
	}
	if err != nil {
		http.Error(w, "plugin publish failed", 500)
		return
	}
	status := 200
	if !exists {
		status = 201
	}
	jsonReply(w, status, map[string]any{"name": name, "sha256": sum(b), "size": len(b), "restart_required": true})
}
