package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// initialize is for a new panel only. Existing configurations are never regenerated.
func initialize() error {
	path := os.Getenv("MCM_PANEL_CONFIG")
	keyPath := os.Getenv("MCM_VAULT_KEY_FILE")
	if path == "" || keyPath == "" {
		return errors.New("MCM_PANEL_CONFIG and MCM_VAULT_KEY_FILE required")
	}
	if _, e := os.Stat(path); e == nil {
		if b, e := os.ReadFile(keyPath); e != nil || len(b) != 32 {
			return errors.New("existing panel requires original 32-byte vault key; restore it from backup")
		}
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	var key [32]byte
	if _, e := rand.Read(key[:]); e != nil {
		return e
	}
	if e := exclusiveFile(keyPath, key[:]); e != nil {
		return fmt.Errorf("vault key exists or cannot be created; inspect incomplete initialization: %w", e)
	}
	var token [32]byte
	if _, e := rand.Read(token[:]); e != nil {
		return e
	}
	text := hex.EncodeToString(token[:])
	digest := sha256.Sum256([]byte(text))
	all := []string{}
	for scope := range scopes {
		all = append(all, scope)
	}
	if e := exclusiveFile(filepath.Join(filepath.Dir(path), "admin-key.txt"), []byte(text+"\n")); e != nil {
		return e
	}
	if e := persist(path, config{Keys: []keyConfig{{ID: "bootstrap", SHA256: hex.EncodeToString(digest[:]), Scopes: all}}, Nodes: []nodeConfig{}}); e != nil {
		return e
	}
	fmt.Println("Panel initialized. Read admin-key.txt from the protected data directory; open /fleet.")
	return nil
}
func exclusiveFile(path string, b []byte) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return e
	}
	return f.Sync()
}
