package flakerelease

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZipDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.txt"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "two.txt"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "archive.zip")
	if err := zipDirectory(root, out); err != nil {
		t.Fatal(err)
	}

	reader, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	got := map[string]string{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			got[file.Name] = ""
			continue
		}

		src, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(src)
		closeErr := src.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		got[file.Name] = string(data)
	}

	want := map[string]string{
		"one.txt":        "one",
		"nested/":        "",
		"nested/two.txt": "two",
	}
	for name, wantContent := range want {
		if got[name] != wantContent {
			t.Fatalf("zip entry %q = %q; want %q", name, got[name], wantContent)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("zip entry count = %d; want %d (%v)", len(got), len(want), got)
	}
}

func TestTarXzDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.txt"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "archive.tar.xz")
	if err := tarXzDirectory(root, out); err != nil {
		if strings.Contains(err.Error(), "requires cgo") {
			t.Skip(err)
		}
		t.Fatal(err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	xzMagic := []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}
	if len(data) < len(xzMagic) {
		t.Fatalf("archive length = %d; want at least %d", len(data), len(xzMagic))
	}
	if !bytes.HasPrefix(data, xzMagic) {
		t.Fatalf("archive prefix = %x; want xz magic %x", data[:len(xzMagic)], xzMagic)
	}
}

func TestCopyPath(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "one.txt"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "dst")
	if err := copyPath(src, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "nested", "one.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "one" {
		t.Fatalf("copied file = %q; want %q", got, "one")
	}
}

func TestIsStaticRejectsTextExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if isStatic(path) {
		t.Fatal("isStatic returned true for text executable")
	}
}
