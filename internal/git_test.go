package flakerelease

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
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

func TestGitRepositoryFromOrigin(t *testing.T) {
	tests := []struct {
		origin string
		want   string
	}{
		{origin: "git@trev.zip:llc/flake-release.git", want: "llc/flake-release"},
		{origin: "https://trev.zip/llc/flake-release.git", want: "llc/flake-release"},
		{origin: "ssh://git@git.example/owner/project.git", want: "owner/project"},
		{origin: "https://git.example/scm/owner/project.git/", want: "owner/project"},
		{origin: "https://git.example/owner", want: ""},
		{origin: "file:///home/owner/project.git", want: ""},
		{origin: "/home/owner/project.git", want: ""},
	}

	for _, test := range tests {
		if got := gitRepositoryFromOrigin(test.origin); got != test.want {
			t.Fatalf("gitRepositoryFromOrigin(%q) = %q; want %q", test.origin, got, test.want)
		}
	}
}

func TestGitServerURLFromOrigin(t *testing.T) {
	tests := []struct {
		origin string
		want   string
	}{
		{origin: "git@trev.zip:llc/flake-release.git", want: "https://trev.zip"},
		{origin: "https://trev.zip/llc/flake-release.git", want: "https://trev.zip"},
		{origin: "http://git.example:3000/owner/project.git", want: "http://git.example:3000"},
		{origin: "ssh://git@git.example:2222/owner/project.git", want: "https://git.example"},
		{origin: "file:///home/owner/project.git", want: ""},
		{origin: "/home/owner/project.git", want: ""},
	}

	for _, test := range tests {
		if got := gitServerURLFromOrigin(test.origin); got != test.want {
			t.Fatalf("gitServerURLFromOrigin(%q) = %q; want %q", test.origin, got, test.want)
		}
	}
}

func TestGitLatestTagRejectsAmbiguousTaggedCommit(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	commit := commitGitTestFile(t, repo, dir, "ambiguous")
	createGitTestTag(t, repo, "v1.0.0", commit)
	createGitTestTag(t, repo, "packages/api/v1.0.0", commit)
	chdir(t, dir)

	if _, err := gitLatestTag(); err == nil || !strings.Contains(err.Error(), "set TAG explicitly") {
		t.Fatalf("gitLatestTag() error = %v; want actionable ambiguity error", err)
	}
}

func TestPreviousTagOrdersReleaseCandidateBeforeStableVersion(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	releaseCandidate := commitGitTestFile(t, repo, dir, "release candidate")
	createGitTestTag(t, repo, "v1.0.0-rc.1", releaseCandidate)
	stable := commitGitTestFile(t, repo, dir, "stable")
	createGitTestTag(t, repo, "v1.0.0", stable)

	got, err := previousTag(repo, parseReleaseTag("v1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "v1.0.0-rc.1" {
		t.Fatalf("previousTag(v1.0.0) = %q; want v1.0.0-rc.1", got)
	}
}

func TestVersionLessPreservesNumericOrdering(t *testing.T) {
	if !versionLess("v1.9.0", "v1.10.0") {
		t.Fatal("v1.9.0 did not sort before v1.10.0")
	}
	if !versionLess("v1.0.0-rc.1", "v1.0.0") {
		t.Fatal("release candidate did not sort before stable version")
	}
	if versionLess("v1.0.0", "v1.0.0-rc.1") {
		t.Fatal("stable version sorted before its release candidate")
	}
	if got := compareVersionTags("v1.0.0+linux", "v1.0.0+darwin"); got != 0 {
		t.Fatalf("build metadata comparison = %d; want equal precedence", got)
	}
	if got := compareVersionTags("v1.0.0-alpha.1", "v1.0.0-alpha-1"); got >= 0 {
		t.Fatalf("prerelease comparison = %d; want alpha.1 before alpha-1", got)
	}
}

func TestPreviousTagUsesExactReleaseNamespace(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	first := commitGitTestFile(t, repo, dir, "first")
	for _, tag := range []string{"v1.0.0", "packages/api/v1.0.0", "packages/web/v9.0.0", "packages/api/client/v9.0.0"} {
		createGitTestTag(t, repo, tag, first)
	}
	second := commitGitTestFile(t, repo, dir, "second")
	for _, tag := range []string{"v1.1.0", "packages/api/v1.1.0", "packages/web/v10.0.0", "packages/api/client/v10.0.0"} {
		createGitTestTag(t, repo, tag, second)
	}

	tests := []struct {
		tag  string
		want string
	}{
		{tag: "v1.1.0", want: "v1.0.0"},
		{tag: "packages/api/v1.1.0", want: "packages/api/v1.0.0"},
		{tag: "packages/web/v10.0.0", want: "packages/web/v9.0.0"},
		{tag: "packages/api/client/v10.0.0", want: "packages/api/client/v9.0.0"},
	}
	for _, test := range tests {
		got, err := previousTag(repo, parseReleaseTag(test.tag))
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("previousTag(%q) = %q; want %q", test.tag, got, test.want)
		}
	}
}

func TestGitChangelogFiltersScopedCommits(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	commitGitTestFile(t, repo, dir, "root")
	baseline := commitGitTestPaths(t, repo, dir, "api baseline", "packages/api/api.go")
	createGitTestTag(t, repo, "v1.0.0", baseline)
	createGitTestTag(t, repo, "packages/api/v1.0.0", baseline)

	commitGitTestPaths(t, repo, dir, "api direct", "packages/api/api.go")
	commitGitTestPaths(t, repo, dir, "api nested", "packages/api/internal/helper.go")
	commitGitTestPaths(t, repo, dir, "web only", "packages/web/web.go")
	commitGitTestPaths(t, repo, dir, "root docs", "README.md")
	commitGitTestPaths(t, repo, dir, "similar prefix", "packages/api-client/client.go")
	commitGitTestPaths(t, repo, dir, "case mismatch", "packages/API/api.go")
	current := commitGitTestPaths(t, repo, dir, "mixed paths", "packages/api/api.go", "packages/web/web.go")
	createGitTestTag(t, repo, "v1.1.0", current)
	createGitTestTag(t, repo, "packages/api/v1.1.0", current)

	chdir(t, dir)
	scoped := readGitTestChangelog(t, "packages/api/v1.1.0")
	for _, subject := range []string{"api direct", "api nested", "mixed paths"} {
		if count := strings.Count(scoped, subject); count != 1 {
			t.Fatalf("scoped changelog contains %q %d times; want once: %q", subject, count, scoped)
		}
	}
	for _, subject := range []string{"api baseline", "web only", "root docs", "similar prefix", "case mismatch"} {
		if strings.Contains(scoped, subject) {
			t.Fatalf("scoped changelog contains unrelated commit %q: %q", subject, scoped)
		}
	}

	unscoped := readGitTestChangelog(t, "v1.1.0")
	for _, subject := range []string{"api direct", "api nested", "web only", "root docs", "similar prefix", "case mismatch", "mixed paths"} {
		if !strings.Contains(unscoped, subject) {
			t.Fatalf("unscoped changelog does not contain %q: %q", subject, unscoped)
		}
	}
}

func TestCommitChangesPathTracksTreeEntryChanges(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	tests := []struct {
		hash plumbing.Hash
		want bool
	}{
		{hash: commitGitTestFile(t, repo, dir, "root outside scope"), want: false},
		{hash: commitGitTestPaths(t, repo, dir, "add scope", "packages/api/api.go"), want: true},
		{hash: commitGitTestPaths(t, repo, dir, "unrelated change", "packages/web/web.go"), want: false},
		{hash: moveGitTestPath(t, repo, dir, "move out", "packages/api/api.go", "packages/web/api.go"), want: true},
		{hash: moveGitTestPath(t, repo, dir, "move in", "packages/web/api.go", "packages/api/api.go"), want: true},
		{hash: moveGitTestPath(t, repo, dir, "move within", "packages/api/api.go", "packages/api/internal/api.go"), want: true},
		{hash: removeGitTestPaths(t, repo, "remove scope", "packages/api/internal/api.go"), want: true},
	}
	for _, test := range tests {
		commit, err := repo.CommitObject(test.hash)
		if err != nil {
			t.Fatal(err)
		}
		got, err := commitChangesPath(commit, "packages/api")
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("commitChangesPath(%q) = %t; want %t", commit.Message, got, test.want)
		}
	}

	rootDir := t.TempDir()
	rootRepo, err := git.PlainInit(rootDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(rootRepo)
	rootHash := commitGitTestPaths(t, rootRepo, rootDir, "root inside scope", "packages/api/api.go")
	rootCommit, err := rootRepo.CommitObject(rootHash)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := commitChangesPath(rootCommit, "packages/api")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("commitChangesPath(root) = false; want true for a root containing the scope")
	}
}

func TestGitChangelogFirstScopedReleaseExcludesRoot(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	commitGitTestPaths(t, repo, dir, "root scoped", "packages/api/root.go")
	commitGitTestPaths(t, repo, dir, "unrelated", "packages/web/web.go")
	current := commitGitTestPaths(t, repo, dir, "later scoped", "packages/api/api.go")
	createGitTestTag(t, repo, "packages/api/v1.0.0", current)

	chdir(t, dir)
	got := readGitTestChangelog(t, "packages/api/v1.0.0")
	if !strings.Contains(got, "later scoped") || strings.Contains(got, "root scoped") || strings.Contains(got, "unrelated") {
		t.Fatalf("first scoped changelog = %q; want only later scoped changes after the root boundary", got)
	}
}

func TestCommitChangesPathUsesFirstMergeParent(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	root := commitGitTestFile(t, repo, dir, "root")
	parentZero := commitGitTestPaths(t, repo, dir, "parent zero", "packages/api/api.go")
	stageGitTestPaths(t, repo, dir, "parent one", "packages/api/api.go")
	parentOne := commitGitTestIndex(t, repo, "parent one", []plumbing.Hash{root}, false)

	changedMerge := commitGitTestIndex(t, repo, "changed merge", []plumbing.Hash{parentZero, parentOne}, false)
	stageGitTestPaths(t, repo, dir, "parent zero", "packages/api/api.go")
	unchangedMerge := commitGitTestIndex(t, repo, "unchanged merge", []plumbing.Hash{parentZero, parentOne}, true)

	for _, test := range []struct {
		hash plumbing.Hash
		want bool
	}{
		{hash: changedMerge, want: true},
		{hash: unchangedMerge, want: false},
	} {
		commit, err := repo.CommitObject(test.hash)
		if err != nil {
			t.Fatal(err)
		}
		got, err := commitChangesPath(commit, "packages/api")
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("commitChangesPath(%q) = %t; want %t", commit.Message, got, test.want)
		}
	}
}

func TestPreviousTagIgnoresNonAncestorVersions(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	previous := commitGitTestFile(t, repo, dir, "previous")
	createGitTestTag(t, repo, "packages/api/v1.0.0", previous)
	current := commitGitTestFile(t, repo, dir, "current")
	createGitTestTag(t, repo, "packages/api/v1.2.0", current)
	nonAncestor := commitGitTestFile(t, repo, dir, "future")
	createGitTestTag(t, repo, "packages/api/v1.1.0", nonAncestor)

	got, err := previousTag(repo, parseReleaseTag("packages/api/v1.2.0"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "packages/api/v1.0.0" {
		t.Fatalf("previousTag() = %q; want ancestor release", got)
	}
}

func TestPreviousTagUsesRepositoryRootForFirstNamespaceRelease(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	root := commitGitTestFile(t, repo, dir, "root")
	current := commitGitTestFile(t, repo, dir, "current")
	createGitTestTag(t, repo, "packages/api/v1.0.0", current)

	got, err := previousTag(repo, parseReleaseTag("packages/api/v1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if got != root.String() {
		t.Fatalf("previousTag() = %q; want root commit %s", got, root)
	}
}

func TestPreviousTagIgnoresMalformedReleaseTags(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	previous := commitGitTestFile(t, repo, dir, "previous")
	createGitTestTag(t, repo, "packages/api/v0.9.0", previous)
	malformed := commitGitTestFile(t, repo, dir, "malformed")
	createGitTestTag(t, repo, "packages/api/v", malformed)
	current := commitGitTestFile(t, repo, dir, "current")
	createGitTestTag(t, repo, "packages/api/v1.0.0", current)

	got, err := previousTag(repo, parseReleaseTag("packages/api/v1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "packages/api/v0.9.0" {
		t.Fatalf("previousTag() = %q; want valid predecessor", got)
	}
}

func TestPreviousTagAcceptsEquivalentAliasesOnSameCommit(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	root := commitGitTestFile(t, repo, dir, "root")
	current := commitGitTestFile(t, repo, dir, "current")
	createGitTestTag(t, repo, "packages/api/1.0.0", current)
	createGitTestTag(t, repo, "packages/api/v1.0.0", current)

	got, err := previousTag(repo, parseReleaseTag("packages/api/v1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if got != root.String() {
		t.Fatalf("previousTag() = %q; want root commit %s", got, root)
	}
}

func TestPreviousTagRejectsEquivalentAliases(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	alias := commitGitTestFile(t, repo, dir, "alias")
	createGitTestTag(t, repo, "packages/api/1.0.0", alias)
	current := commitGitTestFile(t, repo, dir, "current")
	createGitTestTag(t, repo, "packages/api/v1.0.0", current)

	_, err = previousTag(repo, parseReleaseTag("packages/api/v1.0.0"))
	if err == nil || !strings.Contains(err.Error(), "ambiguous equivalent tag") {
		t.Fatalf("previousTag() error = %v; want equivalent-tag ambiguity", err)
	}
}

func TestPreviousTagAcceptsEquivalentPredecessorAliasesOnSameCommit(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	previous := commitGitTestFile(t, repo, dir, "previous")
	createGitTestTag(t, repo, "packages/api/v1.0.0+linux", previous)
	createGitTestTag(t, repo, "packages/api/v1.0.0+darwin", previous)
	current := commitGitTestFile(t, repo, dir, "current")
	createGitTestTag(t, repo, "packages/api/v1.1.0", current)

	got, err := previousTag(repo, parseReleaseTag("packages/api/v1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "packages/api/v1.0.0+linux" && got != "packages/api/v1.0.0+darwin" {
		t.Fatalf("previousTag() = %q; want equivalent predecessor alias", got)
	}
}

func TestPreviousTagRejectsEquivalentPredecessorAliasesOnDifferentCommits(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	first := commitGitTestFile(t, repo, dir, "first")
	createGitTestTag(t, repo, "packages/api/v1.0.0+linux", first)
	second := commitGitTestFile(t, repo, dir, "second")
	createGitTestTag(t, repo, "packages/api/v1.0.0+darwin", second)
	current := commitGitTestFile(t, repo, dir, "current")
	createGitTestTag(t, repo, "packages/api/v1.1.0", current)

	_, err = previousTag(repo, parseReleaseTag("packages/api/v1.1.0"))
	if err == nil || !strings.Contains(err.Error(), "ambiguous equivalent predecessor tags") {
		t.Fatalf("previousTag() error = %v; want predecessor ambiguity", err)
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
	hasTag := slices.Contains(tags, "v0.17.0")
	if !hasTag {
		t.Skip("v0.17.0 tag is not available")
	}

	changelog, err := gitChangelog(parseReleaseTag("v0.17.0"))
	if err != nil {
		t.Fatal(err)
	}
	defer deletePath(changelog)
}

func commitGitTestFile(t *testing.T, repo *git.Repository, dir string, message string) plumbing.Hash {
	t.Helper()
	return commitGitTestPaths(t, repo, dir, message, "history")
}

func commitGitTestPaths(t *testing.T, repo *git.Repository, dir string, message string, paths ...string) plumbing.Hash {
	t.Helper()
	stageGitTestPaths(t, repo, dir, message, paths...)
	return commitGitTestIndex(t, repo, message, nil, false)
}

func stageGitTestPaths(t *testing.T, repo *git.Repository, dir string, contents string, paths ...string) {
	t.Helper()

	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		filesystemPath := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(filesystemPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filesystemPath, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := worktree.Add(path); err != nil {
			t.Fatal(err)
		}
	}
}

func moveGitTestPath(t *testing.T, repo *git.Repository, dir string, message string, from string, to string) plumbing.Hash {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, filepath.FromSlash(to))), 0o700); err != nil {
		t.Fatal(err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Move(from, to); err != nil {
		t.Fatal(err)
	}
	return commitGitTestIndex(t, repo, message, nil, false)
}

func removeGitTestPaths(t *testing.T, repo *git.Repository, message string, paths ...string) plumbing.Hash {
	t.Helper()

	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if _, err := worktree.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	return commitGitTestIndex(t, repo, message, nil, false)
}

func commitGitTestIndex(t *testing.T, repo *git.Repository, message string, parents []plumbing.Hash, allowEmpty bool) plumbing.Hash {
	t.Helper()

	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	signature := &object.Signature{Name: "Test", Email: "test@example.com", When: time.Unix(int64(len(message)), 0)}
	hash, err := worktree.Commit(message, &git.CommitOptions{
		Author:            signature,
		Committer:         signature,
		Parents:           parents,
		AllowEmptyCommits: allowEmpty,
	})
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func readGitTestChangelog(t *testing.T, tag string) string {
	t.Helper()

	path, err := gitChangelog(parseReleaseTag(tag))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deletePath(path) })
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func createGitTestTag(t *testing.T, repo *git.Repository, name string, hash plumbing.Hash) {
	t.Helper()
	if _, err := repo.CreateTag(name, hash, nil); err != nil {
		t.Fatal(err)
	}
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
