package flakerelease

import (
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	packageGo:    "go.mod",
	packageCargo: "Cargo.toml",
	packageNPM:   "package.json",
	packagePyPI:  "pyproject.toml",
	packageMaven: "pom.xml",
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

type packagePublication struct {
	kind      packageKind
	source    string
	dir       string
	manifest  string
	name      string
	version   string
	artifacts []string
}

type packagePublicationSet struct {
	cfg          config
	provider     releaseProvider
	tag          string
	version      string
	temporaryDir string
	packages     []packagePublication
	commands     packageCommandRunner
}

func applyPackageRegistryDefaults(cfg *config, provider releaseProvider) {
	if cfg.packageRegistryOwner == "" && cfg.githubRepository != "" {
		if repository, err := parseRepository(cfg.githubRepository); err == nil {
			cfg.packageRegistryOwner = repository.owner
		}
	}
	if cfg.packageRegistryURL == "" {
		switch provider {
		case releaseGitHub:
			cfg.packageRegistryURL = "https://npm.pkg.github.com"
		case releaseGitea, releaseForgejo:
			cfg.packageRegistryURL = cfg.githubServerURL
		}
	}
	if cfg.packageRegistryUsername == "" {
		cfg.packageRegistryUsername = cfg.githubActor
	}
}

func preparePackagePublications(cfg config, provider releaseProvider, tag string, nixPackages []string) (*packagePublicationSet, error) {
	return preparePackagePublicationsWith(cfg, provider, tag, nixPackages, nixPkgSrc, execPackageCommandRunner{})
}

func preparePackagePublicationsWith(cfg config, provider releaseProvider, tag string, nixPackages []string, packageSource func(string) (string, error), commands packageCommandRunner) (*packagePublicationSet, error) {
	kinds, err := parsePackageKinds(cfg.publishPackages)
	if err != nil {
		return nil, err
	}
	if len(kinds) == 0 {
		return nil, nil
	}
	if err := validatePackageRegistryConfig(cfg, provider, kinds); err != nil {
		return nil, err
	}

	root, err := os.MkdirTemp("", "flake-release-packages-")
	if err != nil {
		return nil, err
	}
	set := &packagePublicationSet{
		cfg:          cfg,
		provider:     provider,
		tag:          tag,
		version:      tagVersion(tag),
		temporaryDir: root,
		commands:     commands,
	}
	cleanup := true
	defer func() {
		if cleanup {
			set.Close()
		}
	}()

	seenSources := map[string]bool{}
	foundKinds := map[packageKind]bool{}
	for index, nixPackage := range nixPackages {
		source, err := packageSource(nixPackage)
		if err != nil {
			return nil, err
		}
		if source == "" {
			continue
		}
		source, err = filepath.EvalSymlinks(source)
		if err != nil {
			return nil, fmt.Errorf("resolving source for %s: %w", nixPackage, err)
		}
		source, err = filepath.Abs(source)
		if err != nil {
			return nil, err
		}
		if seenSources[source] {
			info(dim("source already staged: %s"), source)
			continue
		}
		seenSources[source] = true

		for _, kind := range kinds {
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
			staged := filepath.Join(root, fmt.Sprintf("source-%d-%s", index+1, kind))
			if err := copyWritableTree(source, staged); err != nil {
				return nil, fmt.Errorf("staging %s source %s: %w", kind, source, err)
			}
			foundKinds[kind] = true
			set.packages = append(set.packages, packagePublication{
				kind:     kind,
				source:   source,
				dir:      staged,
				manifest: filepath.Join(staged, foundManifest),
			})
		}
	}

	for _, kind := range kinds {
		if !foundKinds[kind] {
			candidates := packageManifestCandidates(kind)
			manifestDisplay := strings.Join(candidates, " or ")
			if manifestDisplay == "" {
				manifestDisplay = string(kind)
			}
			return nil, fmt.Errorf("PUBLISH_PACKAGES requested %q, but no %s was found at an evaluated source root", kind, manifestDisplay)
		}
	}
	if err := set.preflight(); err != nil {
		return nil, err
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
	info("")
	info("preflighting package publications")
	identities := map[string]string{}
	for index := range set.packages {
		publication := &set.packages[index]
		info("validating %s package from %s", publication.kind, publication.source)
		if err := publication.preflight(set); err != nil {
			return fmt.Errorf("preflighting %s package at %s: %w", publication.kind, publication.source, err)
		}
		expectedVersion := set.version
		if publication.kind == packageGo {
			expectedVersion = set.tag
		}
		if publication.version != expectedVersion {
			return fmt.Errorf("%s package %s version %q does not match git tag %q", publication.kind, publication.name, publication.version, expectedVersion)
		}
		identity := publicationIdentity(*publication)
		if previousSource, exists := identities[identity]; exists {
			return fmt.Errorf("duplicate %s package %s@%s discovered at %s and %s", publication.kind, publication.name, publication.version, previousSource, publication.source)
		}
		identities[identity] = publication.source
	}
	return nil
}

func publicationIdentity(publication packagePublication) string {
	name := publication.name
	if publication.kind == packagePyPI {
		name = normalizePyPIName(name)
	}
	return string(publication.kind) + "\x00" + name + "\x00" + publication.version
}

func (set *packagePublicationSet) publish() error {
	if set == nil {
		return nil
	}
	info("")
	info("publishing packages")
	for index := range set.packages {
		publication := &set.packages[index]
		if set.cfg.dryRun {
			info("dry run: validated %s package %s@%s; skipping publish", publication.kind, publication.name, publication.version)
			continue
		}
		info("publishing %s package %s@%s", publication.kind, publication.name, publication.version)
		if err := publication.publish(set); err != nil {
			return fmt.Errorf("publishing %s package %s@%s: %w", publication.kind, publication.name, publication.version, err)
		}
	}
	return nil
}

func (publication *packagePublication) preflight(set *packagePublicationSet) error {
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
		return fmt.Errorf("PACKAGE_REGISTRY_OWNER is required when PUBLISH_PACKAGES is set")
	}
	if strings.TrimSpace(cfg.packageRegistryOwner) != cfg.packageRegistryOwner || cfg.packageRegistryOwner == "." || cfg.packageRegistryOwner == ".." || strings.ContainsAny(cfg.packageRegistryOwner, "/\\?#") {
		return fmt.Errorf("PACKAGE_REGISTRY_OWNER must be a single owner or namespace name")
	}
	if cfg.packageRegistryURL == "" {
		return fmt.Errorf("PACKAGE_REGISTRY_URL is required when PUBLISH_PACKAGES is set")
	}
	parsed, err := url.Parse(cfg.packageRegistryURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("PACKAGE_REGISTRY_URL must be an HTTP(S) URL without credentials, query, or fragment")
	}
	if !cfg.dryRun && cfg.packageRegistryToken == "" {
		return fmt.Errorf("PACKAGE_REGISTRY_TOKEN is required when PUBLISH_PACKAGES is set")
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
			if set.cfg.githubRepository != "" {
				if repository, err := parseRepository(set.cfg.githubRepository); err == nil {
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
