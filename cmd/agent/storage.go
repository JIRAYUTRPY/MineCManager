package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// One operation at a time, including service starts, prevents mutation/start races.
var operationMu sync.Mutex
var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (a agent) serviceCommand(ctx context.Context, action string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if a.command != nil {
		return a.command(ctx, action)
	}
	return exec.CommandContext(ctx, "systemctl", action, "--", a.service).CombinedOutput()
}
func (a agent) stopped(ctx context.Context) error {
	b, err := a.serviceCommand(ctx, "is-active")
	state := strings.TrimSpace(string(b))
	// Unknown/not-found/failed are not proof that all writers have stopped.
	if state != "inactive" {
		return fmt.Errorf("service must be inactive (state %q, error %v)", state, err)
	}
	return nil
}
func (a agent) stateDirectory() (string, error) {
	p := os.Getenv("MCM_STATE_ROOT")
	if !filepath.IsAbs(p) {
		return "", errors.New("MCM_STATE_ROOT must be absolute")
	}
	root, err := filepath.EvalSymlinks(a.root)
	if err != nil {
		return "", err
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return "", err
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return "", errors.New("state directory must be outside server root")
	}
	return p, nil
}
func strictJSON(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func jsonReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func atomicWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".mcm-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

// Reject all symlinks and special files. os.Root additionally confines actual opens.
func regularPath(root *os.Root, path string) error {
	if !filepath.IsLocal(path) || strings.Contains(path, "\\") {
		return errors.New("invalid path")
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := range parts {
		st, e := root.Lstat(strings.Join(parts[:i+1], "/"))
		if e != nil {
			return e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink denied")
		}
		if i == len(parts)-1 && !st.Mode().IsRegular() {
			return errors.New("regular file required")
		}
	}
	return nil
}
func readBounded(root *os.Root, path string, limit int64) ([]byte, error) {
	if err := regularPath(root, path); err != nil {
		return nil, err
	}
	f, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("file too large")
	}
	return b, err
}
