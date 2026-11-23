package accounts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT username,role,enabled FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []User{}
	for rows.Next() {
		var u User
		if err = rows.Scan(&u.Name, &u.Role, &u.Enabled); err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, rows.Err()
}

func (s *Store) Create(ctx context.Context, name, password, role string) error {
	name, err := username(name)
	if err != nil {
		return err
	}
	if role == "admin" {
		return ErrDenied
	}
	salt, hash, err := newPassword(password)
	if err != nil {
		return err
	}
	r, err := s.db.ExecContext(ctx, `INSERT INTO users SELECT ?,name,1,?,? FROM roles WHERE name=?
 ON CONFLICT(username) DO NOTHING`, name, salt, hash, role)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return ErrConflict
	}
	return nil
}

type Change struct {
	Role     *string `json:"role"`
	Enabled  *bool   `json:"enabled"`
	Password *string `json:"password"`
}

func (s *Store) Change(ctx context.Context, name string, ch Change) error {
	if !namePattern.MatchString(name) || ch.Role == nil && ch.Enabled == nil && ch.Password == nil {
		return ErrInvalid
	}
	var salt, hash []byte
	var err error
	if ch.Password != nil {
		salt, hash, err = newPassword(*ch.Password)
		if err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var role string
	var enabled bool
	err = tx.QueryRowContext(ctx, "SELECT role,enabled FROM users WHERE username=?", name).Scan(&role, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	if role == "admin" {
		return ErrDenied
	}
	if ch.Role != nil {
		if *ch.Role == "admin" {
			return ErrDenied
		}
		var known bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM roles WHERE name=?)", *ch.Role).Scan(&known); err != nil {
			return err
		}
		if !known {
			return ErrInvalid
		}
		role = *ch.Role
	}
	if ch.Enabled != nil {
		enabled = *ch.Enabled
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET role=?,enabled=?,salt=coalesce(?,salt),verifier=coalesce(?,verifier) WHERE username=?`, role, enabled, salt, hash, name); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE username=?", name); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Password(ctx context.Context, name, old, next string) error {
	verified, err := s.verify(ctx, name, old)
	if err != nil {
		return err
	}
	salt, hash, err := newPassword(next)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "UPDATE users SET salt=?,verifier=? WHERE username=? AND verifier=? AND enabled=1", salt, hash, name, verified)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return ErrDenied
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE username=?", name); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Roles(ctx context.Context) ([]Role, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name,builtin,scopes FROM roles ORDER BY builtin DESC,name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Role{}
	for rows.Next() {
		var r Role
		var scopes string
		if err = rows.Scan(&r.Name, &r.Builtin, &scopes); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(scopes), &r.Scopes); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

func (s *Store) Assignable() []string {
	var list []string
	for _, scope := range s.catalog {
		if !strings.HasPrefix(scope, "admin:") && scope != "users:manage" {
			list = append(list, scope)
		}
	}
	return list
}

func (s *Store) SaveRole(ctx context.Context, name string, scopes []string, create bool) error {
	if !namePattern.MatchString(name) || len(scopes) == 0 || len(scopes) > len(s.catalog) {
		return ErrInvalid
	}
	slices.Sort(scopes)
	scopes = slices.Compact(scopes)
	allowed := s.Assignable()
	for _, scope := range scopes {
		if !slices.Contains(allowed, scope) {
			return ErrInvalid
		}
	}
	raw, _ := json.Marshal(scopes)
	var r sql.Result
	var err error
	if create {
		r, err = s.db.ExecContext(ctx, "INSERT INTO roles VALUES(?,0,?) ON CONFLICT DO NOTHING", name, string(raw))
	} else {
		r, err = s.db.ExecContext(ctx, "UPDATE roles SET scopes=? WHERE name=? AND builtin=0", string(raw), name)
	}
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) Preferences(ctx context.Context, user, page string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT field,value FROM preferences WHERE username=? AND page=?", user, page)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err = rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) UpdatePreferences(ctx context.Context, user, page string, values map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for field, value := range values {
		_, err = tx.ExecContext(ctx, `INSERT INTO preferences VALUES(?,?,?,?)
 ON CONFLICT(username,page,field) DO UPDATE SET value=excluded.value`, user, page, field, value)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
