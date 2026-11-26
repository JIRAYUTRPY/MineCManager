// Package accounts stores human identities and per-page preferences on the Panel VM.
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite"
)

var (
	ErrInvalid  = errors.New("invalid account input")
	ErrDenied   = errors.New("invalid credentials or permission")
	ErrConflict = errors.New("account or role conflicts with existing state")
	ErrFull     = errors.New("password verification busy")
)

const SessionLife = 12 * time.Hour

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,39}$`)
var hashSlots = make(chan struct{}, 3)

type User struct {
	Name    string   `json:"username"`
	Role    string   `json:"role"`
	Enabled bool     `json:"enabled"`
	Scopes  []string `json:"scopes,omitempty"`
}

func (u User) Has(scope string) bool { return u.Enabled && slices.Contains(u.Scopes, scope) }

type Role struct {
	Name    string   `json:"name"`
	Builtin bool     `json:"builtin"`
	Scopes  []string `json:"scopes"`
}

type Store struct {
	db      *sql.DB
	catalog []string
}

const schema = `
CREATE TABLE IF NOT EXISTS roles (
 name TEXT PRIMARY KEY, builtin INTEGER NOT NULL, scopes TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
 username TEXT PRIMARY KEY, role TEXT NOT NULL REFERENCES roles(name), enabled INTEGER NOT NULL,
 salt BLOB NOT NULL, verifier BLOB NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS one_admin ON users(role) WHERE role='admin';
CREATE TABLE IF NOT EXISTS bootstrap (id INTEGER PRIMARY KEY CHECK(id=1));
CREATE TABLE IF NOT EXISTS sessions (
 digest BLOB PRIMARY KEY, username TEXT NOT NULL REFERENCES users(username), expires INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(username);
CREATE INDEX IF NOT EXISTS sessions_expiry ON sessions(expires);
CREATE TABLE IF NOT EXISTS preferences (
 username TEXT NOT NULL REFERENCES users(username), page TEXT NOT NULL, field TEXT NOT NULL, value TEXT NOT NULL,
 PRIMARY KEY(username,page,field)
);`

func Open(path string, apiScopes map[string]bool) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = file.Chmod(0600); err != nil {
		file.Close()
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	dsn := url.URL{Scheme: "file", Path: abs}
	q := dsn.Query()
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Set("_txlock", "immediate")
	dsn.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	for scope := range apiScopes {
		s.catalog = append(s.catalog, scope)
	}
	s.catalog = append(s.catalog, "users:manage")
	slices.Sort(s.catalog)
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return fmt.Errorf("account database schema %d is newer than supported", version)
	}
	if _, err = tx.Exec(schema); err != nil {
		return err
	}
	view := []string{"nodes:read", "plugins:read", "backups:read", "players:read", "resources:read"}
	operator := append(slices.Clone(view), "nodes:control", "configs:read", "configs:write", "plugins:write", "backups:write", "inventory:read")
	for name, list := range map[string][]string{"admin": s.catalog, "viewer": view, "operator": operator} {
		slices.Sort(list)
		raw, _ := json.Marshal(list)
		if _, err = tx.Exec(`INSERT INTO roles VALUES(?,1,?) ON CONFLICT(name) DO UPDATE SET scopes=excluded.scopes WHERE roles.builtin=1`, name, string(raw)); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("PRAGMA user_version=1"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Bootstrapped(ctx context.Context) (bool, error) {
	var yes bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM bootstrap)").Scan(&yes)
	return yes, err
}

func username(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !namePattern.MatchString(name) {
		return "", ErrInvalid
	}
	return name, nil
}

func digest(password string, salt []byte) ([]byte, error) {
	select {
	case hashSlots <- struct{}{}:
		defer func() { <-hashSlots }()
	default:
		return nil, ErrFull
	}
	return argon2.IDKey([]byte(password), salt, 2, 32*1024, 1, 32), nil
}

func newPassword(password string) (salt, hash []byte, err error) {
	if utf8.RuneCountInString(password) < 12 || len(password) > 256 {
		return nil, nil, ErrInvalid
	}
	salt = make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return
	}
	hash, err = digest(password, salt)
	return
}

func issue(ctx context.Context, tx *sql.Tx, name string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(token))
	now := time.Now()
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires<=?", now.Unix()); err != nil {
		return "", err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO sessions VALUES(?,?,?)", h[:], name, now.Add(SessionLife).Unix())
	return token, err
}

func (s *Store) Bootstrap(ctx context.Context, name, password string) (string, error) {
	name, err := username(name)
	if err != nil {
		return "", err
	}
	salt, hash, err := newPassword(password)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "INSERT INTO bootstrap VALUES(1) ON CONFLICT DO NOTHING")
	if err != nil {
		return "", err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return "", ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO users VALUES(?,'admin',1,?,?)", name, salt, hash); err != nil {
		return "", err
	}
	token, err := issue(ctx, tx, name)
	if err != nil {
		return "", err
	}
	return token, tx.Commit()
}

func (s *Store) verify(ctx context.Context, name, password string) ([]byte, error) {
	if len(password) > 256 {
		return nil, ErrDenied
	}
	var salt, want []byte
	err := s.db.QueryRowContext(ctx, "SELECT salt,verifier FROM users WHERE username=? AND enabled=1", name).Scan(&salt, &want)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err != nil {
		salt = make([]byte, 16)
		want = make([]byte, 32)
	}
	got, err := digest(password, salt)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return nil, ErrDenied
	}
	return want, nil
}

func (s *Store) Login(ctx context.Context, name, password string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	verified, err := s.verify(ctx, name, password)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var ok bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE username=? AND verifier=? AND enabled=1)", name, verified).Scan(&ok); err != nil {
		return "", err
	}
	if !ok {
		return "", ErrDenied
	}
	token, err := issue(ctx, tx, name)
	if err != nil {
		return "", err
	}
	return token, tx.Commit()
}

func (s *Store) User(ctx context.Context, name string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT u.username,u.role,u.enabled,r.scopes FROM users u JOIN roles r ON u.role=r.name WHERE u.username=? AND u.enabled=1`, name))
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	var raw string
	err := row.Scan(&u.Name, &u.Role, &u.Enabled, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(raw), &u.Scopes); err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) Session(ctx context.Context, token string) (*User, error) {
	if len(token) != 43 {
		return nil, nil
	}
	h := sha256.Sum256([]byte(token))
	return scanUser(s.db.QueryRowContext(ctx, `SELECT u.username,u.role,u.enabled,r.scopes FROM sessions s
 JOIN users u ON u.username=s.username JOIN roles r ON r.name=u.role
 WHERE s.digest=? AND s.expires>? AND u.enabled=1`, h[:], time.Now().Unix()))
}

func (s *Store) Logout(ctx context.Context, token string) error {
	h := sha256.Sum256([]byte(token))
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE digest=?", h[:])
	return err
}
