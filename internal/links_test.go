package flakerelease

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReleaseBodyWithLinks(t *testing.T) {
	image := releaseLink{text: "ghcr.io/owner/repo:1.0.0", url: "https://github.com/owner/repo/pkgs/container/repo"}
	crate := releaseLink{text: "crate@1.0.0", url: "https://crates.io/crates/crate/1.0.0", registry: "crates.io"}
	plain := releaseLink{text: "registry.example/owner/repo:1.0.0"}
	for _, test := range []struct {
		name        string
		body        string
		links       []releaseLink
		want        string
		wantChanged bool
	}{
		{
			name:        "empty body",
			links:       []releaseLink{image, plain},
			want:        "## Packages\n\n- [`ghcr.io/owner/repo:1.0.0`](https://github.com/owner/repo/pkgs/container/repo)\n- `registry.example/owner/repo:1.0.0`",
			wantChanged: true,
		},
		{
			name:        "appends section",
			body:        "## Features\n\n* feat: thing\n",
			links:       []releaseLink{crate},
			want:        "## Features\n\n* feat: thing\n\n## Packages\n\n- [`crate@1.0.0`](https://crates.io/crates/crate/1.0.0) on crates.io",
			wantChanged: true,
		},
		{
			name:        "merges into section",
			body:        "* fix: thing\r\n\r\n## Packages\r\n\r\n" + image.markdown() + "\r\n",
			links:       []releaseLink{image, crate, crate},
			want:        "* fix: thing\n\n## Packages\n\n" + image.markdown() + "\n" + crate.markdown(),
			wantChanged: true,
		},
		{
			name:        "section before another heading",
			body:        "## Packages\n" + image.markdown() + "\n## Notes\n\nnote",
			links:       []releaseLink{crate},
			want:        "## Packages\n" + image.markdown() + "\n" + crate.markdown() + "\n\n## Notes\n\nnote",
			wantChanged: true,
		},
		{
			name:        "empty section",
			body:        "## Packages\n\n## Notes",
			links:       []releaseLink{crate},
			want:        "## Packages\n\n" + crate.markdown() + "\n\n## Notes",
			wantChanged: true,
		},
		{
			name:  "already linked",
			body:  "changes\n\n## Packages\n\n" + image.markdown(),
			links: []releaseLink{image},
			want:  "changes\n\n## Packages\n\n" + image.markdown(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, changed := releaseBodyWithLinks(test.body, test.links)
			if got != test.want || changed != test.wantChanged {
				t.Fatalf("releaseBodyWithLinks() = %q, %t; want %q, %t", got, changed, test.want, test.wantChanged)
			}
		})
	}
}

func TestContainerReleaseLink(t *testing.T) {
	for _, test := range []struct {
		name       string
		provider   releaseProvider
		cfg        config
		repository string
		want       releaseLink
	}{
		{
			name:       "ghcr",
			provider:   releaseGitHub,
			cfg:        config{containerRegistry: "ghcr.io", gitRepository: "Owner/Repo", gitServerURL: "https://github.com"},
			repository: "Owner/Repo/packages/api",
			want:       releaseLink{text: "ghcr.io/owner/repo/packages/api:1.0.0", url: "https://github.com/Owner/Repo/pkgs/container/repo%2Fpackages%2Fapi"},
		},
		{
			name:       "github enterprise",
			provider:   releaseGitHub,
			cfg:        config{containerRegistry: "ghcr.io", gitRepository: "owner/repo", gitServerURL: "https://github.example"},
			repository: "owner/repo",
			want:       releaseLink{text: "ghcr.io/owner/repo:1.0.0"},
		},
		{
			name:       "forgejo",
			provider:   releaseForgejo,
			cfg:        config{containerRegistry: "trev.zip", gitRepository: "llc/pr-tracker", gitServerURL: "https://trev.zip/"},
			repository: "llc/pr-tracker/server",
			want:       releaseLink{text: "trev.zip/llc/pr-tracker/server:1.0.0", url: "https://trev.zip/llc/-/packages/container/pr-tracker%2Fserver/1.0.0"},
		},
		{
			name:       "other registry",
			provider:   releaseGitea,
			cfg:        config{containerRegistry: "docker.io", gitRepository: "owner/repo", gitServerURL: "https://gitea.example"},
			repository: "owner/repo",
			want:       releaseLink{text: "docker.io/owner/repo:1.0.0"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := containerReleaseLink(test.cfg, test.provider, test.repository, "1.0.0"); got != test.want {
				t.Fatalf("containerReleaseLink() = %#v; want %#v", got, test.want)
			}
		})
	}
}

func TestPackageReleaseLinks(t *testing.T) {
	t.Setenv("PACKAGE_REGISTRY_URL", "")
	forgejo := config{packageRegistryOwner: "llc", packageRegistryURL: "https://trev.zip", gitRepository: "llc/repo", gitServerURL: "https://trev.zip"}
	github := config{packageRegistryOwner: "owner", gitRepository: "owner/repo", gitServerURL: "https://github.com"}
	applyPackageRegistryDefaults(&github, releaseGitHub)
	otherOwner := github
	otherOwner.packageRegistryOwner = "other"
	for _, test := range []struct {
		name        string
		provider    releaseProvider
		cfg         config
		publication packagePublication
		want        releaseLink
	}{
		{
			name:        "forgejo npm",
			provider:    releaseForgejo,
			cfg:         forgejo,
			publication: packagePublication{kind: packageNPM, name: "@trevrpc/effect", version: "1.6.0"},
			want:        releaseLink{text: "@trevrpc/effect@1.6.0", url: "https://trev.zip/llc/-/packages/npm/@trevrpc%2Feffect/1.6.0", registry: "Forgejo"},
		},
		{
			name:        "gitea go",
			provider:    releaseGitea,
			cfg:         forgejo,
			publication: packagePublication{kind: packageGo, name: "trev.zip/llc/repo", version: "v1.0.0"},
			want:        releaseLink{text: "trev.zip/llc/repo@v1.0.0", url: "https://trev.zip/llc/-/packages/go/trev.zip%2Fllc%2Frepo/v1.0.0", registry: "Gitea"},
		},
		{
			name:        "forgejo pypi",
			provider:    releaseForgejo,
			cfg:         forgejo,
			publication: packagePublication{kind: packagePyPI, name: "My_Package", version: "1.0.0"},
			want:        releaseLink{text: "My_Package@1.0.0", url: "https://trev.zip/llc/-/packages/pypi/my-package/1.0.0", registry: "Forgejo"},
		},
		{
			name:        "forgejo maven",
			provider:    releaseForgejo,
			cfg:         forgejo,
			publication: packagePublication{kind: packageMaven, name: "zip.trev:core", version: "1.0.0"},
			want:        releaseLink{text: "zip.trev:core:1.0.0", url: "https://trev.zip/llc/-/packages/maven/zip.trev:core/1.0.0", registry: "Forgejo"},
		},
		{
			name:        "forgejo gradle",
			provider:    releaseForgejo,
			cfg:         forgejo,
			publication: packagePublication{kind: packageGradle, name: "zip.trev:root", version: "1.0.0"},
			want:        releaseLink{text: "zip.trev:*:1.0.0", url: "https://trev.zip/llc/-/packages?q=zip.trev&type=maven", registry: "Forgejo"},
		},
		{
			name:        "github npm",
			provider:    releaseGitHub,
			cfg:         github,
			publication: packagePublication{kind: packageNPM, name: "@owner/pkg", version: "1.0.0"},
			want:        releaseLink{text: "@owner/pkg@1.0.0", url: "https://github.com/owner/repo/pkgs/npm/pkg", registry: "GitHub"},
		},
		{
			name:        "github maven",
			provider:    releaseGitHub,
			cfg:         github,
			publication: packagePublication{kind: packageMaven, name: "com.example:lib", version: "1.0.0"},
			want:        releaseLink{text: "com.example:lib:1.0.0", url: "https://github.com/owner/repo/pkgs/maven/com.example.lib", registry: "GitHub"},
		},
		{
			name:        "github gradle",
			provider:    releaseGitHub,
			cfg:         github,
			publication: packagePublication{kind: packageGradle, name: "com.example:lib", version: "1.0.0"},
			want:        releaseLink{text: "com.example:*:1.0.0", registry: "GitHub"},
		},
		{
			name:        "github other owner",
			provider:    releaseGitHub,
			cfg:         otherOwner,
			publication: packagePublication{kind: packageNPM, name: "@other/pkg", version: "1.0.0"},
			want:        releaseLink{text: "@other/pkg@1.0.0", registry: "GitHub"},
		},
		{
			name:        "crates.io",
			provider:    releaseForgejo,
			cfg:         forgejo,
			publication: packagePublication{kind: packageCargo, registry: registryCratesIO, name: "crate", version: "1.0.0"},
			want:        releaseLink{text: "crate@1.0.0", url: "https://crates.io/crates/crate/1.0.0", registry: "crates.io"},
		},
		{
			name:        "npmjs",
			provider:    releaseForgejo,
			cfg:         forgejo,
			publication: packagePublication{kind: packageNPM, registry: registryNPMJS, name: "@scope/pkg", version: "1.0.0"},
			want:        releaseLink{text: "@scope/pkg@1.0.0", url: "https://www.npmjs.com/package/@scope/pkg/v/1.0.0", registry: "npmjs.com"},
		},
		{
			name:        "pypi",
			provider:    releaseForgejo,
			cfg:         forgejo,
			publication: packagePublication{kind: packagePyPI, registry: registryPyPI, name: "My.Package", version: "1.0.0"},
			want:        releaseLink{text: "My.Package@1.0.0", url: "https://pypi.org/project/my-package/1.0.0/", registry: "pypi.org"},
		},
		{
			name:        "maven central",
			provider:    releaseForgejo,
			cfg:         forgejo,
			publication: packagePublication{kind: packageMaven, registry: registryMavenCentral, name: "com.example:lib", version: "1.0.0"},
			want:        releaseLink{text: "com.example:lib:1.0.0", url: "https://central.sonatype.com/artifact/com.example/lib/1.0.0", registry: "Maven Central"},
		},
		{
			name:        "maven central gradle",
			provider:    releaseForgejo,
			cfg:         forgejo,
			publication: packagePublication{kind: packageGradle, registry: registryMavenCentral, name: "com.example:root", version: "1.0.0"},
			want:        releaseLink{text: "com.example:*:1.0.0", url: "https://central.sonatype.com/namespace/com.example", registry: "Maven Central"},
		},
		{
			name:        "pkg.go.dev",
			provider:    releaseForgejo,
			cfg:         forgejo,
			publication: packagePublication{kind: packageGo, registry: registryPkgGoDev, name: "trev.zip/llc/repo/v2", version: "v2.0.0"},
			want:        releaseLink{text: "trev.zip/llc/repo/v2@v2.0.0", url: "https://pkg.go.dev/trev.zip/llc/repo/v2@v2.0.0", registry: "pkg.go.dev"},
		},
		{
			name:        "hex.pm",
			provider:    releaseForgejo,
			cfg:         forgejo,
			publication: packagePublication{kind: packageGleam, registry: registryHexPM, name: "lib", version: "1.0.0"},
			want:        releaseLink{text: "lib@1.0.0", url: "https://hex.pm/packages/lib/1.0.0", registry: "hex.pm"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			set := &packagePublicationSet{cfg: test.cfg, provider: test.provider}
			if got := set.releaseLink(test.publication); got != test.want {
				t.Fatalf("releaseLink() = %#v; want %#v", got, test.want)
			}
		})
	}
}

func TestPackageReleaseLinksOnlyPublished(t *testing.T) {
	set := &packagePublicationSet{
		cfg:      config{packageRegistryOwner: "owner", packageRegistryURL: "https://git.example"},
		provider: releaseForgejo,
		packages: []packagePublication{
			{kind: packageCargo, name: "published", version: "1.0.0", published: true},
			{kind: packageCargo, name: "failed", version: "1.0.0"},
		},
	}
	if links := set.releaseLinks(); len(links) != 1 || links[0].text != "published@1.0.0" {
		t.Fatalf("releaseLinks() = %#v; want only the published package", links)
	}
	set.cfg.dryRun = true
	if links := set.releaseLinks(); len(links) != 2 {
		t.Fatalf("dry run releaseLinks() = %#v; want every package", links)
	}
	if links := (*packagePublicationSet)(nil).releaseLinks(); links != nil {
		t.Fatalf("nil releaseLinks() = %#v; want nil", links)
	}
}

func TestAddReleaseLinks(t *testing.T) {
	link := releaseLink{text: "crate@1.0.0", url: "https://crates.io/crates/crate/1.0.0", registry: "crates.io"}
	for _, provider := range []releaseProvider{releaseGitHub, releaseGitea, releaseForgejo} {
		for _, test := range []struct {
			name      string
			body      string
			wantPatch string
		}{
			{name: "adds links", body: "changes", wantPatch: "changes\n\n## Packages\n\n" + link.markdown()},
			{name: "already linked", body: "## Packages\n\n" + link.markdown()},
		} {
			t.Run(string(provider)+"/"+test.name, func(t *testing.T) {
				prefix := "/api/v1/repos/owner/repo/releases"
				if provider == releaseGitHub {
					prefix = "/api/v3/repos/owner/repo/releases"
				}
				var patched []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.Method == http.MethodGet && r.URL.Path == prefix+"/tags/v1.0.0":
						writeCleanupJSON(w, map[string]any{"id": 7, "tag_name": "v1.0.0", "body": test.body})
					case r.Method == http.MethodPatch && r.URL.Path == prefix+"/7":
						var request editReleaseRequest
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							http.Error(w, err.Error(), http.StatusBadRequest)
							return
						}
						patched = append(patched, request.Body)
						writeCleanupJSON(w, map[string]any{"id": 7})
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()

				if err := cleanupTestClient(provider, server.URL).addReleaseLinks("v1.0.0", []releaseLink{link}); err != nil {
					t.Fatal(err)
				}
				switch {
				case test.wantPatch == "" && len(patched) != 0:
					t.Fatalf("addReleaseLinks() patched %q; want no update", patched)
				case test.wantPatch != "" && (len(patched) != 1 || patched[0] != test.wantPatch):
					t.Fatalf("addReleaseLinks() patched %q; want %q", patched, test.wantPatch)
				}
			})
		}
	}
}
