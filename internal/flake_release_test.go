package flakerelease

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitPackages(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "spaces", input: "a b  c", want: []string{"a", "b", "c"}},
		{name: "newlines", input: "a\nb\n\nc\n", want: []string{"a", "b", "c"}},
		{name: "empty", input: "", want: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := splitPackages(test.input)
			if len(got) != len(test.want) {
				t.Fatalf("splitPackages(%q) length = %d; want %d (%v)", test.input, len(got), len(test.want), got)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("splitPackages(%q)[%d] = %q; want %q", test.input, i, got[i], test.want[i])
				}
			}
		})
	}
}

func TestTruthy(t *testing.T) {
	for _, value := range []string{"true", "TRUE", "1", "yes", "on"} {
		if !truthy(value) {
			t.Fatalf("truthy(%q) = false; want true", value)
		}
	}
	for _, value := range []string{"", "false", "0", "no", "off"} {
		if truthy(value) {
			t.Fatalf("truthy(%q) = true; want false", value)
		}
	}
}

func TestReleaseType(t *testing.T) {
	t.Setenv("GIT_TYPE", "")
	t.Setenv("FORGEJO_ACTIONS", "")
	t.Setenv("GITEA_ACTIONS", "")
	t.Setenv("GITHUB_ACTIONS", "")

	tests := []struct {
		origin string
		want   releaseProvider
	}{
		{origin: "git@github.com:spotdemo4/flake-release.git", want: releaseGitHub},
		{origin: "https://git.example/gitea/project", want: releaseGitea},
		{origin: "https://git.example/forgejo/project", want: releaseForgejo},
	}

	for _, test := range tests {
		got, err := releaseType(test.origin)
		if err != nil {
			t.Fatalf("releaseType(%q) returned error: %v", test.origin, err)
		}
		if got != test.want {
			t.Fatalf("releaseType(%q) = %q; want %q", test.origin, got, test.want)
		}
	}
}

func TestReleaseTypeEnvOverride(t *testing.T) {
	t.Setenv("GIT_TYPE", "forgejo")
	t.Setenv("FORGEJO_ACTIONS", "")
	t.Setenv("GITEA_ACTIONS", "")
	t.Setenv("GITHUB_ACTIONS", "")

	got, err := releaseType("git@github.com:spotdemo4/flake-release.git")
	if err != nil {
		t.Fatalf("releaseType returned error: %v", err)
	}
	if got != releaseForgejo {
		t.Fatalf("releaseType with override = %q; want %q", got, releaseForgejo)
	}
}

func TestSortChangelog(t *testing.T) {
	input := "* chore: one (1)\n* fix: two (2)\n* feat(ui): three (3)\n"
	want := "* feat(ui): three (3)\n* fix: two (2)\n* chore: one (1)\n"

	if got := sortChangelog(input); got != want {
		t.Fatalf("sortChangelog() = %q; want %q", got, want)
	}
}

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

func TestHTTPRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token test-token" {
			http.Error(w, "bad authorization", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Accept") != "application/json" {
			http.Error(w, "bad accept", http.StatusBadRequest)
			return
		}

		switch r.URL.Path {
		case "/fail":
			http.Error(w, "nope", http.StatusTeapot)
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer server.Close()

	body, err := httpRequest(http.MethodGet, "test-token", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q; want JSON", body)
	}

	body, err = httpRequest(http.MethodDelete, "test-token", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		t.Fatalf("DELETE body = %q; want nil", body)
	}

	if _, err := httpRequest(http.MethodGet, "test-token", server.URL+"/fail"); err == nil {
		t.Fatal("httpRequest returned nil error for non-2xx response")
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

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
