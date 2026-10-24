package flakerelease

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/module"
)

var (
	goProxyURL  = "https://proxy.golang.org"
	goSumDBURL  = "https://sum.golang.org"
	pkgGoDevURL = "https://pkg.go.dev"
	// goProxyAttempts bounds how many times each module proxy request is tried, since
	// the proxy may not see a newly pushed tag right away.
	goProxyAttempts      = 5
	goProxyRetryInterval = 5 * time.Second
	pkgGoDevPollInterval = 10 * time.Second
	// pkgGoDevTimeout bounds how long to wait for pkg.go.dev to serve the version.
	pkgGoDevTimeout = 10 * time.Minute
	pkgGoDevClient  = &http.Client{Timeout: time.Minute}
)

// publishPkgGoDevPackage publishes a Go module version, which exists once its tag is
// pushed, by fetching it through the public module proxy and checksum database.
// pkg.go.dev only picks new versions up from index.golang.org eventually, which can
// take hours, so the version is then requested from pkg.go.dev directly.
func publishPkgGoDevPackage(publication *packagePublication) error {
	escapedPath, err := module.EscapePath(publication.name)
	if err != nil {
		return err
	}
	escapedVersion, err := module.EscapeVersion(publication.version)
	if err != nil {
		return err
	}

	base := strings.TrimRight(goProxyURL, "/") + "/" + escapedPath + "/@v/" + escapedVersion
	for _, suffix := range []string{".info", ".mod", ".zip"} {
		if err := goProxyFetch(base + suffix); err != nil {
			return err
		}
	}
	// The checksum database records the module when it is first looked up, but the
	// proxy already serves it, so a failed lookup does not fail publication.
	lookup := strings.TrimRight(goSumDBURL, "/") + "/lookup/" + escapedPath + "@" + escapedVersion
	if err := goProxyGet(lookup); err != nil {
		itemWarn("checksum database lookup failed: %v", err)
	}

	return requestPkgGoDev(publication.name + "@" + publication.version)
}

// goProxyFetch requests url from the module proxy, retrying failures.
func goProxyFetch(url string) error {
	detail("GET %s", url)
	var err error
	for attempt := 1; attempt <= goProxyAttempts; attempt++ {
		if err = goProxyGet(url); err == nil {
			return nil
		}
		if attempt < goProxyAttempts {
			status("retrying: %v", err)
			time.Sleep(goProxyRetryInterval)
		}
	}
	return err
}

// goProxyGet requests url and discards the response, which may be a large module zip.
func goProxyGet(url string) error {
	resp, err := pkgGoDevClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if message := strings.TrimSpace(string(body)); message != "" {
			return fmt.Errorf("GET %s failed: %s: %s", url, resp.Status, message)
		}
		return fmt.Errorf("GET %s failed: %s", url, resp.Status)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

// requestPkgGoDev requests a module version from pkg.go.dev the way its "Request"
// button does, and waits until it is served. The fetch request answers 408 while
// processing continues, so its response is ignored.
func requestPkgGoDev(moduleVersion string) error {
	base := strings.TrimRight(pkgGoDevURL, "/")
	page := base + "/" + moduleVersion
	deadline := time.Now().Add(pkgGoDevTimeout)
	for {
		if resp, err := pkgGoDevClient.Get(page); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				status("served at %s", page)
				return nil
			}
		}
		if time.Now().After(deadline) {
			break
		}
		detail("POST %s/fetch/%s", base, moduleVersion)
		if resp, err := pkgGoDevClient.Post(base+"/fetch/"+moduleVersion, "", nil); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		time.Sleep(pkgGoDevPollInterval)
	}

	// The module proxy already serves the version, so pkg.go.dev will list it eventually.
	itemWarn("pkg.go.dev did not serve %s yet; request it at %s", moduleVersion, page)
	return nil
}
