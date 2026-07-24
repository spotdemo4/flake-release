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

func TestZipDirectoryPreservesSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target"), []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
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

	for _, file := range reader.File {
		if file.Name != "link" {
			continue
		}
		if file.Mode()&os.ModeSymlink == 0 {
			t.Fatal("zip link entry is not a symlink")
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
		if string(data) != "target" {
			t.Fatalf("zip symlink target = %q; want target", data)
		}
		return
	}
	t.Fatal("zip link entry not found")
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

func TestPreparePackageBundleMultipleOutputs(t *testing.T) {
	root := t.TempDir()
	outputs := []packageOutput{
		{Name: "out", Path: filepath.Join(root, "out")},
		{Name: "doc", Path: filepath.Join(root, "doc")},
		{Name: "dev", Path: filepath.Join(root, "dev")},
	}
	files := map[string]string{
		"out/share/app/data.txt":       "runtime",
		"dev/include/app/api.h":        "header",
		"doc/share/doc/app/readme.md": "documentation",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	bundle, err := preparePackageBundle(outputs, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer deletePath(bundle)

	want := map[string]string{
		"dev/include/app/api.h":        "header",
		"doc/share/doc/app/readme.md": "documentation",
		"out/share/app/data.txt":       "runtime",
	}
	for name, wantContent := range want {
		got, err := os.ReadFile(filepath.Join(bundle, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != wantContent {
			t.Fatalf("bundle entry %q = %q; want %q", name, got, wantContent)
		}
	}
}

func TestPreparePackageBundlePreservesSymlinks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(filepath.Join(root, "share", "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "share", "target", "file.txt"), []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"file-link":      "target/file.txt",
		"directory-link": "target",
		"broken-link":    "missing",
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, "share", name)); err != nil {
			t.Fatal(err)
		}
	}

	bundle, err := preparePackageBundle([]packageOutput{{Name: "out", Path: root}}, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer deletePath(bundle)

	for name, target := range links {
		path := filepath.Join(bundle, "share", name)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("bundle entry %q is not a symlink", path)
		}
		got, err := os.Readlink(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != target {
			t.Fatalf("symlink %q target = %q; want %q", path, got, target)
		}
	}
}

func TestPreparePackageBundleFlattensSingleExecutable(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	executablePath := filepath.Join(out, "bin", "tool")
	if err := os.MkdirAll(filepath.Dir(executablePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executablePath, []byte("tool"), 0o755); err != nil {
		t.Fatal(err)
	}

	bundle, err := preparePackageBundle([]packageOutput{{Name: "out", Path: out}}, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer deletePath(bundle)

	if _, err := os.Stat(filepath.Join(bundle, "tool")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bundle, "bin")); !os.IsNotExist(err) {
		t.Fatalf("bundle bin directory error = %v; want not found", err)
	}
}

func TestPreparePackageBundleRetainsBinWithOtherContent(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	executablePath := filepath.Join(out, "bin", "tool")
	if err := os.MkdirAll(filepath.Dir(executablePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executablePath, []byte("tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	dataPath := filepath.Join(out, "share", "data")
	if err := os.MkdirAll(filepath.Dir(dataPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := preparePackageBundle([]packageOutput{{Name: "out", Path: out}}, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer deletePath(bundle)

	for _, name := range []string{"bin/tool", "share/data"} {
		if _, err := os.Stat(filepath.Join(bundle, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPreparePackageBundleRetainsOutputRoots(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	executablePath := filepath.Join(out, "bin", "tool")
	if err := os.MkdirAll(filepath.Dir(executablePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executablePath, []byte("tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	dev := filepath.Join(root, "dev")
	if err := os.MkdirAll(dev, 0o755); err != nil {
		t.Fatal(err)
	}

	bundle, err := preparePackageBundle([]packageOutput{{Name: "out", Path: out}, {Name: "dev", Path: dev}}, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer deletePath(bundle)

	if _, err := os.Stat(filepath.Join(bundle, "out", "bin", "tool")); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveOutputsUsesZipForWindows(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(filepath.Join(out, "share"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "share", "data"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	archivePath, err := archiveOutputs([]packageOutput{{Name: "out", Path: out}}, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(archivePath) != ".zip" {
		t.Fatalf("archive extension = %q; want .zip", filepath.Ext(archivePath))
	}

	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if len(reader.File) != 2 || reader.File[1].Name != "share/data" {
		t.Fatalf("zip entries = %v; want share/data", reader.File)
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

func TestFindFilesDoesNotFollowDirectorySymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "file"), []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	files, err := findFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != link {
		t.Fatalf("findFiles() = %v; want [%s]", files, link)
	}
}

func TestRenameAssetPreservesTarXzExtension(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "archive.tar.xz")
	if err := os.WriteFile(archivePath, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}

	asset, err := renameAsset(archivePath, "app", "1.2.3", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	defer deletePath(filepath.Dir(asset))
	if got, want := filepath.Base(asset), "app_1.2.3_linux_amd64.tar.xz"; got != want {
		t.Fatalf("asset name = %q; want %q", got, want)
	}
}

func TestRenameAssetRejectsPathTraversal(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(archivePath, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := renameAsset(archivePath, "../app", "1.2.3", "linux", "amd64"); err == nil {
		t.Fatal("renameAsset returned no error for path traversal")
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
