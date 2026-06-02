package flakerelease

import (
	"os"
	"testing"

	git "github.com/go-git/go-git/v6"
)

func TestSortChangelog(t *testing.T) {
	input := "* chore: one (1)\n* fix: two (2)\n* feat(ui): three (3)\n"
	want := "* feat(ui): three (3)\n* fix: two (2)\n* chore: one (1)\n"

	if got := sortChangelog(input); got != want {
		t.Fatalf("sortChangelog() = %q; want %q", got, want)
	}
}

func TestSplitLines(t *testing.T) {
	got := splitLines("one\ntwo\n")
	want := []string{"one", "two"}
	if len(got) != len(want) {
		t.Fatalf("splitLines() length = %d; want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("splitLines()[%d] = %q; want %q", i, got[i], want[i])
		}
	}

	if got := splitLines(""); got != nil {
		t.Fatalf("splitLines(empty) = %v; want nil", got)
	}
}

func TestGitUserUsesScopedGitConfigPrecedence(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	chdir(t, dir)

	systemConfig := writeGitConfig(t, "system-user")
	globalConfig := writeGitConfig(t, "global-user")

	t.Setenv("GIT_CONFIG_SYSTEM", systemConfig)
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "")

	if got, err := gitUser(); err != nil {
		t.Fatal(err)
	} else if got != "system-user" {
		t.Fatalf("gitUser() with system config = %q; want system-user", got)
	}

	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	if got, err := gitUser(); err != nil {
		t.Fatal(err)
	} else if got != "global-user" {
		t.Fatalf("gitUser() with global config = %q; want global-user", got)
	}

	cfg, err := repo.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.User.Name = "local-user"
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if got, err := gitUser(); err != nil {
		t.Fatal(err)
	} else if got != "local-user" {
		t.Fatalf("gitUser() with local config = %q; want local-user", got)
	}
}

func TestGitChangelogForCurrentRepositoryTags(t *testing.T) {
	repo, err := openGitRepository()
	if err != nil {
		t.Skip("current directory is not a git repository")
	}
	defer closeGitRepository(repo)

	tags, err := tagNames(repo)
	if err != nil {
		t.Fatal(err)
	}
	hasTag := false
	for _, tag := range tags {
		if tag == "v0.17.0" {
			hasTag = true
			break
		}
	}
	if !hasTag {
		t.Skip("v0.17.0 tag is not available")
	}

	changelog, err := gitChangelog("v0.17.0")
	if err != nil {
		t.Fatal(err)
	}
	defer deletePath(changelog)
}

func writeGitConfig(t *testing.T, userName string) string {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "gitconfig-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("[user]\n\tname = " + userName + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return file.Name()
}

func chdir(t *testing.T, dir string) {
	t.Helper()

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatal(err)
		}
	})
}
