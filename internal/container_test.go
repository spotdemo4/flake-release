package flakerelease

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v6"
	gitconfig "github.com/go-git/go-git/v6/plumbing/format/config"
)

func containerTestRepository(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "repository with spaces")
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	closeGitRepository(repo)
	chdir(t, dir)
	t.Setenv("HOME", home)
	t.Setenv("PATH", home)
	t.Setenv("USER", "")
	t.Setenv("DOCKER", "true")
	t.Setenv("CI", "true")
	t.Setenv("TMPDIR", t.TempDir())
	return home, dir
}

func TestSetupContainerEnvironmentGating(t *testing.T) {
	for _, test := range []struct {
		name   string
		docker string
		ci     string
	}{
		{name: "ordinary CLI"},
		{name: "host CI", ci: "true"},
		{name: "non-CI container", docker: "true"},
		{name: "false Docker flag", docker: "false", ci: "true"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			missing := filepath.Join(home, "missing")
			chdir(t, home)
			t.Setenv("HOME", home)
			t.Setenv("DOCKER", test.docker)
			t.Setenv("CI", test.ci)
			t.Setenv("TMPDIR", missing)
			if err := setupContainerEnvironment(); err != nil {
				t.Fatal(err)
			}
			if got := os.Getenv("TMPDIR"); got != missing {
				t.Fatalf("TMPDIR = %q; want unchanged %q", got, missing)
			}
			if _, err := os.Stat(filepath.Join(home, ".gitconfig")); !os.IsNotExist(err) {
				t.Fatalf("global Git config unexpectedly created: %v", err)
			}
		})
	}
}

func TestSetupContainerTempDir(t *testing.T) {
	for _, name := range []string{"valid", "unset", "missing", "file", "unwritable"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			candidate := dir
			want := "/tmp"
			switch name {
			case "valid":
				want = dir
			case "unset":
				want = ""
			case "missing":
				candidate = filepath.Join(dir, "missing")
			case "file":
				candidate = filepath.Join(dir, "file")
				if err := os.WriteFile(candidate, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "unwritable":
				if os.Geteuid() == 0 {
					t.Skip("root bypasses directory write permissions")
				}
				if err := os.Chmod(dir, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
			}
			t.Setenv("TMPDIR", candidate)
			if name == "unset" {
				if err := os.Unsetenv("TMPDIR"); err != nil {
					t.Fatal(err)
				}
			}
			if err := setupContainerTempDir(); err != nil {
				t.Fatal(err)
			}
			if got := os.Getenv("TMPDIR"); got != want {
				t.Fatalf("TMPDIR = %q; want %q", got, want)
			}
			if err := checkTempDir(os.TempDir()); err != nil {
				t.Fatalf("temporary directory remains unusable: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "flake-release-probe-") {
					t.Fatalf("probe was not removed: %s", entry.Name())
				}
			}
			if name == "missing" {
				if _, err := os.Stat(candidate); !os.IsNotExist(err) {
					t.Fatalf("host-only directory unexpectedly created: %v", err)
				}
			}
		})
	}
}

func TestCheckTempDirFailure(t *testing.T) {
	if err := checkTempDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("checkTempDir accepted a missing directory")
	}
}

func TestSetupContainerGitTrust(t *testing.T) {
	home, dir := containerTestRepository(t)
	configPath := filepath.Join(home, ".gitconfig")
	original := "# Keep this comment\n[user]\n\tname = Container User\n[safe]\n\tdirectory = /other/workspace"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "cli-only-config"))
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	chdir(t, filepath.Join(dir, "nested"))
	for range 2 {
		if err := setupContainerEnvironment(); err != nil {
			t.Fatal(err)
		}
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(contents), original) {
		t.Fatalf("existing global config changed: %s", contents)
	}
	values := readContainerGitConfig(t, configPath).Section("safe").OptionAll("directory")
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/other/workspace", root}; !slices.Equal(values, want) {
		t.Fatalf("safe.directory = %q; want %q", values, want)
	}
	if _, err := os.Stat(filepath.Join(home, "cli-only-config")); !os.IsNotExist(err) {
		t.Fatalf("wrote Git CLI override instead of libgit2 config: %v", err)
	}
}

func TestSetupContainerGitTrustAfterReset(t *testing.T) {
	home, dir := containerTestRepository(t)
	configPath := filepath.Join(home, ".gitconfig")
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	config := gitconfig.New().AddOption("safe", "", "directory", root).AddOption("safe", "", "directory", "")
	var contents bytes.Buffer
	if err := gitconfig.NewEncoder(&contents).Encode(config); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, contents.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setupContainerEnvironment(); err != nil {
		t.Fatal(err)
	}
	values := readContainerGitConfig(t, configPath).Section("safe").OptionAll("directory")
	if want := []string{root, "", root}; !slices.Equal(values, want) {
		t.Fatalf("safe.directory = %q; want %q", values, want)
	}
}

func TestSetupContainerEnvironmentChangelog(t *testing.T) {
	_, dir := containerTestRepository(t)
	repo, err := openGitRepository()
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)
	hash := commitGitTestFile(t, repo, dir, "initial commit")
	createGitTestTag(t, repo, "v1.0.0", hash)
	t.Setenv("TMPDIR", filepath.Join(dir, "unmounted-host-temp"))
	if err := setupContainerEnvironment(); err != nil {
		t.Fatal(err)
	}
	tag, err := parseSelectedReleaseTag("v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	path, err := gitChangelog(tag)
	if err != nil {
		t.Fatal(err)
	}
	defer deletePath(path)
	if _, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	}
}

func readContainerGitConfig(t *testing.T, path string) *gitconfig.Config {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config := gitconfig.New()
	if err := gitconfig.NewDecoder(bytes.NewReader(contents)).Decode(config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestAddGitSafeDirectoryEscaping(t *testing.T) {
	for _, root := range []string{
		"/workspace/simple",
		"/workspace/with spaces ",
		"/workspace/with#hash;semicolon",
		"/workspace/with\"quote\\backslash",
		"/workspace/with\ttab\nnewline",
	} {
		t.Run(root, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), ".gitconfig")
			if err := addGitSafeDirectory(configPath, root); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := addGitSafeDirectory(configPath, root); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("repeated setup changed config: %q -> %q", before, after)
			}
			values := readContainerGitConfig(t, configPath).Section("safe").OptionAll("directory")
			if !slices.Equal(values, []string{root}) {
				t.Fatalf("safe.directory = %q; want %q", values, root)
			}
			if _, err := os.Stat(configPath + ".lock"); !os.IsNotExist(err) {
				t.Fatalf("lock was not removed: %v", err)
			}
			info, err := os.Stat(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("new config permissions = %o; want 600", info.Mode().Perm())
			}
		})
	}
}

func TestAddGitSafeDirectorySymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config")
	configPath := filepath.Join(dir, ".gitconfig")
	if err := os.WriteFile(target, []byte("# Existing config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("config", configPath); err != nil {
		t.Fatal(err)
	}
	if err := addGitSafeDirectory(configPath, "/workspace"); err != nil {
		t.Fatal(err)
	}
	if link, err := os.Readlink(configPath); err != nil || link != "config" {
		t.Fatalf("config symlink = %q, %v; want unchanged link", link, err)
	}
	if value := readContainerGitConfig(t, target).Section("safe").Option("directory"); value != "/workspace" {
		t.Fatalf("symlink target safe.directory = %q; want /workspace", value)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("existing config permissions = %o; want 640", info.Mode().Perm())
	}
}

func TestSetupContainerEnvironmentConfigFailure(t *testing.T) {
	for _, name := range []string{"invalid config", "locked config", "config directory"} {
		t.Run(name, func(t *testing.T) {
			home, _ := containerTestRepository(t)
			switch name {
			case "invalid config":
				if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "locked config":
				if err := os.WriteFile(filepath.Join(home, ".gitconfig.lock"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "config directory":
				if err := os.Mkdir(filepath.Join(home, ".gitconfig"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := setupContainerEnvironment(); err == nil || !strings.Contains(err.Error(), "trusting container workspace") {
				t.Fatalf("setupContainerEnvironment() error = %v; want contextual failure", err)
			}
			_, lockErr := os.Stat(filepath.Join(home, ".gitconfig.lock"))
			if name == "locked config" {
				if lockErr != nil {
					t.Fatalf("existing lock was removed: %v", lockErr)
				}
			} else if !os.IsNotExist(lockErr) {
				t.Fatalf("failed update left a lock: %v", lockErr)
			}
			if name == "invalid config" {
				contents, err := os.ReadFile(filepath.Join(home, ".gitconfig"))
				if err != nil || string(contents) != "[invalid" {
					t.Fatalf("invalid config was modified: %q, %v", contents, err)
				}
			}
		})
	}
}
