package flakerelease

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPkgGoDevPublishingEnablesGoTarget(t *testing.T) {
	if targets := publicPackageTargets(config{}); len(targets) != 0 {
		t.Fatalf("targets = %#v; want none without PKG_GO_DEV_PUBLISHING", targets)
	}
	targets, _, err := selectPackageTargets(config{pkgGoDevPublishing: true}, releaseGitHub)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].registry != registryPkgGoDev || !slices.Equal(targets[0].kinds, []packageKind{packageGo}) {
		t.Fatalf("targets = %#v; want Go modules for pkg.go.dev", targets)
	}
	if name := packageRegistryName(registryPkgGoDev, releaseGitHub); name != "pkg.go.dev" {
		t.Fatalf("registry name = %q", name)
	}
}

// pkgGoDevServer records requests to fake module proxy, checksum database, and
// pkg.go.dev servers, which are served under /proxy, /sumdb, and /pkgsite.
type pkgGoDevServer struct {
	mu       sync.Mutex
	requests []string
	handler  func(w http.ResponseWriter, r *http.Request)
}

func (server *pkgGoDevServer) recorded() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return slices.Clone(server.requests)
}

func usePkgGoDevServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *pkgGoDevServer {
	t.Helper()
	recorder := &pkgGoDevServer{handler: handler}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		recorder.requests = append(recorder.requests, r.Method+" "+r.URL.Path)
		recorder.handler(w, r)
	}))
	t.Cleanup(server.Close)

	previousProxy, previousSumDB, previousPkgGoDev := goProxyURL, goSumDBURL, pkgGoDevURL
	previousRetry, previousPoll, previousTimeout := goProxyRetryInterval, pkgGoDevPollInterval, pkgGoDevTimeout
	goProxyURL, goSumDBURL, pkgGoDevURL = server.URL+"/proxy/", server.URL+"/sumdb", server.URL+"/pkgsite"
	goProxyRetryInterval, pkgGoDevPollInterval, pkgGoDevTimeout = time.Millisecond, time.Millisecond, time.Second
	t.Cleanup(func() {
		goProxyURL, goSumDBURL, pkgGoDevURL = previousProxy, previousSumDB, previousPkgGoDev
		goProxyRetryInterval, pkgGoDevPollInterval, pkgGoDevTimeout = previousRetry, previousPoll, previousTimeout
	})
	return recorder
}

func TestPublishPkgGoDevFetchesProxyAndRequestsVersion(t *testing.T) {
	fetched := false
	server := usePkgGoDevServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/proxy/"), strings.HasPrefix(r.URL.Path, "/sumdb/"):
		case r.Method == http.MethodPost:
			fetched = true
			w.WriteHeader(http.StatusRequestTimeout)
		case !fetched:
			http.NotFound(w, r)
		}
	})
	publication := &packagePublication{kind: packageGo, registry: registryPkgGoDev, name: "example.com/Owner/repo/api/v2", version: "v2.0.0-RC.1"}
	if err := publication.publish(&packagePublicationSet{}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /proxy/example.com/!owner/repo/api/v2/@v/v2.0.0-!r!c.1.info",
		"GET /proxy/example.com/!owner/repo/api/v2/@v/v2.0.0-!r!c.1.mod",
		"GET /proxy/example.com/!owner/repo/api/v2/@v/v2.0.0-!r!c.1.zip",
		"GET /sumdb/lookup/example.com/!owner/repo/api/v2@v2.0.0-!r!c.1",
		"GET /pkgsite/example.com/Owner/repo/api/v2@v2.0.0-RC.1",
		"POST /pkgsite/fetch/example.com/Owner/repo/api/v2@v2.0.0-RC.1",
		"GET /pkgsite/example.com/Owner/repo/api/v2@v2.0.0-RC.1",
	}
	if got := server.recorded(); !slices.Equal(got, want) {
		t.Fatalf("requests = %q; want %q", got, want)
	}
}

func TestPublishPkgGoDevFailsWhenProxyCannotFetchModule(t *testing.T) {
	server := usePkgGoDevServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found: unknown revision v1.2.3", http.StatusNotFound)
	})
	publication := &packagePublication{kind: packageGo, registry: registryPkgGoDev, name: "example.com/owner/repo", version: "v1.2.3"}
	err := publication.publish(&packagePublicationSet{})
	if err == nil || !strings.Contains(err.Error(), "unknown revision v1.2.3") {
		t.Fatalf("error = %v; want the proxy's response", err)
	}
	if got := len(server.recorded()); got != goProxyAttempts {
		t.Fatalf("requests = %d; want %d attempts", got, goProxyAttempts)
	}
}

func TestPublishPkgGoDevToleratesChecksumAndPkgGoDevDelays(t *testing.T) {
	server := usePkgGoDevServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/proxy/"):
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusRequestTimeout)
		default:
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}
	})
	pkgGoDevTimeout = 20 * time.Millisecond
	publication := &packagePublication{kind: packageGo, registry: registryPkgGoDev, name: "example.com/owner/repo", version: "v1.2.3"}
	if err := publication.publish(&packagePublicationSet{}); err != nil {
		t.Fatalf("publication failed after the proxy served the module: %v", err)
	}
	if !slices.Contains(server.recorded(), "POST /pkgsite/fetch/example.com/owner/repo@v1.2.3") {
		t.Fatalf("requests = %q; want a pkg.go.dev fetch request", server.recorded())
	}
}
