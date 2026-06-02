package flakerelease

import (
	"net/http"
	"net/http/httptest"
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
