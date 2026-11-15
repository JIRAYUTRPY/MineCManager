package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const snapshotLimit int64 = 10 << 30

// Copy the complete server root (including custom world names), rejecting symlinks
// and special files instead of silently producing an incomplete backup.
func copySnapshot(source, dest string, expected []backupEntry) ([]backupEntry, error) {
	root, err := os.OpenRoot(source)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	out, err := os.OpenRoot(dest)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	entries := []backupEntry{}
	var total int64
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if path == "." {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("snapshot contains symlink")
		}
		if d.IsDir() {
			if e := out.MkdirAll(path, 0770); e != nil {
				return e
			}
			return out.Chmod(path, 0770)
		}
		if len(entries) >= 100000 {
			return errors.New("snapshot file count exceeded")
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() {
			return errors.New("snapshot contains special file")
		}
		if st.Size() > snapshotLimit-total {
			return errors.New("snapshot size limit exceeded")
		}
		// Hash while copying, then verify the destination bytes against the manifest.
		in, e := root.Open(path)
		if e != nil {
			return e
		}
		defer in.Close()
		f, e := out.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0660)
		if e != nil {
			return e
		}
		if e = f.Chmod(0660); e != nil {
			f.Close()
			return e
		}
		h := newHashWriter(f)
		n, e := io.Copy(h, io.LimitReader(in, snapshotLimit-total+1))
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		total += n
		if total > snapshotLimit {
			return errors.New("snapshot size limit exceeded")
		}
		entries = append(entries, backupEntry{Path: path, Size: n, SHA256: h.digest()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	if expected != nil {
		sort.Slice(expected, func(i, j int) bool { return expected[i].Path < expected[j].Path })
		if len(expected) != len(entries) {
			return nil, errors.New("snapshot manifest count mismatch")
		}
		for i := range entries {
			if entries[i] != expected[i] {
				return nil, errors.New("snapshot checksum mismatch")
			}
		}
	}
	if err = syncTree(dest); err != nil {
		return nil, err
	}
	return entries, nil
}
func syncTree(path string) error {
	return filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return syncDir(p)
		}
		return nil
	})
}

type snapshot struct {
	ID      string        `json:"id"`
	Created time.Time     `json:"created"`
	Files   []backupEntry `json:"files"`
}

func (a agent) backups(w http.ResponseWriter, r *http.Request) {
	operationMu.Lock()
	defer operationMu.Unlock()
	state, err := a.stateDirectory()
	if err != nil {
		http.Error(w, "state directory unavailable", 503)
		return
	}
	dir := filepath.Join(state, "backups")
	if err = os.MkdirAll(dir, 0700); err != nil {
		http.Error(w, "backup storage unavailable", 503)
		return
	}
	if err = syncDir(state); err != nil {
		http.Error(w, "cannot persist backup storage directory", 503)
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/v1/backups")
	if suffix == "" && r.Method == "GET" {
		entries, e := os.ReadDir(dir)
		if e != nil {
			http.Error(w, "cannot list backups", 500)
			return
		}
		ids := []string{}
		for _, entry := range entries {
			if identifier.MatchString(entry.Name()) && entry.IsDir() {
				ids = append(ids, entry.Name())
			}
		}
		jsonReply(w, 200, ids)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	if err = a.stopped(r.Context()); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if suffix == "" {
		var idBytes [16]byte
		if _, err = rand.Read(idBytes[:]); err != nil {
			http.Error(w, "random source failed", 500)
			return
		}
		id := hex.EncodeToString(idBytes[:])
		stage, e := os.MkdirTemp(dir, ".pending-")
		if e != nil {
			http.Error(w, "cannot stage backup", 500)
			return
		}
		defer os.RemoveAll(stage)
		data := filepath.Join(stage, "data")
		if e = os.Mkdir(data, 0750); e != nil {
			http.Error(w, "cannot stage backup", 500)
			return
		}
		manifest, e := copySnapshot(a.root, data, nil)
		if e != nil {
			http.Error(w, "backup failed: "+e.Error(), 500)
			return
		}
		result := snapshot{ID: id, Created: time.Now().UTC(), Files: manifest}
		b, _ := json.Marshal(result)
		if len(b) > 16<<20 {
			http.Error(w, "snapshot manifest exceeds 16 MiB limit", 413)
			return
		}
		if e = atomicWrite(filepath.Join(stage, "manifest.json"), b); e == nil {
			e = os.Rename(stage, filepath.Join(dir, id))
		}
		if e == nil {
			e = syncDir(dir)
		}
		if e != nil {
			http.Error(w, "cannot publish backup", 500)
			return
		}
		jsonReply(w, 201, result)
		return
	}
	parts := strings.Split(strings.TrimPrefix(suffix, "/"), "/")
	if len(parts) != 2 || !identifier.MatchString(parts[0]) || parts[1] != "restore" {
		http.NotFound(w, r)
		return
	}
	var input struct {
		Confirm string `json:"confirm"`
	}
	if strictJSON(w, r, &input) != nil || input.Confirm != parts[0] {
		http.Error(w, "confirm must equal backup ID", 400)
		return
	}
	source := filepath.Join(dir, parts[0])
	st, e := os.Lstat(source)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		http.NotFound(w, r)
		return
	}
	sr, e := os.OpenRoot(source)
	if e != nil {
		http.Error(w, "backup unavailable", 400)
		return
	}
	defer sr.Close()
	b, e := readBounded(sr, "manifest.json", 32<<20)
	var snap snapshot
	if e != nil || len(b) > 32<<20 || json.Unmarshal(b, &snap) != nil || snap.ID != parts[0] || snap.Files == nil {
		http.Error(w, "invalid backup manifest", 400)
		return
	}
	// Root replacement requires write permission on its parent; never delete the old root.
	root, e := filepath.Abs(a.root)
	if e != nil {
		http.Error(w, "invalid root", 500)
		return
	}
	root = filepath.Clean(root)
	st, e = os.Lstat(root)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || root == "/" {
		http.Error(w, "restore requires a real server directory", 409)
		return
	}
	stage, e := os.MkdirTemp(filepath.Dir(root), ".mcm-restore-")
	if e != nil {
		http.Error(w, "cannot stage restore next to server root", 500)
		return
	}
	defer os.RemoveAll(stage)
	if e = os.Chmod(stage, st.Mode().Perm()); e != nil {
		http.Error(w, "restore permissions failed", 500)
		return
	}
	if _, e = copySnapshot(filepath.Join(source, "data"), stage, snap.Files); e != nil {
		http.Error(w, "backup verification failed: "+e.Error(), 400)
		return
	}
	previous := stage + "-previous"
	if e = os.Rename(root, previous); e != nil {
		http.Error(w, "cannot preserve previous server root", 500)
		return
	}
	if e = os.Rename(stage, root); e != nil {
		rollbackErr := os.Rename(previous, root)
		if rollbackErr != nil {
			http.Error(w, "restore and rollback failed; recover previous root manually", 500)
			return
		}
		http.Error(w, "restore failed; previous root recovered", 500)
		return
	}
	if e = syncDir(filepath.Dir(root)); e != nil {
		http.Error(w, "restore applied but directory sync failed; inspect before start", 500)
		return
	}
	jsonReply(w, 200, map[string]any{"restored": snap.ID, "previous_root": previous, "restart_required": true})
}
