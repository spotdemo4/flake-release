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

func TestLibraryBundleName(t *testing.T) {
	for _, test := range []struct {
		dependency string
		want       string
	}{
		{dependency: "libfoo.so.1", want: "libfoo.so.1"},
		{dependency: "/nix/store/package/lib/libfoo.so.1", want: "libfoo.so.1"},
		{dependency: "../../victim.so", want: "victim.so"},
	} {
		got, err := libraryBundleName(test.dependency)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("libraryBundleName(%q) = %q; want %q", test.dependency, got, test.want)
		}
	}

	if _, err := libraryBundleName(""); err == nil {
		t.Fatal("libraryBundleName returned no error for an empty dependency")
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

func TestDynamicRPath(t *testing.T) {
	root := filepath.Join("tmp", "bundle")
	for _, test := range []struct {
		name       string
		executable string
		want       string
	}{
		{name: "bin", executable: filepath.Join(root, "bin", "app"), want: "$ORIGIN/../lib"},
		{name: "root", executable: filepath.Join(root, "app"), want: "$ORIGIN/lib"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := dynamicRPath(root, test.executable)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("dynamicRPath() = %q; want %q", got, test.want)
			}
		})
	}
}

func TestCopyPathWritableKeepsCopiedDirectoriesWritable(t *testing.T) {
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
	if err := copyPathWritable(src, dst); err != nil {
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

func TestMaterializeDirectoryTreeLocalizesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "tool"), []byte("tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.Symlink("target", bin); err != nil {
		t.Fatal(err)
	}

	if err := materializeDirectoryTree(root, "bin"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("materialized bin mode = %v; want directory", info.Mode())
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("materialized bin permissions = %o; want 755", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(bin, "tool")); err != nil {
		t.Fatal(err)
	}
}

func TestMaterializeDirectoryTreeRejectsExternalSymlink(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(root, "bin")); err != nil {
		t.Fatal(err)
	}

	if err := materializeDirectoryTree(root, "bin"); err == nil {
		t.Fatal("materializeDirectoryTree returned no error for external symlink")
	}
}

func TestMaterializeDirectoryTreeRejectsAncestorSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(".", filepath.Join(root, "bin")); err != nil {
		t.Fatal(err)
	}

	if err := materializeDirectoryTree(root, "bin"); err == nil {
		t.Fatal("materializeDirectoryTree returned no error for ancestor symlink")
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
