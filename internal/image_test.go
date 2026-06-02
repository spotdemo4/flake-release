package flakerelease

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDockerImageName(t *testing.T) {
	got := dockerImageName("GHCR.IO", "Owner/Repo", "v1.2.3")
	want := "ghcr.io/owner/repo:v1.2.3"
	if got != want {
		t.Fatalf("dockerImageName() = %q; want %q", got, want)
	}
}

func TestExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !executable(path) {
		t.Fatal("executable() = false; want true")
	}

	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	if executable(plain) {
		t.Fatal("executable() = true for non-executable file")
	}
}
