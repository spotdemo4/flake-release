package flakerelease

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/module"
)

var (
	cratesIOIndexURL = "https://index.crates.io/"
	pyPISimpleURL    = "https://pypi.org/simple/"
	// registryLookupClient looks up versions in registries that have no client of their own.
	registryLookupClient = &http.Client{Timeout: time.Minute}
)

// registryLookupLimit bounds a lookup's response, which for npm is the package's whole
// version history.
const registryLookupLimit = 64 << 20

// pyPISimpleFile matches the file name in a link of a PyPI simple index page.
var pyPISimpleFile = regexp.MustCompile(`(?is)<a\s[^>]*>\s*([^<]+?)\s*</a>`)

// registryVersion is what a registry already has of a publication's version.
type registryVersion struct {
	served bool
	// staged is hex.pm's staged release of the version, which it holds until a
	// package owner approves it.
	staged *hexPMStagedRelease
	// remaining are the PyPI distributions the registry lacks, when it already has
	// some of them.
	remaining []string
}

// checkRegistries asks each publication's registry whether it already has the version,
// so a version published by an earlier run is neither verified nor published again.
// Publications that only know their identity once verification builds them are checked
// just before they publish.
func (set *packagePublicationSet) checkRegistries() error {
	if set == nil || !slices.ContainsFunc(set.packages, func(publication packagePublication) bool { return publication.name != "" }) {
		return nil
	}
	section("Checking package registries")
	for index := range set.packages {
		publication := &set.packages[index]
		if publication.name == "" || publication.checked {
			continue
		}
		item("%s %s@%s on %s", publication.kind, publication.name, publication.version, packageRegistryName(publication.registry, set.provider))
		if err := set.checkRegistry(publication); err != nil {
			return err
		}
	}
	return nil
}

// checkRegistry asks the publication's registry whether it already has the version. A
// lookup that fails only warns, so the version is published, and a registry that
// refuses it as a duplicate then fails publication.
func (set *packagePublicationSet) checkRegistry(publication *packagePublication) error {
	publication.checked = true
	registry := packageRegistryName(publication.registry, set.provider)
	existing, err := publication.lookup(set)
	if err != nil {
		if set.cfg.dryRun {
			status("can't check whether %s already has it: %v", registry, err)
		} else {
			itemWarn("can't check whether %s already has it, so it is published: %v", registry, err)
		}
		return nil
	}
	if staged := existing.staged; staged != nil {
		if err := staged.err(publication.name, publication.version); err != nil {
			return err
		}
		if staged.Status == "approved" {
			existing.served = true
		} else {
			status("hex.pm holds it as a staged release waiting for approval, so it is not published again")
			set.stage(publication, staged)
			return nil
		}
	}
	switch {
	case existing.served:
		status("%s already has it, so it is not published again", registry)
		publication.served = true
	case len(existing.remaining) > 0:
		var uploaded []string
		for _, artifact := range publication.artifacts {
			if !slices.Contains(existing.remaining, artifact) {
				uploaded = append(uploaded, filepath.Base(artifact))
			}
		}
		status("%s already has %s, so only the other distributions are uploaded", registry, strings.Join(uploaded, ", "))
		publication.artifacts = existing.remaining
	default:
		status("%s does not have it yet", registry)
	}
	return nil
}

// stage records that hex.pm holds the publication as a staged release, which dependents
// in the run wait for until HEXPM_STAGED_TIMEOUT from now.
func (set *packagePublicationSet) stage(publication *packagePublication, staged *hexPMStagedRelease) {
	publication.staged = staged
	publication.approvalDeadline = time.Now().Add(set.cfg.hexPMStagedTimeout)
}

// lookup returns what the publication's registry already has of its version.
func (publication *packagePublication) lookup(set *packagePublicationSet) (registryVersion, error) {
	name, version := publication.name, publication.version
	switch publication.registry {
	case registryHexPM:
		return lookupHexPMRelease(set.cfg.hexPMToken, name, version)
	case registryPkgGoDev:
		return lookupPkgGoDev(name, version)
	case registryMavenCentral:
		return lookupMavenCentral(set.cfg.mavenCentralUsername, set.cfg.mavenCentralToken, name, version)
	case registryCratesIO:
		return lookupCargoIndex(cratesIOIndexURL, "", name, version)
	case registryNPMJS:
		registryURL, token := set.npmRegistry(registryNPMJS)
		return lookupNPM(registryURL, token, name, version)
	case registryPyPI:
		return lookupPyPI(pyPISimpleURL, "", "", name, publication.artifacts)
	case registryHost:
	default:
		return registryVersion{}, fmt.Errorf("unsupported registry %q", publication.registry)
	}

	token := set.cfg.packageRegistryToken
	switch publication.kind {
	case packageCargo:
		return lookupCargoIndex(set.registryURL(packageCargo), token, name, version)
	case packageGo:
		return lookupGoProxy(set.registryURL(packageGo), token, name, version)
	case packageNPM:
		registryURL, token := set.npmRegistry(registryHost)
		return lookupNPM(registryURL, token, name, version)
	case packagePyPI:
		registryURL, username, token := set.pyPIRegistry(registryHost)
		return lookupPyPI(registryURL+"/simple/", username, token, name, publication.artifacts)
	case packageMaven, packageGradle:
		username := firstNonEmpty(set.cfg.packageRegistryUsername, set.cfg.packageRegistryOwner)
		return lookupMavenRepository(set.registryURL(publication.kind), username, token, name, version)
	default:
		return registryVersion{}, fmt.Errorf("unsupported package kind %q", publication.kind)
	}
}

// lookupCargoIndex looks the crate version up in the sparse index at indexURL.
func lookupCargoIndex(indexURL string, token string, name string, version string) (registryVersion, error) {
	lookup := registryLookup{url: strings.TrimRight(indexURL, "/") + "/" + cargoIndexPath(name), accept: "*/*", secrets: []string{token}}
	if token != "" {
		lookup.authorization = "Bearer " + token
	}
	body, found, err := lookup.find()
	if err != nil || !found {
		return registryVersion{}, err
	}
	// Versions differing only in build metadata are the same version to a registry.
	want, _, _ := strings.Cut(version, "+")
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(nil, len(body)+1)
	for scanner.Scan() {
		var entry struct {
			Version string `json:"vers"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		if have, _, _ := strings.Cut(entry.Version, "+"); have == want {
			return registryVersion{served: true}, nil
		}
	}
	return registryVersion{}, scanner.Err()
}

// cargoIndexPath returns the path of a crate's file in a Cargo registry index.
func cargoIndexPath(name string) string {
	name = strings.ToLower(name)
	switch len(name) {
	case 1, 2:
		return fmt.Sprintf("%d/%s", len(name), name)
	case 3:
		return "3/" + name[:1] + "/" + name
	default:
		return name[:2] + "/" + name[2:4] + "/" + name
	}
}

// lookupNPM looks the package version up in the package's metadata in the npm registry
// at registryURL.
func lookupNPM(registryURL string, token string, name string, version string) (registryVersion, error) {
	lookup := registryLookup{
		url: strings.TrimRight(registryURL, "/") + "/" + url.PathEscape(name),
		// The abbreviated metadata that installs use is enough, and much smaller.
		accept:  "application/vnd.npm.install-v1+json; q=1.0, application/json; q=0.8",
		secrets: []string{token},
	}
	if token != "" {
		lookup.authorization = "Bearer " + token
	}
	body, found, err := lookup.find()
	if err != nil || !found {
		return registryVersion{}, err
	}
	var packument struct {
		Versions map[string]json.RawMessage `json:"versions"`
	}
	if err := json.Unmarshal(body, &packument); err != nil {
		return registryVersion{}, fmt.Errorf("parsing npm package metadata: %w", err)
	}
	_, served := packument.Versions[version]
	return registryVersion{served: served}, nil
}

// lookupPyPI looks the distributions up in the project's page of the simple index at
// indexURL, since a version can gain distributions after it is first uploaded.
func lookupPyPI(indexURL string, username string, password string, name string, artifacts []string) (registryVersion, error) {
	if len(artifacts) == 0 {
		return registryVersion{}, errors.New("its distributions were not built")
	}
	lookup := registryLookup{
		url:      strings.TrimRight(indexURL, "/") + "/" + url.PathEscape(normalizePyPIName(name)),
		accept:   "text/html",
		username: username,
		password: password,
		secrets:  []string{password},
	}
	body, found, err := lookup.find()
	if err != nil || !found {
		return registryVersion{}, err
	}
	files := map[string]bool{}
	for _, match := range pyPISimpleFile.FindAllSubmatch(body, -1) {
		files[html.UnescapeString(string(match[1]))] = true
	}
	var remaining []string
	for _, artifact := range artifacts {
		if !files[filepath.Base(artifact)] {
			remaining = append(remaining, artifact)
		}
	}
	switch len(remaining) {
	case 0:
		return registryVersion{served: true}, nil
	case len(artifacts):
		return registryVersion{}, nil
	default:
		return registryVersion{remaining: remaining}, nil
	}
}

// lookupGoProxy looks the module version up in the Go module proxy at proxyURL.
func lookupGoProxy(proxyURL string, token string, modulePath string, version string) (registryVersion, error) {
	escapedPath, err := module.EscapePath(modulePath)
	if err != nil {
		return registryVersion{}, err
	}
	escapedVersion, err := module.EscapeVersion(version)
	if err != nil {
		return registryVersion{}, err
	}
	lookup := registryLookup{url: strings.TrimRight(proxyURL, "/") + "/" + escapedPath + "/@v/" + escapedVersion + ".info", secrets: []string{token}}
	if token != "" {
		lookup.authorization = tokenAuthScheme + " " + token
	}
	_, found, err := lookup.find()
	return registryVersion{served: found}, err
}

// lookupPkgGoDev looks the module version up on pkg.go.dev, which serves a page for each
// version it lists.
func lookupPkgGoDev(modulePath string, version string) (registryVersion, error) {
	lookup := registryLookup{url: strings.TrimRight(pkgGoDevURL, "/") + "/" + modulePath + "@" + version, accept: "text/html", client: pkgGoDevClient}
	_, found, err := lookup.find()
	return registryVersion{served: found}, err
}

// lookupMavenRepository looks the version's POM up in the Maven repository at
// repositoryURL. A Gradle build is looked up by its root project's coordinates.
func lookupMavenRepository(repositoryURL string, username string, password string, name string, version string) (registryVersion, error) {
	groupID, artifactID, ok := strings.Cut(name, ":")
	if !ok {
		return registryVersion{}, fmt.Errorf("maven package %q has no group ID", name)
	}
	path := strings.ReplaceAll(groupID, ".", "/") + "/" + artifactID + "/" + version + "/" + artifactID + "-" + version + ".pom"
	lookup := registryLookup{url: strings.TrimRight(repositoryURL, "/") + "/" + path, accept: "*/*", secrets: []string{password}}
	if password != "" {
		lookup.username, lookup.password = username, password
	}
	_, found, err := lookup.find()
	return registryVersion{served: found}, err
}

// lookupMavenCentral asks the Central Portal whether it has published the component.
func lookupMavenCentral(username string, password string, name string, version string) (registryVersion, error) {
	groupID, artifactID, ok := strings.Cut(name, ":")
	if !ok {
		return registryVersion{}, fmt.Errorf("maven package %q has no group ID", name)
	}
	if username == "" || password == "" {
		return registryVersion{}, errors.New("the Central Portal needs MAVEN_CENTRAL_USERNAME and MAVEN_CENTRAL_TOKEN to look it up")
	}
	token := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	query := url.Values{"namespace": {groupID}, "name": {artifactID}, "version": {version}}
	body, found, err := registryLookup{
		url:           strings.TrimRight(mavenCentralURL, "/") + "/api/v1/publisher/published?" + query.Encode(),
		authorization: "Bearer " + token,
		secrets:       []string{token, password},
	}.find()
	if err != nil || !found {
		return registryVersion{}, err
	}
	var published struct {
		Published bool `json:"published"`
	}
	if err := json.Unmarshal(body, &published); err != nil {
		return registryVersion{}, fmt.Errorf("parsing maven central publication status: %w", err)
	}
	return registryVersion{served: published.Published}, nil
}

// registryLookup is a GET request that looks a package version up in a registry.
type registryLookup struct {
	url    string
	accept string
	// authorization is the Authorization header, unless username or password is set
	// for Basic authentication.
	authorization string
	username      string
	password      string
	// secrets are redacted from error messages.
	secrets []string
	client  *http.Client
}

// get sends the request and returns the response's status code and body.
func (lookup registryLookup) get() (int, []byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, lookup.url, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", firstNonEmpty(lookup.accept, jsonAccept))
	req.Header.Set("User-Agent", "flake-release/"+currentVersion())
	switch {
	case lookup.username != "" || lookup.password != "":
		req.SetBasicAuth(lookup.username, lookup.password)
	case lookup.authorization != "":
		req.Header.Set("Authorization", lookup.authorization)
	}
	client := lookup.client
	if client == nil {
		client = registryLookupClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, registryLookupLimit))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

// find sends the request and returns the body of a successful response. A missing
// resource is not found, and any other unsuccessful response is an error.
func (lookup registryLookup) find() ([]byte, bool, error) {
	detail("GET %s", lookup.url)
	code, body, err := lookup.get()
	switch {
	case err != nil:
		return nil, false, err
	case code >= 200 && code < 300:
		return body, true, nil
	case code == http.StatusNotFound || code == http.StatusGone:
		return nil, false, nil
	default:
		return nil, false, lookupStatusError(lookup.url, code, body, lookup.secrets...)
	}
}

// lookupStatusError describes an unsuccessful registry response, with the message of a
// JSON body when it has one.
func lookupStatusError(endpoint string, code int, body []byte, secrets ...string) error {
	text := fmt.Sprintf("%d %s", code, http.StatusText(code))
	var response struct {
		Message string `json:"message"`
	}
	message := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &response) == nil && response.Message != "" {
		message = response.Message
	}
	// An HTML error page says nothing a status code doesn't.
	if strings.HasPrefix(message, "<") || len(message) > 500 {
		message = ""
	}
	message = redactSecrets(message, secrets)
	if message != "" {
		return fmt.Errorf("GET %s failed: %s: %s", endpoint, text, message)
	}
	return fmt.Errorf("GET %s failed: %s", endpoint, text)
}
