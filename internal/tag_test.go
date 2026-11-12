package flakerelease

import (
	"strings"
	"testing"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

func parseReleaseTags(names ...string) []releaseTag {
	tags := make([]releaseTag, 0, len(names))
	for _, name := range names {
		tags = append(tags, parseReleaseTag(name))
	}
	return tags
}

func TestResolvePackageTag(t *testing.T) {
	monorepo := parseReleaseTags(
		"v1.0.0",
		"v1.1.0",
		"packages/api/v1.0.0",
		"packages/api/v1.2.3",
		"packages/web/v1.0.0",
		"packages/web/0.4.0",
	)
	trevrpc := parseReleaseTags(
		"v1.9.0",
		"trevrpc-go/v1.9.0",
		"trevrpc-go/oteltrevrpc/v1.9.0",
		"trevrpc-js/v1.9.0",
		"trevrpc-js/effect/v1.9.0",
		"trevrpc-rust/v1.9.0",
		"trevrpc-rust/crates/trevrpc-opentelemetry/v1.9.0",
	)
	nested := parseReleaseTags("dir/v1.0.0", "dir/sub/v1.0.0")
	for _, test := range []struct {
		name   string
		plan   releasePackagePlan
		hint   string
		ci     bool
		source string
		tags   []releaseTag
		want   string
		skip   string
		err    string
	}{
		{
			name: "unscoped version",
			plan: releasePackagePlan{pkg: "packages.default", pname: "app", version: "1.1.0"},
			tags: monorepo,
			want: "v1.1.0",
		},
		{
			name: "scoped version",
			plan: releasePackagePlan{pkg: "packages.api", pname: "api", version: "1.2.3"},
			tags: monorepo,
			want: "packages/api/v1.2.3",
		},
		{
			name: "tag without v prefix",
			plan: releasePackagePlan{pkg: "packages.web", pname: "web", version: "0.4.0"},
			tags: monorepo,
			want: "packages/web/0.4.0",
		},
		{
			name: "image tag",
			plan: releasePackagePlan{pkg: "images.api", imageName: "api", imageTag: "1.2.3"},
			tags: monorepo,
			want: "packages/api/v1.2.3",
		},
		{
			name: "pname breaks ties",
			plan: releasePackagePlan{pkg: "packages.web", pname: "web", version: "1.0.0"},
			tags: monorepo,
			want: "packages/web/v1.0.0",
		},
		{
			name: "ambiguous without matching pname",
			plan: releasePackagePlan{pkg: "packages.default", pname: "app", version: "1.0.0"},
			tags: monorepo,
			err:  "matches multiple git tags (packages/api/v1.0.0, packages/web/v1.0.0, v1.0.0); set TAG",
		},
		{
			name: "hint breaks ties",
			plan: releasePackagePlan{pkg: "packages.default", pname: "app", version: "1.0.0"},
			hint: "v1.0.0",
			tags: monorepo,
			want: "v1.0.0",
		},
		{
			name: "hint need not exist yet",
			plan: releasePackagePlan{pkg: "packages.default", pname: "app", version: "2.0.0"},
			hint: "v2.0.0",
			want: "v2.0.0",
		},
		{
			name: "hint filters other versions",
			plan: releasePackagePlan{pkg: "packages.web", pname: "web", version: "0.4.0"},
			hint: "packages/api/v1.2.3",
			tags: monorepo,
			skip: `version "0.4.0" does not match git tag "packages/api/v1.2.3"`,
		},
		{
			name: "scoped CI tag releases matching versions to it",
			plan: releasePackagePlan{pkg: "packages.default", pname: "app", version: "1.0.0"},
			hint: "packages/api/v1.0.0",
			ci:   true,
			tags: monorepo,
			want: "packages/api/v1.0.0",
		},
		{
			name: "unscoped CI tag releases a package to its scoped tag",
			plan: releasePackagePlan{pkg: "packages.api", pname: "api", version: "1.0.0"},
			hint: "v1.0.0",
			ci:   true,
			tags: parseReleaseTags("v1.0.0", "packages/api/v1.0.0", "packages/web/v1.0.0"),
			want: "packages/api/v1.0.0",
		},
		{
			name: "unscoped CI tag is the fallback",
			plan: releasePackagePlan{pkg: "packages.default", pname: "app", version: "1.0.0"},
			hint: "v1.0.0",
			ci:   true,
			tags: parseReleaseTags("v1.0.0", "packages/api/v1.0.0", "packages/web/v1.0.0"),
			want: "v1.0.0",
		},
		{
			name: "unscoped CI tag need not be listed",
			plan: releasePackagePlan{pkg: "packages.default", pname: "app", version: "1.0.0"},
			hint: "v1.0.0",
			ci:   true,
			want: "v1.0.0",
		},
		{
			name: "unscoped CI tag filters other versions",
			plan: releasePackagePlan{pkg: "packages.api", pname: "api", version: "1.2.3"},
			hint: "v1.0.0",
			ci:   true,
			tags: monorepo,
			skip: `version "1.2.3" does not match git tag "v1.0.0"`,
		},
		{
			name: "unscoped CI tag ignores tags for the image tag",
			plan: releasePackagePlan{pkg: "images.api", imageName: "api", version: "1.0.0", imageTag: "1.2.3"},
			hint: "v1.0.0",
			ci:   true,
			tags: monorepo,
			want: "v1.0.0",
		},
		{
			name:   "source directory breaks ties",
			plan:   releasePackagePlan{pkg: "packages.trevrpc-js-effect", pname: "trevrpc-js-effect", version: "1.9.0"},
			source: "trevrpc-js/effect",
			tags:   trevrpc,
			want:   "trevrpc-js/effect/v1.9.0",
		},
		{
			name:   "source directory with an unscoped CI tag",
			plan:   releasePackagePlan{pkg: "packages.trevrpc-rust-otel", pname: "trevrpc-rust-otel", version: "1.9.0"},
			hint:   "v1.9.0",
			ci:     true,
			source: "trevrpc-rust/crates/trevrpc-opentelemetry",
			tags:   trevrpc,
			want:   "trevrpc-rust/crates/trevrpc-opentelemetry/v1.9.0",
		},
		{
			name:   "source directory takes precedence over pname",
			plan:   releasePackagePlan{pkg: "packages.go", pname: "oteltrevrpc", version: "1.9.0"},
			source: "trevrpc-go",
			tags:   trevrpc,
			want:   "trevrpc-go/v1.9.0",
		},
		{
			name:   "source directory matches its tagged directory",
			plan:   releasePackagePlan{pkg: "packages.dir", pname: "one", version: "1.0.0"},
			source: "dir",
			tags:   nested,
			want:   "dir/v1.0.0",
		},
		{
			name:   "source subdirectory matches the nearest tagged directory",
			plan:   releasePackagePlan{pkg: "packages.sub", pname: "two", version: "1.0.0"},
			source: "dir/sub/cmd/two",
			tags:   nested,
			want:   "dir/sub/v1.0.0",
		},
		{
			name:   "source directory must match whole path segments",
			plan:   releasePackagePlan{pkg: "packages.dirt", pname: "three", version: "1.0.0"},
			source: "dirt",
			tags:   nested,
			err:    "matches multiple git tags (dir/sub/v1.0.0, dir/v1.0.0); set TAG",
		},
		{
			name:   "source outside every tagged directory is ambiguous",
			plan:   releasePackagePlan{pkg: "packages.other", pname: "other", version: "1.0.0"},
			source: "other",
			tags:   nested,
			err:    "matches multiple git tags (dir/sub/v1.0.0, dir/v1.0.0); set TAG",
		},
		{
			name:   "source outside every scoped tag without a CI tag is ambiguous",
			plan:   releasePackagePlan{pkg: "packages.other", pname: "other", version: "1.0.0"},
			source: "other",
			tags:   parseReleaseTags("v1.0.0", "dir/v1.0.0"),
			err:    "matches multiple git tags (dir/v1.0.0, v1.0.0); set TAG",
		},
		{
			name: "no matching tag",
			plan: releasePackagePlan{pkg: "packages.api", pname: "api", version: "9.9.9"},
			tags: monorepo,
			skip: `no git tag found for version "9.9.9"`,
		},
		{
			name: "no version",
			plan: releasePackagePlan{pkg: "packages.tool", pname: "tool"},
			tags: monorepo,
			skip: "package has no version",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			hint, err := newTagHint(test.hint, test.ci)
			if err != nil {
				t.Fatal(err)
			}
			sourcePath := func(plan releasePackagePlan) string {
				if plan.pkg != test.plan.pkg {
					t.Fatalf("source path requested for %s", plan.pkg)
				}
				return test.source
			}
			got, skip, err := resolvePackageTag(test.plan, hint, test.tags, sourcePath)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("resolvePackageTag() error = %v; want %q", err, test.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if skip != test.skip {
				t.Fatalf("resolvePackageTag() skip = %q; want %q", skip, test.skip)
			}
			if got.full != test.want {
				t.Fatalf("resolvePackageTag() = %q; want %q", got.full, test.want)
			}
		})
	}
}

func TestGroupReleasePackagesGroupsByTag(t *testing.T) {
	tags := parseReleaseTags("packages/api/v1.2.3", "packages/web/v0.4.0")
	plans := []releasePackagePlan{
		{pkg: "packages.api", pname: "api", version: "1.2.3"},
		{pkg: "packages.web", pname: "web", version: "0.4.0"},
		{pkg: "images.api", imageName: "api", imageTag: "1.2.3"},
		{pkg: "packages.unreleased", pname: "unreleased", version: "0.0.1"},
	}

	groups, err := groupReleasePackages(plans, nil, tags, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("groupReleasePackages() returned %d groups; want 2", len(groups))
	}
	if groups[0].tag.full != "packages/api/v1.2.3" || len(groups[0].packages) != 2 ||
		groups[0].packages[0].pkg != "packages.api" || groups[0].packages[1].pkg != "images.api" {
		t.Fatalf("api group = %+v", groups[0])
	}
	if groups[1].tag.full != "packages/web/v0.4.0" || len(groups[1].packages) != 1 {
		t.Fatalf("web group = %+v", groups[1])
	}
}

func TestGroupReleasePackagesReportsEveryAmbiguity(t *testing.T) {
	tags := parseReleaseTags("a/v1.0.0", "b/v1.0.0", "a/v2.0.0", "b/v2.0.0")
	plans := []releasePackagePlan{
		{pkg: "packages.one", pname: "one", version: "1.0.0"},
		{pkg: "packages.two", pname: "two", version: "2.0.0"},
	}

	_, err := groupReleasePackages(plans, nil, tags, nil)
	if err == nil || !strings.Contains(err.Error(), "packages.one") || !strings.Contains(err.Error(), "packages.two") {
		t.Fatalf("groupReleasePackages() error = %v; want both ambiguities", err)
	}
}

func TestGroupReleasePackagesFromUnscopedCITag(t *testing.T) {
	tags := parseReleaseTags("v1.0.0", "a/v1.0.0", "b/v1.0.0")
	plans := []releasePackagePlan{
		{pkg: "packages.a", pname: "a", version: "1.0.0"},
		{pkg: "packages.b", pname: "package-b", version: "1.0.0"},
		{pkg: "packages.c", pname: "c", version: "1.0.0"},
		{pkg: "packages.old", pname: "old", version: "0.9.0"},
	}
	sources := map[string]string{"packages.b": "b/cmd/package-b", "packages.c": "c"}
	hint, err := newTagHint("v1.0.0", true)
	if err != nil {
		t.Fatal(err)
	}

	groups, err := groupReleasePackages(plans, hint, tags, func(plan releasePackagePlan) string { return sources[plan.pkg] })
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, group := range groups {
		for _, plan := range group.packages {
			got = append(got, plan.pkg+"="+group.tag.full)
		}
	}
	want := "packages.a=a/v1.0.0,packages.b=b/v1.0.0,packages.c=v1.0.0"
	if strings.Join(got, ",") != want {
		t.Fatalf("groupReleasePackages() = %s; want %s", strings.Join(got, ","), want)
	}
}

func TestGitHeadReleaseTagsOnlyIncludesHead(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	first := commitGitTestFile(t, repo, dir, "first")
	createGitTestTag(t, repo, "a/v1.0.0", first)
	second := commitGitTestFile(t, repo, dir, "second")
	createGitTestTag(t, repo, "v1.0.0", second)
	createGitTestTag(t, repo, "b/v1.0.0", second)
	chdir(t, dir)

	tags, err := gitHeadReleaseTags()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tag := range tags {
		names = append(names, tag.full)
	}
	if got, want := strings.Join(names, ","), "b/v1.0.0,v1.0.0"; got != want {
		t.Fatalf("gitHeadReleaseTags() = %s; want %s", got, want)
	}
}

func TestGitReachableReleaseTagsIgnoresOtherHistory(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)

	first := commitGitTestFile(t, repo, dir, "first")
	createGitTestTag(t, repo, "v1.0.0", first)
	createGitTestTag(t, repo, "packages/api/v1.0.0", first)
	createGitTestTag(t, repo, "packages/api/v", first)
	second := commitGitTestFile(t, repo, dir, "second")
	createGitTestTag(t, repo, "packages/web/v0.1.0", second)
	stageGitTestPaths(t, repo, dir, "side", "side")
	side := commitGitTestIndex(t, repo, "side branch", []plumbing.Hash{first}, false)
	createGitTestTag(t, repo, "v9.0.0", side)

	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := worktree.Checkout(&git.CheckoutOptions{Hash: second, Force: true}); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	tags, err := gitReachableReleaseTags()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tag := range tags {
		names = append(names, tag.full)
	}
	want := "packages/api/v1.0.0,packages/web/v0.1.0,v1.0.0"
	if got := strings.Join(names, ","); got != want {
		t.Fatalf("gitReachableReleaseTags() = %s; want %s", got, want)
	}
}
