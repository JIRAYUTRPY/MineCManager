// Package fleet manages privileged SSH hosts independently of legacy Agent nodes.
package fleet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Host struct {
	ID          string `json:"id"`
	PublicIP    string `json:"public_ip"`
	PrivateIP   string `json:"private_ip"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Fingerprint string `json:"fingerprint"`
	Password    string `json:"password,omitempty"`
	PrivateKey  string `json:"private_key,omitempty"`
}
type Cloud struct {
	Token     string `json:"token,omitempty"`
	Account   string `json:"account"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	Bucket    string `json:"bucket"`
}
type Job struct {
	ID       string          `json:"id"`
	Host     string          `json:"host"`
	Action   string          `json:"action"`
	Status   string          `json:"status"`
	Created  time.Time       `json:"created"`
	Finished *time.Time      `json:"finished,omitempty"`
	Input    map[string]any  `json:"input,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	Actor    string          `json:"actor"`
}
type Schedule struct {
	ID         string    `json:"id"`
	Host       string    `json:"host"`
	Server     string    `json:"server"`
	EveryHours int       `json:"every_hours"`
	Retain     int       `json:"retain"`
	R2         bool      `json:"r2"`
	Next       time.Time `json:"next"`
	Enabled    bool      `json:"enabled"`
	Actor      string    `json:"actor"`
}
type state struct {
	Hosts     map[string]Host     `json:"hosts"`
	Jobs      []Job               `json:"jobs"`
	Schedules map[string]Schedule `json:"schedules"`
	Cloud     Cloud               `json:"cloud"`
}
type Store struct {
	mu     sync.Mutex
	path   string
	aead   cipher.AEAD
	data   state
	failed error
}

func Open(path string, key []byte) (*Store, error) {
	if len(key) != 32 {
		return nil, errors.New("vault key must be exactly 32 bytes")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	s := &Store{path: path, aead: a, data: state{Hosts: map[string]Host{}, Schedules: map[string]Schedule{}}}
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil {
		return nil, e
	}
	if len(b) < a.NonceSize() {
		return nil, errors.New("truncated vault")
	}
	plain, e := a.Open(nil, b[:a.NonceSize()], b[a.NonceSize():], []byte("MineCManager fleet v1"))
	if e != nil {
		return nil, errors.New("vault authentication failed")
	}
	if e = json.Unmarshal(plain, &s.data); e != nil {
		return nil, e
	}
	if s.data.Hosts == nil || s.data.Schedules == nil {
		return nil, errors.New("invalid vault state")
	}
	// Never replay a privileged operation whose outcome is unknown after a crash.
	for i := range s.data.Jobs {
		if s.data.Jobs[i].Status == "running" {
			s.data.Jobs[i].Status = "interrupted"
			s.data.Jobs[i].Error = "Panel restarted; inspect remote state before retry"
		}
	}
	if e = s.save(); e != nil {
		return nil, e
	}
	return s, nil
}
func (s *Store) save() error {
	if s.failed != nil {
		return s.failed
	}
	b, e := json.Marshal(s.data)
	if e != nil {
		return e
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return e
	}
	encrypted := s.aead.Seal(nonce, nonce, b, []byte("MineCManager fleet v1"))
	f, e := os.CreateTemp(filepath.Dir(s.path), ".vault-*")
	if e != nil {
		s.failed = e
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(encrypted); e == nil {
		e = f.Sync()
	}
	if e == nil {
		e = f.Close()
	}
	if e == nil {
		e = os.Rename(f.Name(), s.path)
	}
	if e == nil {
		var d *os.File
		d, e = os.Open(filepath.Dir(s.path))
		if e == nil {
			e = d.Sync()
			d.Close()
		}
	}
	if e != nil {
		s.failed = e
	}
	return e
}
func publicHost(h Host) Host { h.Password = ""; h.PrivateKey = ""; return h }
func publicJob(j Job) Job    { j.Input = nil; return j }
