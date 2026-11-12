// Package flakerelease generates release artifacts for packages in a nix flake.
package flakerelease

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

type config struct {
	dryRun                      bool
	bundleAppImage              bool
	deleteOldReleaseArtifacts   string
	gitRepository               string
	gitServerURL                string
	gitActor                    string
	gitToken                    string
	containerRegistry           string
	containerRegistryUsername   string
	containerRegistryPassword   string
	publishPackages             string
	packageRegistryOwner        string
	packageRegistryURL          string
	packageRegistryToken        string
	packageRegistryUsername     string
	cratesIOToken               string
	npmjsToken                  string
	npmjsTrustedPublishing      bool
	pypiToken                   string
	pypiTrustedPublishing       bool
	mavenCentralUsername        string
	mavenCentralToken           string
	mavenCentralSigningKey      string
	mavenCentralSigningPassword string
	pkgGoDevPublishing          bool
	hexPMToken                  string
}

type releaseSession struct {
	cfg               config
	client            releaseClient
	tag               releaseTag
	changelog         string
	hasOutput         bool
	creationAttempted bool
	creationErr       error
	created           bool
}

func (session *releaseSession) ensureRelease() error {
	session.hasOutput = true
	if session.creationAttempted {
		return session.creationErr
	}
	session.creationAttempted = true

	if session.cfg.dryRun {
		status("dry run: skipping release creation")
		return nil
	}
	created, err := session.client.createRelease(session.tag.full, session.changelog)
	if err != nil {
		session.creationErr = fmt.Errorf("creating release %s: %w", session.tag.full, err)
		return session.creationErr
	}
	session.created = created
	return nil
}

func (session releaseSession) requireOutput() error {
	if session.creationErr != nil {
		return session.creationErr
	}
	if !session.hasOutput {
		return errors.New("no releasable package outputs found")
	}
	return nil
}

func configFromEnv() config {
	return config{
		dryRun:                      truthy(os.Getenv("DRY_RUN")),
		bundleAppImage:              truthy(os.Getenv("BUNDLE_APPIMAGE")),
		deleteOldReleaseArtifacts:   os.Getenv("DELETE_OLD_RELEASE_ARTIFACTS"),
		gitRepository:               actionsEnv("GIT_REPOSITORY", "GITHUB_REPOSITORY"),
		gitServerURL:                actionsEnv("GIT_SERVER_URL", "GITHUB_SERVER_URL"),
		gitActor:                    actionsEnv("GIT_ACTOR", "GITHUB_ACTOR"),
		gitToken:                    gitTokenEnv(),
		containerRegistry:           deprecatedEnv("CONTAINER_REGISTRY", "REGISTRY"),
		containerRegistryUsername:   deprecatedEnv("CONTAINER_REGISTRY_USERNAME", "REGISTRY_USERNAME"),
		containerRegistryPassword:   deprecatedEnv("CONTAINER_REGISTRY_PASSWORD", "REGISTRY_PASSWORD"),
		publishPackages:             os.Getenv("PUBLISH_PACKAGES"),
		packageRegistryOwner:        os.Getenv("PACKAGE_REGISTRY_OWNER"),
		packageRegistryURL:          os.Getenv("PACKAGE_REGISTRY_URL"),
		packageRegistryToken:        os.Getenv("PACKAGE_REGISTRY_TOKEN"),
		packageRegistryUsername:     os.Getenv("PACKAGE_REGISTRY_USERNAME"),
		cratesIOToken:               os.Getenv("CRATES_IO_TOKEN"),
		npmjsToken:                  os.Getenv("NPMJS_TOKEN"),
		npmjsTrustedPublishing:      truthy(os.Getenv("NPMJS_TRUSTED_PUBLISHING")),
		pypiToken:                   os.Getenv("PYPI_TOKEN"),
		pypiTrustedPublishing:       truthy(os.Getenv("PYPI_TRUSTED_PUBLISHING")),
		mavenCentralUsername:        os.Getenv("MAVEN_CENTRAL_USERNAME"),
		mavenCentralToken:           os.Getenv("MAVEN_CENTRAL_TOKEN"),
		mavenCentralSigningKey:      os.Getenv("MAVEN_CENTRAL_SIGNING_KEY"),
		mavenCentralSigningPassword: os.Getenv("MAVEN_CENTRAL_SIGNING_PASSWORD"),
		pkgGoDevPublishing:          truthy(os.Getenv("PKG_GO_DEV_PUBLISHING")),
		hexPMToken:                  os.Getenv("HEXPM_TOKEN"),
	}
}

// deprecatedEnv reads name, falling back to the deprecated variable when name is unset.
func deprecatedEnv(name string, deprecated string) string {
	value := os.Getenv(name)
	old := os.Getenv(deprecated)
	if old == "" {
		return value
	}
	if value != "" && value != old {
		warn("%s is deprecated and ignored because %s is set", deprecated, name)
		return value
	}
	warn("%s is deprecated; use %s instead", deprecated, name)
	return old
}

// actionsEnv reads name, falling back to the GITHUB_ variable that Actions runners set.
// Outside Actions the fallback can only have been set by hand, so it is deprecated there.
func actionsEnv(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	value := os.Getenv(fallback)
	if value != "" && !inActions() {
		warn("%s is deprecated; use %s instead", fallback, name)
	}
	return value
}

// gitTokenEnv reads GIT_TOKEN, falling back to GITHUB_TOKEN.
func gitTokenEnv() string {
	if token := os.Getenv("GIT_TOKEN"); token != "" {
		return token
	}
	return os.Getenv("GITHUB_TOKEN")
}

func inActions() bool {
	return os.Getenv("GITHUB_ACTIONS") != "" || os.Getenv("GITEA_ACTIONS") != "" || os.Getenv("FORGEJO_ACTIONS") != ""
}

// defaultContainerRegistry returns ghcr.io on GitHub, otherwise the host of serverURL.
func defaultContainerRegistry(provider releaseProvider, serverURL string) string {
	if provider == releaseGitHub {
		return "ghcr.io"
	}
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

type runMode int

const (
	runRelease runMode = iota
	runHelp
	runVersion
)

func parseRunArgs(cfg *config, args []string) ([]string, runMode) {
	var packages []string
	for _, arg := range args {
		switch arg {
		case "--help":
			return packages, runHelp
		case "--version":
			return packages, runVersion
		case "--dry-run":
			cfg.dryRun = true
		case "--bundle-appimage":
			cfg.bundleAppImage = true
		default:
			packages = append(packages, arg)
		}
	}
	return packages, runRelease
}

// Run releases the flake packages named in args and the PACKAGES environment variable.
func Run(args []string) error {
	cfg := configFromEnv()
	packages, mode := parseRunArgs(&cfg, args)
	switch mode {
	case runHelp:
		info("Usage: flake-release [packages...] [--dry-run] [--bundle-appimage]")
		info("       flake-release --version")
		info("")
		info("If no packages are provided as arguments, the command will attempt to get packages from the nix flake for the current system.")
		return nil
	case runVersion:
		_, err := fmt.Println("flake-release", currentVersion())
		return err
	case runRelease:
	}
	retain, err := parseArtifactRetention(cfg.deleteOldReleaseArtifacts)
	if err != nil {
		return err
	}
	packages = append(packages, splitPackages(os.Getenv("PACKAGES"))...)

	if err := setupContainerEnvironment(); err != nil {
		return err
	}
	setupNixConfig()

	origin, err := gitOrigin()
	if err != nil {
		return err
	}
	if cfg.gitRepository == "" {
		if repository := gitRepositoryFromOrigin(origin); repository != "" {
			cfg.gitRepository = repository
			_ = os.Setenv("GIT_REPOSITORY", cfg.gitRepository)
		}
	}
	info("git repository: %s", firstNonEmpty(cfg.gitRepository, "<none>"))
	if cfg.gitServerURL == "" {
		if serverURL := gitServerURLFromOrigin(origin); serverURL != "" {
			cfg.gitServerURL = serverURL
			_ = os.Setenv("GIT_SERVER_URL", cfg.gitServerURL)
		}
	}
	info("git server: %s", firstNonEmpty(cfg.gitServerURL, "<none>"))

	provider, err := releaseType(origin)
	if err != nil {
		return err
	}
	info("git type: %s", provider)

	hintName, hintFromCI, err := releaseTagHint()
	if err != nil {
		return err
	}
	hint, err := newTagHint(hintName, hintFromCI)
	if err != nil {
		return err
	}
	if hint != nil {
		info("git tag: %s", hint.tag.full)
	}

	if cfg.gitActor == "" {
		cfg.gitActor, err = gitUser()
		if err != nil {
			return err
		}
		_ = os.Setenv("GIT_ACTOR", cfg.gitActor)
	}
	info("git user: %s", cfg.gitActor)

	if cfg.containerRegistryUsername == "" {
		cfg.containerRegistryUsername = cfg.gitActor
		_ = os.Setenv("CONTAINER_REGISTRY_USERNAME", cfg.containerRegistryUsername)
	}
	info("container registry user: %s", cfg.containerRegistryUsername)

	if cfg.containerRegistryPassword == "" && cfg.gitToken != "" {
		cfg.containerRegistryPassword = cfg.gitToken
		_ = os.Setenv("CONTAINER_REGISTRY_PASSWORD", cfg.containerRegistryPassword)
	}

	if cfg.containerRegistry == "" {
		if registry := defaultContainerRegistry(provider, cfg.gitServerURL); registry != "" {
			cfg.containerRegistry = registry
			_ = os.Setenv("CONTAINER_REGISTRY", cfg.containerRegistry)
		}
	}
	info("container registry: %s", firstNonEmpty(cfg.containerRegistry, "<none>"))

	applyPackageRegistryDefaults(&cfg, provider)
	if cfg.publishPackages != "" || cfg.packageRegistryToken != "" {
		info("package registry owner: %s", firstNonEmpty(cfg.packageRegistryOwner, "<none>"))
		info("package registry: %s", packageRegistryDisplayURL(cfg.packageRegistryURL))
		info("package registry user: %s", firstNonEmpty(cfg.packageRegistryUsername, "<none>"))
	}
	if targets := publicPackageTargets(cfg); len(targets) > 0 {
		names := make([]string, 0, len(targets))
		for _, target := range targets {
			names = append(names, packageRegistryName(target.registry, provider))
		}
		info("public package registries: %s", strings.Join(names, ", "))
	}

	system := cachedNixSystem(nixSystem)
	if len(packages) == 0 {
		packages, err = defaultPackages(system, nixSystemHasPackages)
		if err != nil {
			return err
		}
	} else {
		packages = resolvePackages(packages, nixPkgExists, system)
	}
	// A flake without packages still gets a release carrying the changelog.
	changelogOnly := len(packages) == 0
	releasePackages := prepareReleasePackages(packages)

	groups, err := releaseGroups(changelogOnly, releasePackages, hint, nixPackageSourcePath)
	if err != nil {
		return err
	}

	release := newReleaseClient(provider, cfg)
	// Every release is prepared and validated before anything is published. Registry
	// dry runs, which may need packages from earlier releases, run as each one publishes.
	prepared := make([]preparedRelease, 0, len(groups))
	defer func() {
		for _, ready := range prepared {
			ready.Close()
		}
	}()
	for _, group := range groups {
		if len(groups) > 1 {
			section("Preparing release %s", group.tag.full)
		}
		ready, err := prepareRelease(cfg, provider, group, changelogOnly)
		prepared = append(prepared, ready)
		if err != nil {
			return err
		}
	}

	return publishReleases(cfg, prepared, func(ready preparedRelease, unpublished func(packagePublication) []string) error {
		return publishRelease(cfg, provider, release, retain, ready, unpublished)
	})
}

// releasePublisher publishes a prepared release. unpublished reports a publication's
// dependencies that earlier releases in the run should have published but did not.
type releasePublisher func(ready preparedRelease, unpublished func(packagePublication) []string) error

// publishReleases publishes each release after the releases it depends on. A failed
// release skips the releases that depend on it, but not unrelated ones.
func publishReleases(cfg config, prepared []preparedRelease, publish releasePublisher) error {
	multiple := len(prepared) > 1
	dependencies := releaseDependencies(prepared)
	order := orderReleases(prepared, dependencies)
	failed := make([]bool, len(prepared))
	outcomes := make([]string, len(prepared))
	var errs error
	for _, index := range order {
		ready := prepared[index]
		var blockers []string
		for _, dependency := range dependencies[index] {
			if failed[dependency] {
				blockers = append(blockers, prepared[dependency].tag.full)
			}
		}
		if len(blockers) > 0 {
			failed[index] = true
			outcomes[index] = "skipped because " + strings.Join(blockers, ", ") + " failed"
			section("Skipping release %s", ready.tag.full)
			itemWarn("%s", outcomes[index])
			errs = errors.Join(errs, fmt.Errorf("%s: %s", ready.tag.full, outcomes[index]))
			continue
		}

		if multiple {
			section("Releasing %s", ready.tag.full)
		}
		// A dry run publishes nothing, so a dependency on an earlier release is unavailable.
		var unpublished func(packagePublication) []string
		if cfg.dryRun {
			unpublished = func(publication packagePublication) []string {
				return publicationDependenciesIn(publication, prepared, dependencies[index])
			}
		}
		if err := publish(ready, unpublished); err != nil {
			failed[index] = true
			outcomes[index] = "failed"
			if multiple {
				err = fmt.Errorf("%s: %w", ready.tag.full, err)
			}
			errs = errors.Join(errs, err)
			continue
		}
		outcomes[index] = "released"
	}

	if multiple {
		section("Release summary")
		for _, index := range order {
			item("%s", prepared[index].tag.full)
			if outcomes[index] == "released" {
				status("%s", outcomes[index])
			} else {
				itemWarn("%s", outcomes[index])
			}
		}
	}
	return errs
}

// releaseDependencies returns, for each release, the other releases that publish a
// package one of its packages depends on.
func releaseDependencies(prepared []preparedRelease) [][]int {
	providers := map[string][]int{}
	for index, ready := range prepared {
		for _, publication := range ready.publicationList() {
			key := publicationDependencyKey(publication.kind, publication.manifestName)
			if !slices.Contains(providers[key], index) {
				providers[key] = append(providers[key], index)
			}
		}
	}
	dependencies := make([][]int, len(prepared))
	for index, ready := range prepared {
		for _, publication := range ready.publicationList() {
			for _, name := range publication.dependencies {
				for _, provider := range providers[publicationDependencyKey(publication.kind, name)] {
					if provider != index && !slices.Contains(dependencies[index], provider) {
						dependencies[index] = append(dependencies[index], provider)
					}
				}
			}
		}
		slices.Sort(dependencies[index])
	}
	return dependencies
}

func publicationDependencyKey(kind packageKind, name string) string {
	return string(kind) + "\x00" + name
}

// publicationDependenciesIn returns the dependencies of publication that the releases
// at indexes publish.
func publicationDependenciesIn(publication packagePublication, prepared []preparedRelease, indexes []int) []string {
	var names []string
	for _, index := range indexes {
		for _, provided := range prepared[index].publicationList() {
			if provided.kind == publication.kind && slices.Contains(publication.dependencies, provided.manifestName) && !slices.Contains(names, provided.manifestName) {
				names = append(names, provided.manifestName)
			}
		}
	}
	slices.Sort(names)
	return names
}

// orderReleases orders releases so each follows the releases it depends on, otherwise
// keeping the order their tags were resolved in. Releases that depend on each other
// keep that order.
func orderReleases(prepared []preparedRelease, dependencies [][]int) []int {
	placed := make([]bool, len(prepared))
	order := make([]int, 0, len(prepared))
	for len(order) < len(prepared) {
		next := -1
		for index := range prepared {
			if !placed[index] && !slices.ContainsFunc(dependencies[index], func(dependency int) bool { return !placed[dependency] }) {
				next = index
				break
			}
		}
		if next < 0 {
			next = slices.Index(placed, false)
			warn("release %s depends on a release that depends on it; releasing it in the order listed", prepared[next].tag.full)
		}
		placed[next] = true
		order = append(order, next)
	}
	return order
}

// releaseGroups resolves the release tag of each package and groups packages by tag.
// A changelog-only release uses the tag hint, or the latest Git tag.
func releaseGroups(changelogOnly bool, packages []releasePackagePlan, hint *tagHint, sourcePath packageSourcePath) ([]releaseGroup, error) {
	if changelogOnly {
		if hint != nil {
			return []releaseGroup{{tag: hint.tag}}, nil
		}
		selected, err := gitLatestTag()
		if err != nil {
			return nil, err
		}
		tag, err := parseSelectedReleaseTag(selected)
		if err != nil {
			return nil, err
		}
		info("git tag: %s", tag.full)
		return []releaseGroup{{tag: tag}}, nil
	}

	var tags []releaseTag
	var err error
	switch {
	case hint == nil:
		tags, err = gitReachableReleaseTags()
	case hint.filter:
		// Only tags created with the CI tag are candidates, so a package whose version
		// happens to match an old scoped tag is not released to it again.
		tags, err = gitHeadReleaseTags()
	}
	if err != nil {
		return nil, err
	}
	groups, err := groupReleasePackages(packages, hint, tags, sourcePath)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, errors.New("no releasable package outputs found")
	}
	return groups, nil
}

// nixPackageSourcePath returns the package's sourceRoot directory within its src. When
// src is the repository, or a fileset rooted there, this is its path in the repository.
func nixPackageSourcePath(plan releasePackagePlan) string {
	src, err := nixPkgSrc(plan.pkg)
	if err != nil || src.root == "" {
		return ""
	}
	return filepath.ToSlash(src.subdir)
}

// preparedRelease is a release group ready to publish: its package publications are
// validated, its changelog is written, and its image destinations are checked.
type preparedRelease struct {
	releaseGroup
	changelogOnly   bool
	publications    *packagePublicationSet
	changelog       string
	imageRepository string
}

// publicationList returns the release's package publications.
func (release preparedRelease) publicationList() []packagePublication {
	if release.publications == nil {
		return nil
	}
	return release.publications.packages
}

func (release preparedRelease) Close() {
	release.publications.Close()
	if release.changelog != "" {
		deletePath(release.changelog)
	}
}

func prepareRelease(cfg config, provider releaseProvider, group releaseGroup, changelogOnly bool) (preparedRelease, error) {
	prepared := preparedRelease{releaseGroup: group, changelogOnly: changelogOnly}
	packages := make([]string, 0, len(group.packages))
	nixVersions := make(map[string]string, len(group.packages))
	for _, plan := range group.packages {
		packages = append(packages, plan.pkg)
		nixVersions[plan.pkg] = plan.version
	}

	var err error
	prepared.publications, err = preparePackagePublications(cfg, provider, group.tag, packages, nixVersions)
	if err != nil {
		return prepared, err
	}
	prepared.changelog, err = gitChangelog(group.tag)
	if err != nil {
		return prepared, err
	}
	prepared.imageRepository = containerImageRepository(cfg.gitRepository, group.tag)
	if err := validateReleaseImageDestinations(cfg, prepared.imageRepository, group.tag, group.packages); err != nil {
		return prepared, err
	}
	return prepared, nil
}

// publishRelease verifies a prepared release's packages against their registries, then
// publishes its artifacts and packages, links them in the release, and cleans up old
// artifacts.
func publishRelease(cfg config, provider releaseProvider, release releaseClient, retain int, prepared preparedRelease, unpublished func(packagePublication) []string) error {
	tag := prepared.tag
	publications := prepared.publications
	if err := publications.verify(unpublished); err != nil {
		return err
	}
	imageRepository := prepared.imageRepository
	changelogOnly := prepared.changelogOnly
	session := releaseSession{cfg: cfg, client: release, tag: tag, changelog: prepared.changelog}

	// Native artifacts and package registries are published before cross-compiled
	// artifacts, so a slow cross build does not hold up the rest of the release.
	images := false
	var releaseErr error
	var publishErr error
	packagesPublished := false
	// Package publication is skipped after an artifact failure, and its own failure
	// is reported once the remaining artifacts are released.
	publishPackages := func() error {
		packagesPublished = true
		if publications != nil || changelogOnly {
			if err := session.ensureRelease(); err != nil {
				return err
			}
		}
		if releaseErr == nil {
			publishErr = publications.publish()
		}
		return nil
	}
	for _, tier := range releaseTiers(prepared.packages, runnerPlatform()) {
		if tier.distance > 0 && !packagesPublished {
			if err := publishPackages(); err != nil {
				return err
			}
		}

		imageRoots, err := prepareReleaseImages(tag, tier)
		if imageRoots != "" {
			defer deletePath(imageRoots)
		}
		if err != nil {
			return err
		}

		section("Publishing release artifacts%s", tier.suffix())
		for _, pkg := range tier.packages {
			item("%s", pkg.pkg)
			if err := releasePackage(cfg, imageRepository, release, tag, pkg, session.ensureRelease, &images); err != nil {
				releaseErr = errors.Join(releaseErr, fmt.Errorf("%s: %w", pkg.pkg, err))
			}
		}
	}
	if !packagesPublished {
		if err := publishPackages(); err != nil {
			return err
		}
	}
	if err := session.requireOutput(); err != nil {
		return err
	}
	if releaseErr != nil {
		return releaseErr
	}

	if images {
		section("Updating image manifest")
		detail("tag: %s", tag.version)
		if cfg.dryRun {
			status("dry run: skipping manifest update")
		} else if err := manifestUpdate(cfg, imageRepository, tag.version); err != nil {
			return fmt.Errorf("updating image manifest: %w", err)
		}
	}

	// Images are linked only once their combined manifest exists, and packages that
	// published before a failure are still linked.
	var links []releaseLink
	if images {
		links = append(links, containerReleaseLink(cfg, provider, imageRepository, tag.version))
	}
	links = append(links, publications.releaseLinks()...)
	if len(links) > 0 {
		linkReleasePackages(cfg, release, tag.full, links)
	}

	if publishErr != nil {
		return publishErr
	}

	if retain > 0 {
		section("Cleaning up old artifacts")
		switch {
		case cfg.dryRun:
			status("dry run: skipping cleanup")
		case !session.created:
			status("skipping cleanup because no new release was created")
		default:
			retainedTags, err := release.cleanupAssets(tag, retain)
			if err != nil {
				return fmt.Errorf("cleaning up old release assets: %w", err)
			}
			if images {
				if err := imageCleanupOld(cfg, imageRepository, tag.version, retainedTags); err != nil {
					return fmt.Errorf("cleaning up old images: %w", err)
				}
			}
		}
	}

	return nil
}

// linkReleasePackages adds the published packages and images to the release description.
// The release is already published by then, so a failure only warns.
func linkReleasePackages(cfg config, release releaseClient, tag string, links []releaseLink) {
	section("Linking packages in release")
	for _, link := range links {
		item("%s", link.text)
		if link.url != "" {
			detail("url: %s", link.url)
		}
	}
	if cfg.dryRun {
		status("dry run: skipping release update")
		return
	}
	if err := release.addReleaseLinks(tag, links); err != nil {
		warn("adding package links to release %s: %v", tag, err)
	}
}

// defaultPackages selects the current system's default package, or nothing when the
// flake provides no packages for the current system.
func defaultPackages(system func() (string, error), hasPackages func(string) (bool, error)) ([]string, error) {
	currentSystem, err := system()
	if err != nil {
		return nil, err
	}
	found, err := hasPackages(currentSystem)
	if err != nil {
		return nil, err
	}
	if !found {
		info("no packages found for %s; releasing changelog only", currentSystem)
		return nil, nil
	}
	return []string{"packages." + currentSystem + ".default"}, nil
}

// cachedNixSystem evaluates the current system at most once, logging it when first found.
func cachedNixSystem(lookup func() (string, error)) func() (string, error) {
	var system string
	var err error
	done := false
	return func() (string, error) {
		if !done {
			done = true
			system, err = lookup()
			if err == nil {
				info(dim("system: %s"), system)
			}
		}
		return system, err
	}
}

// resolvePackages allows the system to be omitted from package attribute paths, so
// packages.server.x86_64-unknown-linux-musl resolves to
// packages.<system>.server.x86_64-unknown-linux-musl when the former does not exist.
// Packages that cannot be resolved are returned unchanged so evaluation reports the error.
func resolvePackages(packages []string, exists func(string) bool, system func() (string, error)) []string {
	resolved := make([]string, 0, len(packages))
	for _, pkg := range packages {
		resolved = append(resolved, resolvePackage(pkg, exists, system))
	}
	return resolved
}

func resolvePackage(pkg string, exists func(string) bool, system func() (string, error)) string {
	output, rest, ok := strings.Cut(pkg, ".")
	if !ok || output == "" || rest == "" || exists(pkg) {
		return pkg
	}
	currentSystem, err := system()
	if err != nil || currentSystem == "" || rest == currentSystem || strings.HasPrefix(rest, currentSystem+".") {
		return pkg
	}
	candidate := output + "." + currentSystem + "." + rest
	if !exists(candidate) {
		return pkg
	}
	info(dim("package: %s -> %s"), pkg, candidate)
	return candidate
}

type releasePackagePlan struct {
	pkg           string
	storePath     string
	pname         string
	version       string
	mainProgram   string
	platform      platform
	imageName     string
	imageTag      string
	imageBuildErr error
	image         bool
}

type releasePackageLoader func(string) (releasePackagePlan, error)

func loadReleasePackage(pkg string) (releasePackagePlan, error) {
	storePath, err := nixPkgPath(pkg)
	if err != nil {
		return releasePackagePlan{}, err
	}
	return releasePackagePlan{
		pkg:         pkg,
		storePath:   storePath,
		pname:       nixPkgPname(pkg),
		version:     nixPkgVersion(pkg),
		mainProgram: nixPkgMainProgram(pkg),
		platform:    nixPkgPlatform(pkg),
		imageName:   nixImageName(pkg),
		imageTag:    nixImageTag(pkg),
	}, nil
}

func prepareReleasePackages(packages []string) []releasePackagePlan {
	return prepareReleasePackagesWith(packages, loadReleasePackage)
}

func prepareReleasePackagesWith(packages []string, load releasePackageLoader) []releasePackagePlan {
	section("Evaluating packages")
	storePaths := map[string]string{}
	plans := make([]releasePackagePlan, 0, len(packages))
	for _, pkg := range packages {
		item("%s", pkg)

		plan, err := load(pkg)
		if err != nil {
			itemWarn("evaluation failed: %v", err)
			continue
		}
		if original, exists := storePaths[plan.storePath]; exists {
			status("same store path as %s; skipping duplicate", original)
			continue
		}
		storePaths[plan.storePath] = pkg
		printReleasePackageDetails(plan)
		plans = append(plans, plan)
	}
	return plans
}

func printReleasePackageDetails(plan releasePackagePlan) {
	if plan.storePath != "" {
		detail("path: %s", plan.storePath)
	}
	if plan.pname != "" {
		detail("pname: %s", plan.pname)
	}
	if plan.version != "" {
		detail("version: %s", plan.version)
	}
	if plan.mainProgram != "" {
		detail("main program: %s", plan.mainProgram)
	}
	if value := platformDisplay(plan.platform); value != "" {
		detail("platform: %s", value)
	}
	if plan.imageName != "" {
		detail("image name: %s", plan.imageName)
	}
	if plan.imageTag != "" {
		detail("image tag: %s", plan.imageTag)
	}
}

func platformDisplay(value platform) string {
	switch {
	case value.OS != "" && value.Arch != "":
		return value.OS + "/" + value.Arch
	case value.OS != "":
		return value.OS
	default:
		return value.Arch
	}
}

func containerImageRepository(repository string, tag releaseTag) string {
	if repository == "" || tag.namespace == "" {
		return repository
	}
	return repository + "/" + tag.namespace
}

// releaseTier is a group of packages at the same platform distance from the runner.
type releaseTier struct {
	distance int
	packages []releasePackagePlan
}

func (tier releaseTier) suffix() string {
	switch tier.distance {
	case 0:
		return ""
	case 1:
		return " (cross-arch)"
	case 2:
		return " (cross-os)"
	default:
		return " (cross-os, cross-arch)"
	}
}

func runnerPlatform() platform {
	return platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// platformDistance orders target platforms by how likely they are to need slow
// cross-compilation on the runner: native, then same OS on another architecture,
// then another OS. An unknown OS or architecture is treated as native.
func platformDistance(target platform, runner platform) int {
	distance := 0
	if target.OS != "" && target.OS != runner.OS {
		distance += 2
	}
	if target.Arch != "" && target.Arch != runner.Arch {
		distance++
	}
	return distance
}

// releaseTiers groups packages by platform distance from the runner, nearest first,
// keeping the original package order within each tier.
func releaseTiers(packages []releasePackagePlan, runner platform) []releaseTier {
	var tiers []releaseTier
	for _, pkg := range packages {
		distance := platformDistance(pkg.platform, runner)
		index := slices.IndexFunc(tiers, func(tier releaseTier) bool { return tier.distance == distance })
		if index < 0 {
			tiers = append(tiers, releaseTier{distance: distance})
			index = len(tiers) - 1
		}
		tiers[index].packages = append(tiers[index].packages, pkg)
	}
	slices.SortStableFunc(tiers, func(a releaseTier, b releaseTier) int { return a.distance - b.distance })
	return tiers
}

func releaseImageCandidate(pkg releasePackagePlan, tag releaseTag) bool {
	return pkg.imageName != "" && pkg.imageTag != "" && pkg.platform.OS == "linux" && packageMatchesReleaseTag(pkg.version, pkg.imageTag, tag)
}

// validateReleaseImageDestinations checks every image destination before any
// package is built, since images in later tiers are built after earlier tiers publish.
func validateReleaseImageDestinations(cfg config, imageRepository string, tag releaseTag, packages []releasePackagePlan) error {
	for _, pkg := range packages {
		if !releaseImageCandidate(pkg, tag) {
			continue
		}
		if err := validateImagePackageDestination(cfg, imageRepository, tag, pkg); err != nil {
			return err
		}
	}
	return nil
}

func prepareReleaseImages(tag releaseTag, tier releaseTier) (string, error) {
	return prepareReleaseImagesWith(tag, tier, nixBuildLinked)
}

func prepareReleaseImagesWith(tag releaseTag, tier releaseTier, build func(string, string) error) (string, error) {
	rootDir := ""
	started := false
	packages := tier.packages
	for i := range packages {
		pkg := &packages[i]
		if !releaseImageCandidate(*pkg, tag) {
			continue
		}
		if !started {
			section("Preparing container images%s", tier.suffix())
			started = true
		}
		item("%s", pkg.pkg)
		if rootDir == "" {
			var err error
			rootDir, err = os.MkdirTemp("", "flake-release-roots-*")
			if err != nil {
				return "", err
			}
		}
		root := filepath.Join(rootDir, fmt.Sprintf("%d", i))
		status("building linked Nix output")
		if err := build(pkg.pkg, root); err != nil {
			pkg.imageBuildErr = err
			itemWarn("image preparation failed: %v", err)
			continue
		}
		pkg.image = publishableImagePath(pkg.storePath)
		if !pkg.image {
			status("built output is not a publishable container image")
			continue
		}
		status("ready for publication")
	}
	return rootDir, nil
}

func releasePackage(cfg config, imageRepository string, release releaseClient, tag releaseTag, pkg releasePackagePlan, ensureRelease func() error, images *bool) error {
	if pkg.imageBuildErr != nil {
		status("skipping image publication because preparation failed")
		return nil //nolint:nilerr // the preparation error was already reported
	}
	if pkg.image {
		if !imageTagMatchesReleaseTag(pkg.imageTag, tag) {
			itemWarn("image tag %q does not match git tag %q", pkg.imageTag, tag.version)
			return nil
		}
		return releaseImage(cfg, imageRepository, pkg.storePath, pkg.imageName, pkg.imageTag, ensureRelease, images)
	}
	if !packageVersionMatchesReleaseTag(pkg.version, tag) {
		itemWarn("package version %q does not match git tag %q", firstNonEmpty(pkg.version, pkg.imageTag), tag.version)
		return nil
	}
	if pkg.pname == "" {
		itemWarn("unknown package type")
		return nil
	}

	status("building package outputs")
	outputs, err := nixBuildOutputs(pkg.pkg)
	if err != nil {
		itemWarn("building package outputs failed: %v", err)
		return nil
	}
	for _, output := range outputs {
		detail("output %s: %s", output.Name, output.Path)
	}

	if cfg.bundleAppImage && pkg.mainProgram != "" && pkg.platform.OS == "linux" {
		path := packageMainProgramPath(outputs, pkg.mainProgram)
		switch {
		case path == "":
			itemWarn("main program %q was not found; archiving package outputs", pkg.mainProgram)
		case shouldBundleAppImage(cfg, pkg, path):
			status("bundling main program as AppImage")
			archivePath, err := nixBundleAppImage(pkg.pkg)
			if err != nil {
				itemWarn("AppImage bundling failed: %v", err)
				return nil
			}
			return uploadArchive(cfg, release, tag.full, archivePath, pkg.pname, pkg.version, pkg.platform.OS, pkg.platform.Arch, ensureRelease)
		}
	}

	status("archiving package outputs")
	return releasePackageAsset(cfg, release, tag.full, outputs, pkg.pname, pkg.version, pkg.platform.OS, pkg.platform.Arch, ensureRelease)
}

func packageMatchesReleaseTag(version string, imageTag string, tag releaseTag) bool {
	return packageVersionMatchesReleaseTag(version, tag) || imageTagMatchesReleaseTag(imageTag, tag)
}

func packageVersionMatchesReleaseTag(version string, tag releaseTag) bool {
	return version != "" && version == tag.version
}

func imageTagMatchesReleaseTag(imageTag string, tag releaseTag) bool {
	return imageTag != "" && imageTag == tag.version
}

func validateImagePackageDestination(cfg config, imageRepository string, tag releaseTag, pkg releasePackagePlan) error {
	if !imageTagMatchesReleaseTag(pkg.imageTag, tag) {
		return nil
	}
	if err := validateImageDestination(cfg, imageRepository, pkg.imageTag); err != nil {
		return fmt.Errorf("validating container image destination for %s (%s:%s): %w", pkg.pkg, pkg.imageName, pkg.imageTag, err)
	}
	return nil
}

func publishableImagePath(path string) bool {
	return isFile(path) && (strings.HasSuffix(path, ".tar.gz") || executable(path))
}

func releaseImage(cfg config, imageRepository string, storePath string, imageName string, imageTag string, ensureRelease func() error, images *bool) error {
	detail("image: %s", imageName+":"+imageTag)

	imagePath := storePath
	if strings.HasSuffix(storePath, ".tar.gz") {
		detail("type: buildLayeredImage")
	} else if executable(storePath) {
		detail("type: streamLayeredImage")
		status("compressing streamed image")
		var err error
		imagePath, err = imageGzip(storePath)
		if err != nil {
			return err
		}
	} else {
		itemWarn("could not determine image type")
		return nil
	}

	arch, err := imageArch(imagePath)
	if err != nil {
		return err
	}
	detail("architecture: %s", arch)
	if err := validateImageDestination(cfg, imageRepository, imageTag+"-"+arch); err != nil {
		return fmt.Errorf("validating container image destination for %s:%s: %w", imageName, imageTag, err)
	}
	*images = true
	if err := ensureRelease(); err != nil {
		return err
	}

	if imageExists(cfg, imageRepository, imageTag, arch) {
		status("image already exists; skipping upload")
		return nil
	}

	if cfg.dryRun {
		status("dry run: skipping image upload")
		return nil
	}
	if err := imageUpload(cfg, imageRepository, imagePath, imageTag, arch); err != nil {
		return fmt.Errorf("uploading image %s:%s: %w", imageName, imageTag, err)
	}
	return nil
}

func releasePackageAsset(cfg config, release releaseClient, tag string, outputs []packageOutput, pname string, version string, osName string, archName string, ensureRelease func() error) error {
	stem, err := assetStem(pname, version, osName, archName)
	if err != nil {
		return err
	}
	archivePath, err := archiveOutputs(outputs, stem, osName, archName)
	if err != nil {
		itemWarn("archiving package outputs failed: %v", err)
		return nil
	}
	defer deletePath(filepath.Dir(archivePath))
	return uploadArchive(cfg, release, tag, archivePath, pname, version, osName, archName, ensureRelease)
}

func uploadArchive(cfg config, release releaseClient, tag string, archivePath string, pname string, version string, osName string, archName string, ensureRelease func() error) error {
	asset, err := renameAsset(archivePath, pname, version, osName, archName)
	if err != nil {
		return err
	}
	defer func() {
		deletePath(asset)
		_ = os.Remove(filepath.Dir(asset))
	}()
	detail("asset: %s", filepath.Base(asset))
	if err := ensureRelease(); err != nil {
		return err
	}

	if cfg.dryRun {
		status("dry run: skipping asset upload")
		return nil
	}
	if err := release.uploadAsset(tag, asset); err != nil {
		return fmt.Errorf("uploading asset %s: %w", filepath.Base(asset), err)
	}
	return nil
}

func isFile(path string) bool {
	stat, err := os.Stat(filepath.Clean(path))
	return err == nil && stat.Mode().IsRegular()
}

func shouldBundleAppImage(cfg config, pkg releasePackagePlan, path string) bool {
	return cfg.bundleAppImage && pkg.mainProgram != "" && pkg.platform.OS == "linux" && path != "" && !isNativeBinary(path)
}

func packageMainProgramPath(outputs []packageOutput, mainProgram string) string {
	for _, outputName := range []string{"bin", "out"} {
		for _, output := range outputs {
			if output.Name != outputName {
				continue
			}
			path := filepath.Join(output.Path, "bin", mainProgram)
			if isFile(path) {
				return path
			}
		}
	}
	return ""
}
