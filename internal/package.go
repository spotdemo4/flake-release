package flakerelease

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
)

type packageKind string

const (
	packageGo     packageKind = "go"
	packageCargo  packageKind = "cargo"
	packageNPM    packageKind = "npm"
	packagePyPI   packageKind = "pypi"
	packageMaven  packageKind = "maven"
	packageGradle packageKind = "gradle"
)

var packageManifests = map[packageKind]string{
	packageGo:     "go.mod",
	packageCargo:  "Cargo.toml",
	packageNPM:    "package.json",
	packagePyPI:   "pyproject.toml",
	packageMaven:  "pom.xml",
	packageGradle: "build.gradle",
}

func packageManifestCandidates(kind packageKind) []string {
	switch kind {
	case packageGradle:
		return []string{"build.gradle", "build.gradle.kts"}
	default:
		if manifest, ok := packageManifests[kind]; ok {
			return []string{manifest}
		}
		return nil
	}
}

// packageRegistry is a registry that packages are published to. The zero value is the
// git host's own package registry, configured by PACKAGE_REGISTRY_*.
type packageRegistry string

const (
	registryHost         packageRegistry = ""
	registryCratesIO     packageRegistry = "crates-io"
	registryNPMJS        packageRegistry = "npmjs"
	registryMavenCentral packageRegistry = "maven-central"
)

func packageRegistryName(registry packageRegistry, provider releaseProvider) string {
	switch registry {
	case registryCratesIO:
		return "crates.io"
	case registryNPMJS:
		return "npmjs.com"
	case registryMavenCentral:
		return "Maven Central"
	default:
		return releaseProviderName(provider)
	}
}

// packageTarget is a registry and the package kinds discovered for it.
type packageTarget struct {
	registry packageRegistry
	kinds    []packageKind
}

type packagePublication struct {
	kind      packageKind
	registry  packageRegistry
	source    string
	dir       string
	manifest  string
	name      string
	version   string
	artifacts []string
	// publishArgs are extra arguments for the registry's publish command.
	publishArgs []string
}

// errPackageNotPublishable marks a manifest that opts out of publishing, such as a
// private package.json. Discovered manifests with it are skipped; requested ones fail.
var errPackageNotPublishable = errors.New("package is not publishable")

type packagePublicationSet struct {
	cfg          config
	provider     releaseProvider
	releaseTag   releaseTag
	temporaryDir string
	packages     []packagePublication
	commands     packageCommandRunner
	// explicit is set when the deprecated PUBLISH_PACKAGES selected the host package kinds.
	explicit bool
	signer   *openpgp.Entity
}

func applyPackageRegistryDefaults(cfg *config, provider releaseProvider) {
	if cfg.packageRegistryOwner == "" && cfg.gitRepository != "" {
		if repository, err := parseRepository(cfg.gitRepository); err == nil {
			cfg.packageRegistryOwner = repository.owner
		}
	}
	if cfg.packageRegistryURL == "" {
		switch provider {
		case releaseGitHub:
			cfg.packageRegistryURL = "https://npm.pkg.github.com"
		case releaseGitea, releaseForgejo:
			cfg.packageRegistryURL = cfg.gitServerURL
		}
	}
	if cfg.packageRegistryUsername == "" {
		cfg.packageRegistryUsername = cfg.gitActor
	}
}

func preparePackagePublications(cfg config, provider releaseProvider, tag releaseTag, nixPackages []string) (*packagePublicationSet, error) {
	return preparePackagePublicationsWith(cfg, provider, tag, nixPackages, nixPkgSrc, execPackageCommandRunner{})
}

func preparePackagePublicationsWith(cfg config, provider releaseProvider, tag releaseTag, nixPackages []string, packageSource func(string) (nixSource, error), commands packageCommandRunner) (*packagePublicationSet, error) {
	targets, explicit, err := selectPackageTargets(cfg, provider)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, nil
	}
	if explicit {
		if err := validatePackageRegistryConfig(cfg, provider, targets[0].kinds); err != nil {
			return nil, err
		}
	}

	temporaryDir, err := os.MkdirTemp("", "flake-release-packages-")
	if err != nil {
		return nil, err
	}
	set := &packagePublicationSet{
		cfg:          cfg,
		provider:     provider,
		releaseTag:   tag,
		temporaryDir: temporaryDir,
		commands:     commands,
		explicit:     explicit,
	}
	cleanup := true
	defer func() {
		if cleanup {
			set.Close()
		}
	}()

	section("Discovering package publications")
	type registryKind struct {
		registry packageRegistry
		kind     packageKind
	}
	seenSources := map[string]bool{}
	foundKinds := map[registryKind]bool{}
	for index, nixPackage := range nixPackages {
		item("%s", nixPackage)
		src, err := packageSource(nixPackage)
		if err != nil {
			return nil, err
		}
		if src.root == "" {
			detail("source: unavailable")
			continue
		}
		detail("source: %s", src.root)
		if src.subdir != "" {
			detail("source root: %s", src.subdir)
		}
		root, err := filepath.EvalSymlinks(src.root)
		if err != nil {
			return nil, fmt.Errorf("resolving source for %s: %w", nixPackage, err)
		}
		root, err = filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		// Packages often share one repository-wide src and differ only in sourceRoot.
		source := filepath.Join(root, src.subdir)
		if seenSources[source] {
			status("source already staged; skipping duplicate")
			continue
		}
		seenSources[source] = true

		for _, target := range targets {
			for _, kind := range target.kinds {
				candidates := packageManifestCandidates(kind)
				foundManifest := ""
				for _, candidate := range candidates {
					if isFile(filepath.Join(source, candidate)) {
						foundManifest = candidate
						break
					}
				}
				if foundManifest == "" {
					continue
				}
				// Stage the whole src so paths outside sourceRoot, such as shared test data, still resolve.
				// Each registry gets its own copy, since preflight may rewrite manifests for it.
				stagedName := fmt.Sprintf("source-%d-%s", index+1, kind)
				if target.registry != registryHost {
					stagedName += "-" + string(target.registry)
				}
				staged := filepath.Join(set.temporaryDir, stagedName)
				if err := copyWritableTree(root, staged); err != nil {
					return nil, fmt.Errorf("staging %s source %s: %w", kind, root, err)
				}
				dir := filepath.Join(staged, src.subdir)
				foundKinds[registryKind{target.registry, kind}] = true
				set.packages = append(set.packages, packagePublication{
					kind:     kind,
					registry: target.registry,
					source:   source,
					dir:      dir,
					manifest: filepath.Join(dir, foundManifest),
				})
			}
		}
	}

	if !explicit && len(set.packages) == 0 {
		status("no package manifests found")
		return nil, nil
	}
	for targetIndex, target := range targets {
		discovered := make([]packageKind, 0, len(target.kinds))
		for _, kind := range target.kinds {
			if foundKinds[registryKind{target.registry, kind}] {
				discovered = append(discovered, kind)
				continue
			}
			if explicit && targetIndex == 0 {
				candidates := packageManifestCandidates(kind)
				manifestDisplay := strings.Join(candidates, " or ")
				if manifestDisplay == "" {
					manifestDisplay = string(kind)
				}
				return nil, fmt.Errorf("PUBLISH_PACKAGES requested %q, but no %s was found at an evaluated source root", kind, manifestDisplay)
			}
		}
		if len(discovered) == 0 || (explicit && targetIndex == 0) {
			continue
		}
		if target.registry == registryHost {
			err = validatePackageRegistryConfig(cfg, provider, discovered)
		} else {
			err = validatePublicRegistryConfig(cfg, target.registry)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := set.preflight(); err != nil {
		return nil, err
	}
	if len(set.packages) == 0 {
		return nil, nil
	}

	cleanup = false
	return set, nil
}

func (set *packagePublicationSet) Close() {
	if set != nil && set.temporaryDir != "" {
		deletePath(set.temporaryDir)
		set.temporaryDir = ""
	}
}

func (set *packagePublicationSet) preflight() error {
	section("Preflighting package publications")
	identities := map[string]string{}
	publishable := set.packages[:0]
	for index := range set.packages {
		publication := &set.packages[index]
		registry := packageRegistryName(publication.registry, set.provider)
		item("%s for %s from %s", publication.kind, registry, publication.source)
		status("validating package")
		if err := publication.preflight(set); err != nil {
			explicit := set.explicit && publication.registry == registryHost
			if !explicit && errors.Is(err, errPackageNotPublishable) {
				status("skipping: %v", err)
				continue
			}
			return fmt.Errorf("preflighting %s package for %s at %s: %w", publication.kind, registry, publication.source, err)
		}
		detail("name: %s", publication.name)
		detail("version: %s", publication.version)
		expectedVersion := set.releaseTag.version
		if publication.kind == packageGo {
			expectedVersion = set.releaseTag.versionTag
		}
		if publication.version != expectedVersion {
			return fmt.Errorf("%s package %s version %q does not match git tag %q", publication.kind, publication.name, publication.version, expectedVersion)
		}
		identity := publicationIdentity(*publication)
		if previousSource, exists := identities[identity]; exists {
			return fmt.Errorf("duplicate %s package %s@%s for %s discovered at %s and %s", publication.kind, publication.name, publication.version, registry, previousSource, publication.source)
		}
		identities[identity] = publication.source
		publishable = append(publishable, *publication)
	}
	set.packages = publishable
	return nil
}

func publicationIdentity(publication packagePublication) string {
	name := publication.name
	if publication.kind == packagePyPI {
		name = normalizePyPIName(name)
	}
	// Gradle and Maven publish to the same Maven registry.
	kind := publication.kind
	if kind == packageGradle {
		kind = packageMaven
	}
	return string(publication.registry) + "\x00" + string(kind) + "\x00" + name + "\x00" + publication.version
}

func (set *packagePublicationSet) publish() error {
	if set == nil {
		return nil
	}
	section("Publishing packages")
	for index := range set.packages {
		publication := &set.packages[index]
		registry := packageRegistryName(publication.registry, set.provider)
		item("%s %s@%s to %s", publication.kind, publication.name, publication.version, registry)
		if set.cfg.dryRun {
			status("dry run: validated package; skipping publish")
			continue
		}
		status("publishing package")
		if err := publication.publish(set); err != nil {
			return fmt.Errorf("publishing %s package %s@%s to %s: %w", publication.kind, publication.name, publication.version, registry, err)
		}
	}
	return nil
}

func (publication *packagePublication) preflight(set *packagePublicationSet) error {
	if publication.registry == registryMavenCentral {
		return preflightMavenCentralPackage(set, publication)
	}
	switch publication.kind {
	case packageGo:
		return preflightGoPackage(set, publication)
	case packageCargo:
		return preflightCargoPackage(set, publication)
	case packageNPM:
		return preflightNPMPackage(set, publication)
	case packagePyPI:
		return preflightPyPIPackage(set, publication)
	case packageMaven:
		return preflightMavenPackage(set, publication)
	case packageGradle:
		return preflightGradlePackage(set, publication)
	default:
		return fmt.Errorf("unsupported package kind %q", publication.kind)
	}
}

func (publication *packagePublication) publish(set *packagePublicationSet) error {
	if publication.registry == registryMavenCentral {
		return publishMavenCentralPackage(set, publication)
	}
	switch publication.kind {
	case packageGo:
		return publishGoPackage(set, publication)
	case packageCargo:
		return publishCargoPackage(set, publication)
	case packageNPM:
		return publishNPMPackage(set, publication)
	case packagePyPI:
		return publishPyPIPackage(set, publication)
	case packageMaven:
		return publishMavenPackage(set, publication)
	case packageGradle:
		return publishGradlePackage(set, publication)
	default:
		return fmt.Errorf("unsupported package kind %q", publication.kind)
	}
}

// selectPackageTargets returns the registries to publish to and the package kinds to
// discover for each. The git host's registry, when enabled, is always first.
func selectPackageTargets(cfg config, provider releaseProvider) ([]packageTarget, bool, error) {
	kinds, explicit, err := selectPackageKinds(cfg, provider)
	if err != nil {
		return nil, false, err
	}
	var targets []packageTarget
	if len(kinds) > 0 {
		targets = append(targets, packageTarget{registry: registryHost, kinds: kinds})
	} else {
		explicit = false
	}
	return append(targets, publicPackageTargets(cfg)...), explicit, nil
}

// publicPackageTargets returns the public registries enabled by their tokens.
func publicPackageTargets(cfg config) []packageTarget {
	var targets []packageTarget
	if cfg.cratesIOToken != "" {
		targets = append(targets, packageTarget{registry: registryCratesIO, kinds: []packageKind{packageCargo}})
	}
	if cfg.npmjsToken != "" {
		targets = append(targets, packageTarget{registry: registryNPMJS, kinds: []packageKind{packageNPM}})
	}
	if cfg.mavenCentralToken != "" {
		targets = append(targets, packageTarget{registry: registryMavenCentral, kinds: []packageKind{packageGradle, packageMaven}})
	}
	return targets
}

// selectPackageKinds returns the package kinds to discover. The deprecated
// PUBLISH_PACKAGES selects them explicitly; otherwise every kind the provider
// supports is discovered when PACKAGE_REGISTRY_TOKEN is set.
func selectPackageKinds(cfg config, provider releaseProvider) ([]packageKind, bool, error) {
	if strings.TrimSpace(cfg.publishPackages) != "" {
		warn("PUBLISH_PACKAGES is deprecated; discovered packages are published when PACKAGE_REGISTRY_TOKEN is set")
		kinds, err := parsePackageKinds(cfg.publishPackages)
		return kinds, true, err
	}
	if cfg.packageRegistryToken == "" {
		return nil, false, nil
	}
	return supportedPackageKinds(provider), false, nil
}

func supportedPackageKinds(provider releaseProvider) []packageKind {
	switch provider {
	case releaseForgejo, releaseGitea:
		return []packageKind{packageCargo, packageGo, packageGradle, packageMaven, packageNPM, packagePyPI}
	case releaseGitHub:
		return []packageKind{packageGradle, packageMaven, packageNPM}
	default:
		return nil
	}
}

func parsePackageKinds(value string) ([]packageKind, error) {
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\r' || r == '\n'
	})
	seen := map[packageKind]bool{}
	kinds := make([]packageKind, 0, len(fields))
	for _, field := range fields {
		kind := packageKind(field)
		switch kind {
		case packageGo, packageCargo, packageNPM, packagePyPI, packageMaven, packageGradle:
		default:
			return nil, fmt.Errorf("unsupported package kind %q in PUBLISH_PACKAGES; supported kinds are cargo, go, gradle, maven, npm, and pypi", field)
		}
		if !seen[kind] {
			seen[kind] = true
			kinds = append(kinds, kind)
		}
	}
	slices.Sort(kinds)
	return kinds, nil
}

func validatePackageRegistryConfig(cfg config, provider releaseProvider, kinds []packageKind) error {
	if cfg.packageRegistryOwner == "" {
		return fmt.Errorf("PACKAGE_REGISTRY_OWNER is required to publish packages")
	}
	if strings.TrimSpace(cfg.packageRegistryOwner) != cfg.packageRegistryOwner || cfg.packageRegistryOwner == "." || cfg.packageRegistryOwner == ".." || strings.ContainsAny(cfg.packageRegistryOwner, "/\\?#") {
		return fmt.Errorf("PACKAGE_REGISTRY_OWNER must be a single owner or namespace name")
	}
	if cfg.packageRegistryURL == "" {
		return fmt.Errorf("PACKAGE_REGISTRY_URL is required to publish packages")
	}
	parsed, err := url.Parse(cfg.packageRegistryURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("PACKAGE_REGISTRY_URL must be an HTTP(S) URL without credentials, query, or fragment")
	}
	if !cfg.dryRun && cfg.packageRegistryToken == "" {
		return fmt.Errorf("PACKAGE_REGISTRY_TOKEN is required to publish packages")
	}

	for _, kind := range kinds {
		switch provider {
		case releaseForgejo, releaseGitea:
			if !cfg.dryRun && kind == packagePyPI && cfg.packageRegistryUsername == "" {
				return fmt.Errorf("PACKAGE_REGISTRY_USERNAME is required to publish PyPI packages to %s", provider)
			}
		case releaseGitHub:
			switch kind {
			case packageNPM, packageMaven, packageGradle:
			default:
				return fmt.Errorf("GitHub package publishing supports gradle, maven, and npm only, not %s", kind)
			}
		default:
			return fmt.Errorf("package publishing is not supported for release provider %q", provider)
		}
	}
	return nil
}

func validatePublicRegistryConfig(cfg config, registry packageRegistry) error {
	if registry != registryMavenCentral {
		return nil
	}
	if !cfg.dryRun && cfg.mavenCentralUsername == "" {
		return fmt.Errorf("MAVEN_CENTRAL_USERNAME is required to publish to Maven Central")
	}
	// Bundles are signed during preflight, so a dry run needs the key too.
	if strings.TrimSpace(cfg.mavenCentralSigningKey) == "" {
		return fmt.Errorf("MAVEN_CENTRAL_SIGNING_KEY is required to publish to Maven Central")
	}
	return nil
}

func (set *packagePublicationSet) registryURL(kind packageKind) string {
	base := strings.TrimRight(set.cfg.packageRegistryURL, "/")
	if set.provider == releaseGitHub {
		switch kind {
		case packageNPM:
			if base == "" {
				base = "https://npm.pkg.github.com"
			}
			return base + "/"
		case packageMaven, packageGradle:
			explicitURL := os.Getenv("PACKAGE_REGISTRY_URL") != ""
			if explicitURL && base != "" && base != "https://npm.pkg.github.com" {
				return base + "/"
			}
			if explicitURL && base != "" && strings.Contains(base, "maven.pkg.github.com") {
				return base + "/"
			}
			owner := set.cfg.packageRegistryOwner
			repo := ""
			if set.cfg.gitRepository != "" {
				if repository, err := parseRepository(set.cfg.gitRepository); err == nil {
					repo = repository.name
				}
			}
			if repo != "" {
				return fmt.Sprintf("https://maven.pkg.github.com/%s/%s/", url.PathEscape(owner), url.PathEscape(repo))
			}
			return fmt.Sprintf("https://maven.pkg.github.com/%s/", url.PathEscape(owner))
		}
		return base + "/"
	}
	registryKind := kind
	if kind == packageGradle {
		registryKind = packageMaven
	}
	return fmt.Sprintf("%s/api/packages/%s/%s/", base, url.PathEscape(set.cfg.packageRegistryOwner), url.PathEscape(string(registryKind)))
}

func packageRegistryDisplayURL(value string) string {
	if value == "" {
		return "<none>"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "<invalid>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func copyWritableTree(source string, destination string) error {
	stat, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !stat.IsDir() {
		return fmt.Errorf("source is not a directory")
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}

	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case entry.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode().IsRegular():
			if err := copyWritableFile(path, target, info.Mode().Perm()|0o600); err != nil {
				return err
			}
			return nil
		default:
			return nil
		}
	})
}

func copyWritableFile(source string, destination string, mode fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
