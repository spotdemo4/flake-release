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
	for _, test := range []struct {
		name string
		plan releasePackagePlan
		hint string
		tags []releaseTag
		want string
		skip string
		err  string
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
			var hint *releaseTag
			if test.hint != "" {
				parsed := parseReleaseTag(test.hint)
				hint = &parsed
			}
			got, skip, err := resolvePackageTag(test.plan, hint, test.tags)
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

	groups, err := groupReleasePackages(plans, nil, tags)
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

	_, err := groupReleasePackages(plans, nil, tags)
	if err == nil || !strings.Contains(err.Error(), "packages.one") || !strings.Contains(err.Error(), "packages.two") {
		t.Fatalf("groupReleasePackages() error = %v; want both ambiguities", err)
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
