package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupManifestSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "server.properties"), []byte("motd=test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "plugins"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugins", "Example.jar"), []byte("plugin"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "plugins", "outside.jar")); err != nil {
		t.Fatal(err)
	}
	manifest, err := buildBackupManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 2 {
		t.Fatalf("expected 2 regular files, got %d", len(manifest.Files))
	}
	if len(manifest.Skipped) != 1 {
		t.Fatalf("expected 1 skipped symlink, got %v", manifest.Skipped)
	}
}
