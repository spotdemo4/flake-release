package flakerelease

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxInterpreter(t *testing.T) {
	got, err := linuxInterpreter("amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/lib64/ld-linux-x86-64.so.2" {
		t.Fatalf("linuxInterpreter(amd64) = %q; want x86_64 loader", got)
	}

	if _, err := linuxInterpreter("wasm"); err == nil {
		t.Fatal("linuxInterpreter returned nil error for unsupported architecture")
	}
}

func TestIsGlibcLibrary(t *testing.T) {
	if !isGlibcLibrary("libc.so.6") {
		t.Fatal("libc.so.6 should be treated as glibc")
	}
	if isGlibcLibrary("libstdc++.so.6") {
		t.Fatal("libstdc++.so.6 should not be treated as glibc")
	}
}

func TestExpandELFOrigin(t *testing.T) {
	binary := filepath.Join("tmp", "bundle", "bin", "app")
	got := expandELFOrigin("$ORIGIN/../lib", binary)
	want := filepath.Clean(filepath.Join("tmp", "bundle", "bin", "../lib"))
	if got != want {
		t.Fatalf("expandELFOrigin() = %q; want %q", got, want)
	}
}

func TestCopyPathDereference(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "copied")
	if err := copyPathDereference(link, dst); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("copyPathDereference preserved a symlink")
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "target" {
		t.Fatalf("copied content = %q; want target", data)
	}
}
