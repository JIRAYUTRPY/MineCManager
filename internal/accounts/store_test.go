package accounts

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConcurrentBootstrapSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel accounts.sqlite3")
	catalog := map[string]bool{"nodes:read": true, "admin:keys": true}
	first, err := Open(path, catalog)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(path, catalog)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	type result struct {
		token string
		err   error
	}
	results := make(chan result, 2)
	for _, db := range []*Store{first, second} {
		go func() {
			<-start
			token, err := db.Bootstrap(t.Context(), "owner", "an example passphrase")
			results <- result{token, err}
		}()
	}
	close(start)
	var token string
	var conflicts int
	for range 2 {
		r := <-results
		switch {
		case r.err == nil:
			token = r.token
		case errors.Is(r.err, ErrConflict):
			conflicts++
		default:
			t.Fatal(r.err)
		}
	}
	if conflicts != 1 || token == "" {
		t.Fatal("bootstrap must elect one administrator")
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	if err = second.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path, catalog)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("account database permissions", err)
	}
	ctx := t.Context()
	if done, err := db.Bootstrapped(ctx); err != nil || !done {
		t.Fatal("missing bootstrap marker", err)
	}
	user, err := db.Session(ctx, token)
	if err != nil || user == nil || !user.Has("users:manage") {
		t.Fatal("lost owner session", err)
	}
	if _, err = db.Bootstrap(ctx, "another", "a different passphrase"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = db.Create(ctx, "another", "a different passphrase", "admin"); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	list, err := db.Users(ctx)
	if err != nil || len(list) != 1 || list[0].Role != "admin" {
		t.Fatalf("owners: %+v %v", list, err)
	}
}
