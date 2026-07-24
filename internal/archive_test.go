package flakerelease

import (
	"archive/tar"
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

func TestWriteTarOutputs(t *testing.T) {
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

	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	if err := writeTarOutputs(writer, outputs); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	reader := tar.NewReader(bytes.NewReader(buffer.Bytes()))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.FileInfo().IsDir() {
			got[header.Name] = ""
			continue
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		got[header.Name] = string(data)
	}

	want := map[string]string{
		"dev/":                         "",
		"dev/include/":                 "",
		"dev/include/app/":             "",
		"dev/include/app/api.h":        "header",
		"doc/":                         "",
		"doc/share/":                   "",
		"doc/share/doc/":               "",
		"doc/share/doc/app/":           "",
		"doc/share/doc/app/readme.md": "documentation",
		"out/":                         "",
		"out/share/":                   "",
		"out/share/app/":               "",
		"out/share/app/data.txt":       "runtime",
	}
	for name, wantContent := range want {
		if got[name] != wantContent {
			t.Fatalf("tar entry %q = %q; want %q", name, got[name], wantContent)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("tar entry count = %d; want %d (%v)", len(got), len(want), got)
	}
}

func TestWriteTarOutputsPreservesSymlinks(t *testing.T) {
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

	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	if err := writeTarOutputs(writer, []packageOutput{{Name: "out", Path: root}}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	reader := tar.NewReader(bytes.NewReader(buffer.Bytes()))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeSymlink {
			got[header.Name] = header.Linkname
		}
	}

	for name, target := range links {
		archiveName := "out/share/" + name
		if got[archiveName] != target {
			t.Fatalf("symlink %q target = %q; want %q", archiveName, got[archiveName], target)
		}
	}
	if len(got) != len(links) {
		t.Fatalf("symlink count = %d; want %d (%v)", len(got), len(links), got)
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

func TestAllStaticRejectsEmptyBinDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	if allStatic(root) {
		t.Fatal("allStatic returned true for an empty bin directory")
	}
}

func TestOutputsHaveExecutables(t *testing.T) {
	root := t.TempDir()
	outsideBin := filepath.Join(root, "out", "lib", "helper")
	if err := os.MkdirAll(filepath.Dir(outsideBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideBin, []byte("helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "dev", "bin", "tool")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("tool"), 0o644); err != nil {
		t.Fatal(err)
	}

	outputs := []packageOutput{
		{Name: "out", Path: filepath.Join(root, "out")},
		{Name: "dev", Path: filepath.Join(root, "dev")},
	}
	hasExecutables, err := outputsHaveExecutables(outputs)
	if err != nil {
		t.Fatal(err)
	}
	if hasExecutables {
		t.Fatal("outputsHaveExecutables found an executable outside bin")
	}

	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	hasExecutables, err = outputsHaveExecutables(outputs)
	if err != nil {
		t.Fatal(err)
	}
	if !hasExecutables {
		t.Fatal("outputsHaveExecutables did not find executable in bin")
	}
}
