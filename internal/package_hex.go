package flakerelease

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	hexPMRepository = "hexpm"
	// rebar3HexConfig is a global rebar3 config that loads the rebar3_hex plugin from
	// hex.pm, since nixpkgs does not package it.
	rebar3HexConfig = "{plugins, [{rebar3_hex, \"~> 7.0\"}]}.\n"
	// gleamVersionZeroConfirmation is the text gleam publish asks to be typed before
	// publishing a 0.x version, even with --yes.
	gleamVersionZeroConfirmation = "I am not using semantic versioning"
	// gleamNotPublishing is printed when gleam publish declines without failing.
	gleamNotPublishing = "Not publishing."
)

var (
	hexPMAPIURL = "https://hex.pm/api"
	// hexPMReleaseAttempts bounds how many times a published release is looked up when
	// hex.pm neither serves nor stages it, since it may be slow to serve a new release.
	hexPMReleaseAttempts      = 6
	hexPMReleaseRetryInterval = 5 * time.Second
	// hexPMApprovalPollInterval is how often a staged release is looked up while a
	// dependent waits for its approval.
	hexPMApprovalPollInterval = 15 * time.Second
	hexPMClient               = &http.Client{Timeout: time.Minute}
	// hexPMStagedReleasesURL is the dashboard page listing staged releases to approve.
	hexPMStagedReleasesURL = "https://hex.pm/dashboard/staged-releases"
)

// hexMetadataField matches a top-level binary field of a Hex package's metadata.config.
var hexMetadataField = regexp.MustCompile(`(?m)^\{<<"(name|version)">>,\s*<<"([^"]*)"(?:/utf8)?>>\}\.`)

var (
	// rebar3Application matches the application name in a .app.src file.
	rebar3Application = regexp.MustCompile(`\{\s*application\s*,\s*'?([a-z][A-Za-z0-9_@]*)'?\s*,`)
	// rebar3PackageName matches the Hex package name that overrides the application name.
	rebar3PackageName = regexp.MustCompile(`\{\s*pkg_name\s*,\s*(?:<<)?["']?([A-Za-z0-9_]+)["']?(?:>>)?\s*\}`)
	// rebar3Version matches a literal application version in a .app.src file.
	rebar3Version = regexp.MustCompile(`\{\s*vsn\s*,\s*"([^"]*)"\s*\}`)
)

type gleamPackageInformation struct {
	Config struct {
		Name         string                      `json:"name"`
		Version      string                      `json:"version"`
		Dependencies map[string]gleamRequirement `json:"dependencies"`
	} `json:"gleam.toml"`
}

// gleamRequirement is a gleam.toml dependency: a Hex version range, a local path, or a
// git repository, optionally with a path within it.
type gleamRequirement struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	Git     string `json:"git"`
}

// preflightRebar3Package only requires rebar3, since the package's identity comes from
// the tarball that verification builds. Its name in the .app.src is read early, so
// releases that depend on it can be ordered after it.
func preflightRebar3Package(set *packagePublicationSet, publication *packagePublication) error {
	if err := set.commands.require("rebar3"); err != nil {
		return err
	}
	publication.manifestName = rebar3PackageNameFromSource(publication.dir)
	return nil
}

// rebar3PackageNameFromSource returns the Hex package name of the single application in
// dir's src directory, or nothing when it can't be read.
func rebar3PackageNameFromSource(dir string) string {
	files, err := filepath.Glob(filepath.Join(dir, "src", "*.app.src"))
	if err != nil || len(files) != 1 {
		return ""
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		return ""
	}
	if match := rebar3PackageName.FindSubmatch(data); match != nil {
		return string(match[1])
	}
	if match := rebar3Application.FindSubmatch(data); match != nil {
		return string(match[1])
	}
	return ""
}

// verifyRebar3Package builds the Hex package, reads its identity, and dry-runs its publication.
func verifyRebar3Package(set *packagePublicationSet, publication *packagePublication) error {
	configDir, err := set.rebar3ConfigDir()
	if err != nil {
		return err
	}
	outputDir, err := os.MkdirTemp(set.temporaryDir, "hex-")
	if err != nil {
		return err
	}
	// rebar3_hex writes into a tmp directory under --output without creating it.
	if err := os.Mkdir(filepath.Join(outputDir, "tmp"), 0o755); err != nil {
		return err
	}
	if err := set.commands.run(commandOptions{
		name: "rebar3",
		args: []string{"hex", "build", "package", "--repo", hexPMRepository, "--output", outputDir},
		dir:  publication.dir,
		env:  []string{"REBAR_GLOBAL_CONFIG_DIR=" + configDir},
	}); err != nil {
		return err
	}
	files, err := findFiles(outputDir)
	if err != nil {
		return err
	}
	var tarballs []string
	for _, file := range files {
		if strings.HasSuffix(file, ".tar") {
			tarballs = append(tarballs, file)
		}
	}
	if len(tarballs) != 1 {
		return fmt.Errorf("rebar3 hex build produced %d package tarballs; want 1", len(tarballs))
	}
	name, version, err := readHexTarballMetadata(tarballs[0])
	if err != nil {
		return fmt.Errorf("reading hex package %s: %w", filepath.Base(tarballs[0]), err)
	}
	if err := requireStrictPackageVersion(version); err != nil {
		return fmt.Errorf("rebar3 package: %w", err)
	}
	publication.name = name
	publication.version = version
	publication.manifestName = name
	return set.commands.run(rebar3PublishCommand(set, publication, configDir, true))
}

func publishRebar3Package(set *packagePublicationSet, publication *packagePublication) error {
	configDir, err := set.rebar3ConfigDir()
	if err != nil {
		return err
	}
	return set.commands.run(rebar3PublishCommand(set, publication, configDir, false))
}

// rebar3PublishCommand publishes the package and, when a doc provider is configured,
// its documentation.
func rebar3PublishCommand(set *packagePublicationSet, publication *packagePublication, configDir string, dryRun bool) commandOptions {
	args := []string{"hex", "publish", "--repo", hexPMRepository, "--yes"}
	if dryRun {
		args = append(args, "--dry-run")
	}
	env := []string{"REBAR_GLOBAL_CONFIG_DIR=" + configDir}
	if set.cfg.hexPMToken != "" {
		env = append(env, "HEX_API_KEY="+set.cfg.hexPMToken)
	}
	return commandOptions{
		name:    "rebar3",
		args:    args,
		dir:     publication.dir,
		env:     env,
		secrets: []string{set.cfg.hexPMToken},
	}
}

// rebar3ConfigDir returns a directory for REBAR_GLOBAL_CONFIG_DIR whose global config
// loads rebar3_hex, so projects need not list the plugin themselves.
func (set *packagePublicationSet) rebar3ConfigDir() (string, error) {
	dir := filepath.Join(set.temporaryDir, "rebar3")
	config := filepath.Join(dir, ".config", "rebar3", "rebar.config")
	if isFile(config) {
		return dir, nil
	}
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(config, []byte(rebar3HexConfig), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

// readHexTarballMetadata returns the package name and version from the metadata.config
// of a Hex package tarball.
func readHexTarballMetadata(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = file.Close() }()
	archive := tar.NewReader(file)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return "", "", fmt.Errorf("package contains no metadata.config")
		}
		if err != nil {
			return "", "", err
		}
		if header.Name != "metadata.config" {
			continue
		}
		data, err := io.ReadAll(archive)
		if err != nil {
			return "", "", err
		}
		fields := map[string]string{}
		for _, match := range hexMetadataField.FindAllStringSubmatch(string(data), -1) {
			fields[match[1]] = match[2]
		}
		if fields["name"] == "" || fields["version"] == "" {
			return "", "", fmt.Errorf("metadata.config has no name or version")
		}
		return fields["name"], fields["version"], nil
	}
}

func preflightGleamPackage(set *packagePublicationSet, publication *packagePublication) error {
	// gleam shells out to escript to build Erlang code.
	for _, command := range []string{"gleam", "escript"} {
		if err := set.commands.require(command); err != nil {
			return err
		}
	}
	output, err := set.commands.capture(commandOptions{
		name: "gleam",
		args: []string{"export", "package-information"},
		dir:  publication.dir,
	})
	if err != nil {
		return err
	}
	var information gleamPackageInformation
	if err := json.Unmarshal([]byte(output), &information); err != nil {
		return fmt.Errorf("parsing gleam package information: %w", err)
	}
	name, version := information.Config.Name, information.Config.Version
	if name == "" || version == "" {
		return fmt.Errorf("gleam.toml requires name and version")
	}
	// gleam publish asks for the core team password for these names, which can't be
	// answered without a terminal.
	if strings.HasPrefix(name, "gleam_") && !strings.HasPrefix(name, "gleam_community_") {
		return fmt.Errorf("gleam package %s uses the gleam_ prefix reserved for the Gleam core team", name)
	}
	if err := requireStrictPackageVersion(version); err != nil {
		return fmt.Errorf("gleam package: %w", err)
	}
	publication.name = name
	publication.version = version
	publication.dependencies = nil
	publication.pathDependencies = nil
	// Development dependencies are not published.
	for _, dependency := range slices.Sorted(maps.Keys(information.Config.Dependencies)) {
		publication.dependencies = append(publication.dependencies, dependency)
		requirement := information.Config.Dependencies[dependency]
		if requirement.Path != "" && requirement.Git == "" {
			if publication.pathDependencies == nil {
				publication.pathDependencies = map[string]string{}
			}
			publication.pathDependencies[dependency] = requirement.Path
		}
	}
	return nil
}

// gleamPathDependencyVersion returns the version of the Gleam or rebar3 package in dir:
// the version in its gleam.toml, or the vsn in the .app.src of its single application.
func gleamPathDependencyVersion(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "gleam.toml"))
	switch {
	case err == nil:
		var config struct {
			Version string `toml:"version"`
		}
		if err := toml.Unmarshal(data, &config); err != nil {
			return "", fmt.Errorf("parsing gleam.toml: %w", err)
		}
		if config.Version == "" {
			return "", errors.New("gleam.toml has no version")
		}
		return config.Version, nil
	case !errors.Is(err, os.ErrNotExist):
		return "", err
	}

	files, err := filepath.Glob(filepath.Join(dir, "src", "*.app.src"))
	if err != nil {
		return "", err
	}
	if len(files) != 1 {
		return "", fmt.Errorf("found no gleam.toml and %d .app.src files in %s; want 1", len(files), dir)
	}
	data, err = os.ReadFile(files[0])
	if err != nil {
		return "", err
	}
	match := rebar3Version.FindSubmatch(data)
	if match == nil || len(match[1]) == 0 {
		return "", fmt.Errorf("%s has no literal vsn", filepath.Base(files[0]))
	}
	return string(match[1]), nil
}

// rewriteGleamPathDependencies replaces the path dependencies in the staged gleam.toml
// with Hex requirements from the given versions up to the next major version, which
// gleam publish accepts. Their local entries are removed from manifest.toml, so Gleam
// resolves them from Hex.
func rewriteGleamPathDependencies(publication *packagePublication, versions map[string]string) error {
	data, err := os.ReadFile(publication.manifest)
	if err != nil {
		return err
	}
	var config map[string]any
	if err := toml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parsing gleam.toml: %w", err)
	}
	dependencies, ok := config["dependencies"].(map[string]any)
	if !ok {
		return fmt.Errorf("gleam.toml has no dependencies table")
	}
	for _, name := range slices.Sorted(maps.Keys(versions)) {
		if _, ok := dependencies[name]; !ok {
			return fmt.Errorf("gleam.toml has no dependency %s", name)
		}
		requirement, err := gleamHexRequirement(versions[name])
		if err != nil {
			return err
		}
		status("replacing path dependency %s with %q", name, requirement)
		dependencies[name] = requirement
	}
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(config); err != nil {
		return fmt.Errorf("writing gleam.toml: %w", err)
	}
	if err := os.WriteFile(publication.manifest, encoded.Bytes(), 0o644); err != nil {
		return err
	}
	return removeGleamLocalPackages(filepath.Join(filepath.Dir(publication.manifest), "manifest.toml"), versions)
}

// gleamManifestPackage matches a package entry of a Gleam manifest.toml, which Gleam
// writes one per line.
var gleamManifestPackage = regexp.MustCompile(`^\s*\{\s*name\s*=\s*"([^"]+)".*\bsource\s*=\s*"local".*\},?\s*$`)

// removeGleamLocalPackages removes the local package entries named in names from a Gleam
// manifest.toml, when there is one.
func removeGleamLocalPackages(path string, names map[string]string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	lines := strings.SplitAfter(string(data), "\n")
	kept := lines[:0]
	for _, line := range lines {
		if match := gleamManifestPackage.FindStringSubmatch(line); match != nil {
			if _, ok := names[match[1]]; ok {
				continue
			}
		}
		kept = append(kept, line)
	}
	return os.WriteFile(path, []byte(strings.Join(kept, "")), 0o644)
}

// gleamHexRequirement returns a Hex requirement from version up to the next major version.
func gleamHexRequirement(version string) (string, error) {
	if err := requireStrictPackageVersion(version); err != nil {
		return "", err
	}
	// Hex requirements can't have build metadata.
	version, _, _ = strings.Cut(version, "+")
	major, _, _ := strings.Cut(version, ".")
	next, err := strconv.Atoi(major)
	if err != nil {
		return "", fmt.Errorf("version %q has an invalid major version", version)
	}
	return fmt.Sprintf(">= %s and < %d.0.0", version, next+1), nil
}

// verifyGleamPackage builds the Hex tarball, which checks the package metadata,
// dependencies, and code.
func verifyGleamPackage(set *packagePublicationSet, publication *packagePublication) error {
	return set.commands.run(commandOptions{
		name: "gleam",
		args: []string{"export", "hex-tarball"},
		dir:  publication.dir,
	})
}

func publishGleamPackage(set *packagePublicationSet, publication *packagePublication) error {
	output, err := set.commands.capture(gleamPublishCommand(set, publication))
	if err != nil {
		return err
	}
	if strings.Contains(output, gleamNotPublishing) {
		return fmt.Errorf("gleam publish declined to publish the package")
	}
	return nil
}

// gleamPublishCommand publishes the package and its documentation.
func gleamPublishCommand(set *packagePublicationSet, publication *packagePublication) commandOptions {
	command := commandOptions{
		name:    "gleam",
		args:    []string{"publish", "--yes"},
		dir:     publication.dir,
		env:     []string{"HEXPM_API_KEY=" + set.cfg.hexPMToken},
		secrets: []string{set.cfg.hexPMToken},
	}
	if strings.HasPrefix(publication.version, "0.") {
		command.stdin = gleamVersionZeroConfirmation + "\n"
	}
	return command
}

// hexPMStagedRelease is a release that hex.pm holds until a package owner approves it.
type hexPMStagedRelease struct {
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
	HTMLURL   string `json:"html_url"`
}

// approvalURL returns where a package owner approves the staged release.
func (staged hexPMStagedRelease) approvalURL() string {
	return firstNonEmpty(staged.HTMLURL, hexPMStagedReleasesURL)
}

// pending describes the staged release of name@version waiting for approval.
func (staged hexPMStagedRelease) pending(name string, version string) string {
	message := fmt.Sprintf("hex.pm holds %s@%s as a staged release, which is not public until a package owner approves it at %s", name, version, staged.approvalURL())
	if staged.ExpiresAt != "" {
		message += "; it expires at " + staged.ExpiresAt
	}
	return message
}

// err returns why hex.pm won't serve the staged release of name@version, or nil when it
// waits for approval or was approved.
func (staged hexPMStagedRelease) err(name string, version string) error {
	switch staged.Status {
	case "staged", "approved":
		return nil
	case "rejected", "expired":
		return fmt.Errorf("hex.pm staged release %s@%s was %s, so it is not published", name, version, staged.Status)
	default:
		return fmt.Errorf("hex.pm staged release %s@%s has unknown status %q", name, version, staged.Status)
	}
}

// hexPMReleaseURL returns the hex.pm API endpoint of a release.
func hexPMReleaseURL(name string, version string) string {
	return strings.TrimRight(hexPMAPIURL, "/") + "/packages/" + url.PathEscape(name) + "/releases/" + url.PathEscape(version)
}

// lookupHexPMStagedRelease looks up the staged release of the release at endpoint,
// authenticated with key. It returns the staged release when hex.pm has one, and
// otherwise the response's status code and body.
func lookupHexPMStagedRelease(endpoint string, key string) (*hexPMStagedRelease, int, []byte, error) {
	detail("GET %s", endpoint+"/staged")
	return getHexPMStagedRelease(endpoint, key)
}

// getHexPMStagedRelease is lookupHexPMStagedRelease without logging the request.
func getHexPMStagedRelease(endpoint string, key string) (*hexPMStagedRelease, int, []byte, error) {
	code, body, err := hexPMGet(endpoint+"/staged", key)
	if err != nil || code != http.StatusOK {
		return nil, code, body, err
	}
	var staged hexPMStagedRelease
	if err := json.Unmarshal(body, &staged); err != nil {
		return nil, code, body, fmt.Errorf("parsing hex.pm staged release: %w", err)
	}
	return &staged, code, body, nil
}

// lookupHexPMRelease returns whether hex.pm serves name@version, or holds it as a staged
// release, before it is published.
func lookupHexPMRelease(key string, name string, version string) (registryVersion, error) {
	release := hexPMReleaseURL(name, version)
	detail("GET %s", release)
	code, body, err := hexPMGet(release, "")
	switch {
	case err != nil:
		return registryVersion{}, err
	case code == http.StatusOK:
		return registryVersion{served: true}, nil
	case code != http.StatusNotFound:
		return registryVersion{}, hexPMStatusError(release, code, body, "")
	}

	staged, code, body, err := lookupHexPMStagedRelease(release, key)
	switch {
	case err != nil:
		return registryVersion{}, err
	case staged != nil:
		return registryVersion{staged: staged}, nil
	case code == http.StatusNotFound:
		return registryVersion{}, nil
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return registryVersion{}, errors.New("hex.pm does not serve it, and only a key with the api:read permission from an owner of the package can look up its staged release")
	default:
		return registryVersion{}, hexPMStatusError(release+"/staged", code, body, key)
	}
}

// confirmHexPMRelease checks that hex.pm serves a release that its publish command
// reported as published. Publish commands report success for a staged release too,
// which hex.pm only serves once an owner approves it on the dashboard, so a staged
// release warns and is returned. A release that was rejected, expired, or is neither
// served nor staged fails.
func confirmHexPMRelease(key string, name string, version string) (*hexPMStagedRelease, error) {
	release := hexPMReleaseURL(name, version)
	status("checking that hex.pm serves the release")
	if served, _ := hexPMServesRelease(release); served {
		return nil, nil
	}

	staged, code, body, err := lookupHexPMStagedRelease(release, key)
	var reason string
	switch {
	case err != nil:
		reason = fmt.Sprintf("its staged release can't be looked up: %v", err)
	case staged != nil:
		if err := staged.err(name, version); err != nil {
			return nil, err
		}
		if staged.Status == "approved" {
			status("hex.pm staged release was approved")
			return nil, nil
		}
		itemWarn("%s", staged.pending(name, version))
		return staged, nil
	case code == http.StatusNotFound:
		reason = "hex.pm has no staged release of it"
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		reason = "it may be a staged release waiting for approval on the hex.pm dashboard, which HEXPM_TOKEN can only check with the api:read permission as an owner of the package"
	default:
		reason = "its staged release can't be looked up: " + hexPMStatusError(release+"/staged", code, body, key).Error()
	}

	// hex.pm may take a moment to serve a release that it did not stage.
	var lastErr error
	if hexPMReleaseAttempts > 1 {
		status("hex.pm does not serve the release yet; retrying")
	}
	for attempt := 2; attempt <= hexPMReleaseAttempts; attempt++ {
		time.Sleep(hexPMReleaseRetryInterval)
		served, err := hexPMServesRelease(release)
		if served {
			return nil, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, fmt.Errorf("hex.pm does not serve %s@%s after publishing it (%w), and %s", name, version, lastErr, reason)
	}
	return nil, fmt.Errorf("hex.pm does not serve %s@%s after publishing it, and %s", name, version, reason)
}

// waitForHexPMApproval polls hex.pm until it serves name@version, which it holds as
// staged, until the staged release is rejected or expires, or until deadline.
func waitForHexPMApproval(key string, name string, version string, staged *hexPMStagedRelease, deadline time.Time) error {
	release := hexPMReleaseURL(name, version)
	approvalURL := staged.approvalURL()
	approved := false
	for {
		if code, _, err := hexPMGet(release, ""); err == nil && code == http.StatusOK {
			status("hex.pm serves %s@%s", name, version)
			return nil
		}
		// A key that can't look up staged releases still sees the release once it is served.
		if current, _, _, err := getHexPMStagedRelease(release, key); err == nil && current != nil {
			if err := current.err(name, version); err != nil {
				return err
			}
			approvalURL = firstNonEmpty(current.HTMLURL, approvalURL)
			if current.Status == "approved" && !approved {
				approved = true
				status("hex.pm staged release was approved; waiting for hex.pm to serve it")
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("%s@%s was not approved on hex.pm in time; approve it at %s", name, version, approvalURL)
		}
		time.Sleep(min(hexPMApprovalPollInterval, remaining))
	}
}

// hexPMServesRelease reports whether the hex.pm API serves the release at endpoint, with an
// error explaining why when it does not.
func hexPMServesRelease(endpoint string) (bool, error) {
	detail("GET %s", endpoint)
	code, body, err := hexPMGet(endpoint, "")
	if err != nil {
		return false, err
	}
	if code != http.StatusOK {
		return false, hexPMStatusError(endpoint, code, body, "")
	}
	return true, nil
}

// hexPMGet requests endpoint from the hex.pm API, authenticated with key when it is set, and
// returns the response's status code and body.
func hexPMGet(endpoint string, key string) (int, []byte, error) {
	// hex.pm takes the bare API key, without a scheme.
	return registryLookup{url: endpoint, authorization: key, client: hexPMClient}.get()
}

// hexPMStatusError describes an unsuccessful hex.pm API response.
func hexPMStatusError(endpoint string, code int, body []byte, key string) error {
	return lookupStatusError(endpoint, code, body, key)
}
