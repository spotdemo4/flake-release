package flakerelease

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// registryServer is a fake registry that serves bodies by request URI and records each
// request with its Authorization header.
type registryServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string
}

func (server *registryServer) recorded() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return slices.Clone(server.requests)
}

// useRegistryServer serves each body in bodies at its request URI, and 404 otherwise.
func useRegistryServer(t *testing.T, bodies map[string]string) *registryServer {
	t.Helper()
	recorder := &registryServer{}
	recorder.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.mu.Lock()
		recorder.requests = append(recorder.requests, strings.TrimSpace(r.URL.RequestURI()+" "+r.Header.Get("Authorization")))
		recorder.mu.Unlock()
		body, ok := bodies[r.URL.RequestURI()]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.HasPrefix(body, "status:") {
			code := map[string]int{"status:401": http.StatusUnauthorized, "status:500": http.StatusInternalServerError}[body]
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"message":"lookup failed"}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(recorder.Close)
	return recorder
}

// hostLookupSet is a Forgejo package publication set whose registry is server.
func hostLookupSet(server *registryServer, publication packagePublication) *packagePublicationSet {
	return &packagePublicationSet{
		cfg: config{
			packageRegistryOwner:    "owner",
			packageRegistryURL:      server.URL,
			packageRegistryToken:    "registry-token",
			packageRegistryUsername: "user",
		},
		provider: releaseForgejo,
		packages: []packagePublication{publication},
	}
}

func basicAuth(username string, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

func TestCargoIndexPath(t *testing.T) {
	for name, want := range map[string]string{
		"a":     "1/a",
		"ab":    "2/ab",
		"abc":   "3/a/abc",
		"Serde": "se/rd/serde",
	} {
		if got := cargoIndexPath(name); got != want {
			t.Errorf("cargoIndexPath(%q) = %q; want %q", name, got, want)
		}
	}
}

func TestLookupFindsVersionsInEachRegistry(t *testing.T) {
	server := useRegistryServer(t, map[string]string{
		"/api/packages/owner/cargo/ex/am/example":                                  "{\"name\":\"example\",\"vers\":\"1.2.2\"}\n{\"name\":\"example\",\"vers\":\"1.2.3+build.1\"}\n",
		"/api/packages/owner/npm/@scope%2Fexample":                                 `{"versions":{"1.2.3":{}}}`,
		"/api/packages/owner/go/example.com/!repo/@v/v1.2.3.info":                  `{"Version":"v1.2.3"}`,
		"/api/packages/owner/maven/com/example/app/1.2.3/app-1.2.3.pom":            "<project/>",
		"/api/packages/owner/pypi/simple/my-project":                               `<a href="/files/my-project-1.2.3.tar.gz#sha256=1">my_project-1.2.3.tar.gz</a><br>`,
		"/api/v1/publisher/published?name=app&namespace=com.example&version=1.2.3": `{"published":true}`,
	})
	previous := mavenCentralURL
	mavenCentralURL = server.URL
	t.Cleanup(func() { mavenCentralURL = previous })

	for _, test := range []struct {
		publication packagePublication
		auth        string
	}{
		{packagePublication{kind: packageCargo, name: "example", version: "1.2.3"}, "Bearer registry-token"},
		{packagePublication{kind: packageNPM, name: "@scope/example", version: "1.2.3"}, "Bearer registry-token"},
		{packagePublication{kind: packageGo, name: "example.com/Repo", version: "v1.2.3"}, "token registry-token"},
		{packagePublication{kind: packageMaven, name: "com.example:app", version: "1.2.3"}, basicAuth("user", "registry-token")},
		{packagePublication{kind: packageGradle, name: "com.example:app", version: "1.2.3"}, basicAuth("user", "registry-token")},
		{packagePublication{kind: packagePyPI, name: "My.Project", version: "1.2.3", artifacts: []string{"/tmp/dist/my_project-1.2.3.tar.gz"}}, basicAuth("user", "registry-token")},
		{packagePublication{kind: packageGradle, registry: registryMavenCentral, name: "com.example:app", version: "1.2.3"}, "Bearer " + base64.StdEncoding.EncodeToString([]byte("central-user:central-token"))},
	} {
		t.Run(string(test.publication.registry)+"/"+string(test.publication.kind), func(t *testing.T) {
			set := hostLookupSet(server, test.publication)
			set.cfg.mavenCentralUsername, set.cfg.mavenCentralToken = "central-user", "central-token"
			before := len(server.recorded())
			existing, err := test.publication.lookup(set)
			if err != nil {
				t.Fatal(err)
			}
			if !existing.served {
				t.Fatalf("lookup() = %#v; want served", existing)
			}
			requests := server.recorded()[before:]
			if len(requests) != 1 || !strings.HasSuffix(requests[0], " "+test.auth) {
				t.Fatalf("requests = %q; want one with %q", requests, test.auth)
			}

			missing := test.publication
			missing.version = "9.9.9"
			missing.artifacts = []string{"/tmp/dist/my_project-9.9.9.tar.gz"}
			if existing, err := missing.lookup(set); err != nil || existing.served || existing.remaining != nil {
				t.Fatalf("lookup() of another version = %#v, %v; want not served", existing, err)
			}
		})
	}
}

func TestLookupFindsVersionsInPublicRegistries(t *testing.T) {
	server := useRegistryServer(t, map[string]string{
		"/ex/am/example":        `{"name":"example","vers":"1.2.3"}`,
		"/@scope%2Fexample":     `{"versions":{"1.2.3":{}}}`,
		"/my-project":           `<a href="https://files/1">my_project-1.2.3-py3-none-any.whl</a>`,
		"/example.com/m@v1.2.3": "<html></html>",
	})
	previous := []string{cratesIOIndexURL, npmjsRegistryURL, pyPISimpleURL, pkgGoDevURL}
	cratesIOIndexURL, npmjsRegistryURL, pyPISimpleURL, pkgGoDevURL = server.URL+"/", server.URL+"/", server.URL+"/", server.URL
	t.Cleanup(func() {
		cratesIOIndexURL, npmjsRegistryURL, pyPISimpleURL, pkgGoDevURL = previous[0], previous[1], previous[2], previous[3]
	})

	set := &packagePublicationSet{cfg: config{npmjsToken: "npm-token"}}
	for _, publication := range []packagePublication{
		{kind: packageCargo, registry: registryCratesIO, name: "example", version: "1.2.3"},
		{kind: packageNPM, registry: registryNPMJS, name: "@scope/example", version: "1.2.3"},
		{kind: packagePyPI, registry: registryPyPI, name: "my_project", version: "1.2.3", artifacts: []string{"dist/my_project-1.2.3-py3-none-any.whl"}},
		{kind: packageGo, registry: registryPkgGoDev, name: "example.com/m", version: "v1.2.3"},
	} {
		existing, err := publication.lookup(set)
		if err != nil || !existing.served {
			t.Fatalf("%s lookup() = %#v, %v; want served", publication.registry, existing, err)
		}
	}
	if requests := server.recorded(); !slices.Contains(requests, "/@scope%2Fexample Bearer npm-token") || !slices.Contains(requests, "/ex/am/example") {
		t.Fatalf("requests = %q; want npmjs.com with its token and crates.io without one", requests)
	}
}

func TestLookupPyPIReportsRemainingDistributions(t *testing.T) {
	server := useRegistryServer(t, map[string]string{
		"/api/packages/owner/pypi/simple/example": `<a href="/1">example-1.2.3.tar.gz</a><br><a href="/2">example-1.2.2-py3-none-any.whl</a>`,
	})
	sdist, wheel := filepath.Join("dist", "example-1.2.3.tar.gz"), filepath.Join("dist", "example-1.2.3-py3-none-any.whl")
	publication := packagePublication{kind: packagePyPI, name: "example", version: "1.2.3", artifacts: []string{sdist, wheel}}
	set := hostLookupSet(server, publication)
	if err := set.checkRegistries(); err != nil {
		t.Fatal(err)
	}
	if got := set.packages[0]; got.served || !slices.Equal(got.artifacts, []string{wheel}) {
		t.Fatalf("publication = served %t with artifacts %q; want only the wheel left to upload", got.served, got.artifacts)
	}
}

func TestCheckRegistryWarnsWhenLookupFails(t *testing.T) {
	output := captureHumanOutput(t)
	server := useRegistryServer(t, map[string]string{"/api/packages/owner/npm/example": "status:500"})
	set := hostLookupSet(server, packagePublication{kind: packageNPM, name: "example", version: "1.2.3"})
	if err := set.checkRegistries(); err != nil {
		t.Fatal(err)
	}
	if publication := set.packages[0]; !publication.checked || publication.served {
		t.Fatalf("publication = %#v; want checked and published", publication)
	}
	if want := "warning: can't check whether Forgejo already has it, so it is published: GET " + server.URL + "/api/packages/owner/npm/example failed: 500 Internal Server Error: lookup failed"; !strings.Contains(output.String(), want) {
		t.Fatalf("output = %q; want %q", output.String(), want)
	}
}

func TestLookupMavenCentralNeedsCredentials(t *testing.T) {
	_, err := lookupMavenCentral("", "", "com.example:app", "1.2.3")
	if err == nil || !strings.Contains(err.Error(), "MAVEN_CENTRAL_USERNAME") {
		t.Fatalf("error = %v; want missing credentials", err)
	}
}

func TestAlreadyPublishedVersionIsNotVerifiedOrPublished(t *testing.T) {
	output := captureHumanOutput(t)
	server := useRegistryServer(t, map[string]string{"/api/packages/owner/npm/example": `{"versions":{"1.2.3":{}}}`})
	var commands []commandOptions
	set := hostLookupSet(server, packagePublication{kind: packageNPM, name: "example", manifestName: "example", version: "1.2.3"})
	set.releaseTag = parseReleaseTag("v1.2.3")
	set.commands = fakePackageCommandRunner{
		requireFunc: func(string) error { return nil },
		runFunc: func(options commandOptions) error {
			commands = append(commands, options)
			return nil
		},
	}
	if err := set.checkRegistries(); err != nil {
		t.Fatal(err)
	}
	if err := set.verify(nil); err != nil {
		t.Fatal(err)
	}
	if err := set.publish(); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 0 {
		t.Fatalf("commands = %v; want no dry run or publish", commands)
	}
	for _, text := range []string{"Forgejo already has it, so it is not published again", "Forgejo already has it; skipping verification", "already published; skipping publish"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("output = %q; want %q", output.String(), text)
		}
	}
	if links := set.releaseLinks(); len(links) != 1 {
		t.Fatalf("links = %#v; want the version already published", links)
	}
}

func TestParseStagedTimeout(t *testing.T) {
	for value, want := range map[string]string{"": "0s", "0": "0s", "30m": "30m0s", " 1h ": "1h0m0s"} {
		got, err := parseStagedTimeout(value)
		if err != nil || got.String() != want {
			t.Errorf("parseStagedTimeout(%q) = %v, %v; want %s", value, got, err, want)
		}
	}
	for _, value := range []string{"30", "-1m", "soon"} {
		if _, err := parseStagedTimeout(value); err == nil || !strings.Contains(err.Error(), "HEXPM_STAGED_TIMEOUT") {
			t.Errorf("parseStagedTimeout(%q) error = %v; want invalid", value, err)
		}
	}
}
