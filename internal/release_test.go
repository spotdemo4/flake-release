package flakerelease

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestReleaseType(t *testing.T) {
	t.Setenv("GIT_TYPE", "")
	t.Setenv("FORGEJO_ACTIONS", "")
	t.Setenv("GITEA_ACTIONS", "")
	t.Setenv("GITHUB_ACTIONS", "")

	tests := []struct {
		origin string
		want   releaseProvider
	}{
		{origin: "git@github.com:owner/project.git", want: releaseGitHub},
		{origin: "https://trev.zip/llc/flake-release.git", want: releaseForgejo},
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

	got, err := releaseType("https://trev.zip/llc/flake-release.git")
	if err != nil {
		t.Fatalf("releaseType returned error: %v", err)
	}
	if got != releaseForgejo {
		t.Fatalf("releaseType with override = %q; want %q", got, releaseForgejo)
	}
}

func TestNewReleaseClient(t *testing.T) {
	cfg := config{
		githubRepository: "owner/repo",
		githubServerURL:  "https://git.example",
		githubToken:      "test-token",
	}

	githubClient := newReleaseClient(releaseGitHub, cfg)
	if client, ok := githubClient.(githubReleaseClient); !ok {
		t.Fatalf("GitHub client type = %T; want githubReleaseClient", githubClient)
	} else if client.cfg != cfg {
		t.Fatal("GitHub client did not keep config")
	}

	giteaClient := newReleaseClient(releaseGitea, cfg)
	if client, ok := giteaClient.(giteaReleaseClient); !ok {
		t.Fatalf("Gitea client type = %T; want giteaReleaseClient", giteaClient)
	} else if client.name != "Gitea" {
		t.Fatalf("Gitea client name = %q; want Gitea", client.name)
	}

	forgejoClient := newReleaseClient(releaseForgejo, cfg)
	if client, ok := forgejoClient.(forgejoReleaseClient); !ok {
		t.Fatalf("Forgejo client type = %T; want forgejoReleaseClient", forgejoClient)
	} else if client.name != "Forgejo" {
		t.Fatalf("Forgejo client name = %q; want Forgejo", client.name)
	}

	unknownClient := newReleaseClient("", cfg)
	if _, ok := unknownClient.(noopReleaseClient); !ok {
		t.Fatalf("unknown client type = %T; want noopReleaseClient", unknownClient)
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

	body, err := httpRequest(httpRequestOptions{
		method:     http.MethodGet,
		url:        server.URL,
		token:      "test-token",
		authScheme: tokenAuthScheme,
		accept:     jsonAccept,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q; want JSON", body)
	}

	body, err = httpRequest(httpRequestOptions{
		method:     http.MethodDelete,
		url:        server.URL,
		token:      "test-token",
		authScheme: tokenAuthScheme,
		accept:     jsonAccept,
	})
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		t.Fatalf("DELETE body = %q; want nil", body)
	}

	if _, err := httpRequest(httpRequestOptions{
		method:     http.MethodGet,
		url:        server.URL + "/fail",
		token:      "test-token",
		authScheme: tokenAuthScheme,
		accept:     jsonAccept,
	}); err == nil {
		t.Fatal("httpRequest returned nil error for non-2xx response")
	}
}

func TestParseRepository(t *testing.T) {
	repo, err := parseRepository("owner/project")
	if err != nil {
		t.Fatal(err)
	}
	if repo.owner != "owner" || repo.name != "project" {
		t.Fatalf("parseRepository returned %#v", repo)
	}

	if _, err := parseRepository("owner/nested/project"); err == nil {
		t.Fatal("parseRepository returned nil error for nested repository path")
	}
}

func TestReleaseCleanupCandidateOnlySelectsOlderNamespaceReleases(t *testing.T) {
	current, err := parseSelectedReleaseTag("packages/cli/v2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		candidate string
		want      bool
	}{
		{candidate: "packages/cli/v1.0.0", want: true},
		{candidate: "packages/cli/v2.0.0", want: false},
		{candidate: "packages/cli/v3.0.0", want: false},
		{candidate: "packages/web/v1.0.0", want: false},
		{candidate: "packages/cli/v", want: true},
		{candidate: "packages/cli/", want: false},
		{candidate: "packages/cli/v1.0.0.lock", want: false},
		{candidate: "packages/cli/v~", want: false},
	}
	for _, test := range tests {
		if got := releaseCleanupCandidate(current, test.candidate); got != test.want {
			t.Fatalf("releaseCleanupCandidate(%q) = %t; want %t", test.candidate, got, test.want)
		}
	}

	root, err := parseSelectedReleaseTag("v2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !releaseCleanupCandidate(root, "v") {
		t.Fatal("legacy root release was excluded from cleanup")
	}
}

func TestReleaseAssetUploadURL(t *testing.T) {
	got, err := releaseAssetUploadURL("https://uploads.github.com/repos/o/r/releases/1/assets{?name,label}", "asset name.zip")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://uploads.github.com/repos/o/r/releases/1/assets?name=asset+name.zip"
	if got != want {
		t.Fatalf("releaseAssetUploadURL = %q; want %q", got, want)
	}
}

func TestAPIBaseURLs(t *testing.T) {
	if got := githubAPIBase(config{}); got != "https://api.github.com" {
		t.Fatalf("githubAPIBase(empty) = %q; want GitHub API", got)
	}
	if got := githubAPIBase(config{githubServerURL: "https://github.example/"}); got != "https://github.example/api/v3" {
		t.Fatalf("githubAPIBase(custom) = %q; want custom API", got)
	}
	if got := giteaAPIBase(config{githubServerURL: "https://git.example/"}); got != "https://git.example/api/v1" {
		t.Fatalf("giteaAPIBase() = %q; want Gitea API", got)
	}
}

func TestRetainedReleaseTags(t *testing.T) {
	current, err := parseSelectedReleaseTag("packages/cli/v2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		retain int
		tags   []string
		want   []string
	}{
		{
			name:   "keep one previous",
			retain: 2,
			tags:   []string{"packages/cli/v1.0.0", "packages/cli/v1.5.0", "packages/web/v1.9.0", "packages/cli/v3.0.0"},
			want:   []string{"packages/cli/v1.5.0"},
		},
		{
			name:   "keep two previous and ignore duplicates",
			retain: 3,
			tags:   []string{"packages/cli/v1.0.0", "packages/cli/v1.5.0", "packages/cli/v1.5.0", "packages/cli/v1.2.0", "packages/web/v9.0.0"},
			want:   []string{"packages/cli/v1.5.0", "packages/cli/v1.2.0"},
		},
		{
			name:   "large retention keeps all eligible",
			retain: 20,
			tags:   []string{"packages/cli/v1.0.0", "packages/cli/v1.5.0", "packages/cli/v", "packages/cli/v2.0.0", "packages/cli/v3.0.0", "v1.9.0"},
			want:   []string{"packages/cli/v1.5.0", "packages/cli/v1.0.0"},
		},
		{
			name:   "zero retention",
			retain: 0,
			tags:   []string{"packages/cli/v1.0.0"},
			want:   nil,
		},
		{
			name:   "current only",
			retain: 1,
			tags:   []string{"packages/cli/v1.0.0"},
		},
		{
			name:   "semantic ordering and prereleases",
			retain: 3,
			tags:   []string{"packages/cli/v1.9.0", "packages/cli/v1.10.0-rc.1", "packages/cli/v1.10.0", "packages/cli/v2.0.0+build"},
			want:   []string{"packages/cli/v1.10.0", "packages/cli/v1.10.0-rc.1"},
		},
		{
			name:   "lexical full-tag tie break",
			retain: 3,
			tags:   []string{"packages/cli/v1.0.0+z", "packages/cli/v1.0.0+a", "packages/cli/v1.0.0+z"},
			want:   []string{"packages/cli/v1.0.0+a", "packages/cli/v1.0.0+z"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := retainedReleaseTags(current, test.tags, test.retain)
			if len(got) != len(test.want) {
				t.Fatalf("retainedReleaseTags() = %#v; want %#v", got, test.want)
			}
			for i, tag := range got {
				if tag.full != test.want[i] {
					t.Fatalf("retainedReleaseTags()[%d] = %q; want %q", i, tag.full, test.want[i])
				}
			}
		})
	}
}

func TestCleanupAssetsRetentionAcrossProviders(t *testing.T) {
	current := parseReleaseTag("packages/cli/v2.0.0")
	tags := []string{
		"packages/cli/v1.0.0", "packages/cli/v1.2.0", "packages/cli/v1.1.0", "packages/cli/v1.3.0",
		"packages/cli/v2.0.0", "packages/cli/v3.0.0", "packages/web/v1.9.0",
	}
	for _, provider := range []releaseProvider{releaseGitHub, releaseGitea, releaseForgejo} {
		for _, test := range []struct {
			retain   int
			deleted  []int64
			listed   []int64
			retained []string
		}{
			{retain: 0},
			{retain: 1, deleted: []int64{1, 2, 3}, listed: []int64{1, 2, 3, 4}},
			{retain: 2, deleted: []int64{1, 2, 3}, listed: []int64{1, 2, 3}, retained: []string{tags[3]}},
			{retain: 3, deleted: []int64{1, 3}, listed: []int64{1, 3}, retained: []string{tags[3], tags[1]}},
			{retain: 50, retained: []string{tags[3], tags[1], tags[2], tags[0]}},
		} {
			t.Run(string(provider)+"/retain-"+strconv.Itoa(test.retain), func(t *testing.T) {
				recorder := runCleanupServer(t, provider, tags, nil, map[int64]bool{1: true, 2: true, 3: true})
				client := cleanupTestClient(provider, recorder.server.URL)
				retained, err := client.cleanupAssets(current, test.retain)
				if err != nil {
					t.Fatal(err)
				}
				var retainedNames []string
				for _, tag := range retained {
					retainedNames = append(retainedNames, tag.full)
				}
				if !slices.Equal(retainedNames, test.retained) {
					t.Fatalf("retained %v; want %v (assetless releases must count)", retainedNames, test.retained)
				}
				if !slices.Equal(recorder.deletedIDs, test.deleted) || !slices.Equal(recorder.assetListIDs, test.listed) {
					t.Fatalf("deleted %v, listed %v; want %v, %v", recorder.deletedIDs, recorder.assetListIDs, test.deleted, test.listed)
				}
				wantPages := []int{1}
				if test.retain == 0 {
					wantPages = nil
				}
				if !slices.Equal(recorder.pages, wantPages) {
					t.Fatalf("release-list pages = %v; want %v", recorder.pages, wantPages)
				}
			})
		}
	}
}

func TestCleanupAssetsPaginationAndErrors(t *testing.T) {
	current, err := parseSelectedReleaseTag("v200.0.0")
	if err != nil {
		t.Fatal(err)
	}
	older := []string{"v199.0.0", "v198.0.0", "v197.0.0"}
	tags := make([]string, 0, 103)
	for i := 399; i >= 300; i-- {
		tags = append(tags, fmt.Sprintf("v%d.0.0", i))
	}
	tags = append(tags, older...)
	for _, provider := range []releaseProvider{releaseGitHub, releaseGitea, releaseForgejo} {
		t.Run(string(provider)+"/pagination", func(t *testing.T) {
			assets := map[int64]bool{101: true, 102: true, 103: true}
			deleted := runCleanupServer(t, provider, tags, nil, assets)
			client := cleanupTestClient(provider, deleted.server.URL)
			if _, err := client.cleanupAssets(current, 2); err != nil {
				t.Fatal(err)
			}
			if len(deleted.deletedIDs) != 2 || deleted.deletedIDs[0] != 102 || deleted.deletedIDs[1] != 103 {
				t.Fatalf("deleted assets %v; want only releases older than retained v199.0.0", deleted.deletedIDs)
			}
			if len(deleted.assetListIDs) != 2 || deleted.assetListIDs[0] != 102 || deleted.assetListIDs[1] != 103 {
				t.Fatalf("asset list release IDs %v; want [102 103]", deleted.assetListIDs)
			}
			if !slices.Equal(deleted.pages, []int{1, 2}) {
				t.Fatalf("release-list pages = %v; want [1 2] without redundant listing", deleted.pages)
			}
		})

		t.Run(string(provider)+"/later page failure", func(t *testing.T) {
			firstPage := make([]string, 101)
			for i := range firstPage {
				firstPage[i] = fmt.Sprintf("v1.0.%d", i)
			}
			deleted := runCleanupServer(t, provider, firstPage, map[string]bool{"releases/2": true}, map[int64]bool{1: true})
			client := cleanupTestClient(provider, deleted.server.URL)
			if _, err := client.cleanupAssets(current, 2); err == nil {
				t.Fatal("cleanup returned nil after second page failed")
			}
			if len(deleted.assetListIDs) != 0 || len(deleted.deletedIDs) != 0 {
				t.Fatal("cleanup started before all release pages were fetched")
			}
		})

		t.Run(string(provider)+"/listing failure", func(t *testing.T) {
			deleted := runCleanupServer(t, provider, nil, map[string]bool{"releases": true}, nil)
			client := cleanupTestClient(provider, deleted.server.URL)
			if _, err := client.cleanupAssets(current, 2); err == nil {
				t.Fatal("cleanup returned nil after release listing failed")
			}
			if len(deleted.deletedIDs) != 0 {
				t.Fatalf("deleted assets after listing failure: %v", deleted.deletedIDs)
			}
		})

		t.Run(string(provider)+"/asset errors accumulated", func(t *testing.T) {
			failureTags := []string{"v199.0.0", "v198.0.0", "v197.0.0", "v196.0.0"}
			failures := map[string]bool{"assets/2": true, "delete/3": true}
			deleted := runCleanupServer(t, provider, failureTags, failures, map[int64]bool{3: true, 4: true})
			client := cleanupTestClient(provider, deleted.server.URL)
			_, err := client.cleanupAssets(current, 2)
			if err == nil || !strings.Contains(err.Error(), "fetching") || !strings.Contains(err.Error(), "deleting") {
				t.Fatalf("cleanup error = %v; want combined fetch and deletion errors", err)
			}
			if !slices.Equal(deleted.deletedIDs, []int64{3, 4}) {
				t.Fatalf("cleanup attempted deletions %v; want [3 4] despite earlier errors", deleted.deletedIDs)
			}
		})
	}
}

type cleanupRecorder struct {
	server       *httptest.Server
	pages        []int
	deletedIDs   []int64
	assetListIDs []int64
}

func cleanupTestClient(provider releaseProvider, serverURL string) releaseClient {
	cfg := config{
		githubRepository: "owner/repo",
		githubServerURL:  serverURL,
		githubToken:      "test-token",
	}
	return newReleaseClient(provider, cfg)
}

func runCleanupServer(t *testing.T, provider releaseProvider, tags []string, failures map[string]bool, assets map[int64]bool) *cleanupRecorder {
	t.Helper()
	recorder := &cleanupRecorder{}
	prefix := "/api/v1/repos/owner/repo/releases"
	if provider == releaseGitHub {
		prefix = "/api/v3/repos/owner/repo/releases"
	}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == prefix && r.Method == http.MethodGet {
			page, err := strconv.Atoi(r.URL.Query().Get("page"))
			if err != nil || page < 1 {
				http.Error(w, "invalid page", http.StatusBadRequest)
				return
			}
			recorder.pages = append(recorder.pages, page)
			if failures["releases"] || failures[fmt.Sprintf("releases/%d", page)] {
				http.Error(w, "release listing failed", http.StatusInternalServerError)
				return
			}
			start := min((page-1)*100, len(tags))
			end := min(start+100, len(tags))
			response := make([]map[string]any, 0, end-start)
			for i, tag := range tags[start:end] {
				id := int64(start + i + 1)
				response = append(response, map[string]any{"id": id, "tag_name": tag})
			}
			writeCleanupJSON(w, response)
			return
		}
		if strings.Contains(r.URL.Path, "/releases/") && strings.HasSuffix(r.URL.Path, "/assets") && r.Method == http.MethodGet {
			parts := strings.Split(r.URL.Path, "/")
			releaseID, _ := strconv.ParseInt(parts[len(parts)-2], 10, 64)
			recorder.assetListIDs = append(recorder.assetListIDs, releaseID)
			if failures[fmt.Sprintf("assets/%d", releaseID)] {
				http.Error(w, "asset listing failed", http.StatusInternalServerError)
				return
			}
			if assets[releaseID] {
				writeCleanupJSON(w, []releaseAsset{{ID: releaseID, Name: "asset.tar"}})
			} else {
				writeCleanupJSON(w, []releaseAsset{})
			}
			return
		}
		if r.Method == http.MethodDelete {
			parts := strings.Split(r.URL.Path, "/")
			id, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
			expectedPath := fmt.Sprintf("%s/%d/assets/%d", prefix, id, id)
			if provider == releaseGitHub {
				expectedPath = fmt.Sprintf("%s/assets/%d", prefix, id)
			}
			if r.URL.Path != expectedPath {
				http.NotFound(w, r)
				return
			}
			recorder.deletedIDs = append(recorder.deletedIDs, id)
			if failures[fmt.Sprintf("delete/%d", id)] {
				http.Error(w, "asset deletion failed", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(recorder.server.Close)
	return recorder
}

func writeCleanupJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
