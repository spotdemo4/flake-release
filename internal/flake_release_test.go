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

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
