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
	if err := os.WriteFile(target, []byte("target"), 0o400); err != nil {
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

func TestCopyPathDereferenceKeepsCopiedDirectoriesWritable(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	bin := filepath.Join(src, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "app"), []byte("app"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bin, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(bin, 0o755)
		_ = os.Chmod(src, 0o755)
	})

	dst := filepath.Join(t.TempDir(), "dst")
	if err := copyPathDereference(src, dst); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dst, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("copied directory mode = %o; want 755", info.Mode().Perm())
	}
}

func TestMakeWritable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("file"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := makeWritable(path); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o; want 755", info.Mode().Perm())
	}
}
