package flakerelease

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type config struct {
	dryRun                    bool
	bundleAppImage            bool
	deleteOldReleaseArtifacts string
	githubRepository          string
	githubServerURL           string
	githubActor               string
	githubToken               string
	registry                  string
	registryUsername          string
	registryPassword          string
	publishPackages           string
	packageRegistryOwner      string
	packageRegistryURL        string
	packageRegistryToken      string
	packageRegistryUsername   string
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
	if err := session.client.createRelease(session.tag.full, session.changelog); err != nil {
		session.creationErr = fmt.Errorf("creating release %s: %w", session.tag.full, err)
		return session.creationErr
	}
	session.created = true
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

func selectedReleaseTag() (string, error) {
	if tag := os.Getenv("TAG"); tag != "" {
		return tag, nil
	}

	refName := os.Getenv("GITHUB_REF_NAME")
	ref := os.Getenv("GITHUB_REF")
	if refName != "" || ref != "" {
		if refName == "" || ref != "refs/tags/"+refName {
			return "", fmt.Errorf("release event must identify one exact tag; got GITHUB_REF_NAME=%q GITHUB_REF=%q", refName, ref)
		}
		return refName, nil
	}
	return gitLatestTag()
}

func configFromEnv() config {
	return config{
		dryRun:                    os.Getenv("DRY_RUN") == "true",
		bundleAppImage:            truthy(os.Getenv("BUNDLE_APPIMAGE")),
		deleteOldReleaseArtifacts: os.Getenv("DELETE_OLD_RELEASE_ARTIFACTS"),
		githubRepository:          os.Getenv("GITHUB_REPOSITORY"),
		githubServerURL:           os.Getenv("GITHUB_SERVER_URL"),
		githubActor:               os.Getenv("GITHUB_ACTOR"),
		githubToken:               os.Getenv("GITHUB_TOKEN"),
		registry:                  os.Getenv("REGISTRY"),
		registryUsername:          os.Getenv("REGISTRY_USERNAME"),
		registryPassword:          os.Getenv("REGISTRY_PASSWORD"),
		publishPackages:           os.Getenv("PUBLISH_PACKAGES"),
		packageRegistryOwner:      os.Getenv("PACKAGE_REGISTRY_OWNER"),
		packageRegistryURL:        os.Getenv("PACKAGE_REGISTRY_URL"),
		packageRegistryToken:      os.Getenv("PACKAGE_REGISTRY_TOKEN"),
		packageRegistryUsername:   os.Getenv("PACKAGE_REGISTRY_USERNAME"),
	}
}

func parseRunArgs(cfg *config, args []string) ([]string, bool) {
	var packages []string
	for _, arg := range args {
		switch arg {
		case "--help":
			return packages, true
		case "--dry-run":
			cfg.dryRun = true
		case "--bundle-appimage":
			cfg.bundleAppImage = true
		default:
			packages = append(packages, arg)
		}
	}
	return packages, false
}

func Run(args []string) error {
	cfg := configFromEnv()
	packages, help := parseRunArgs(&cfg, args)
	if help {
		info("Usage: flake-release [packages...] [--dry-run] [--bundle-appimage]")
		info("")
		info("If no packages are provided as arguments, the command will attempt to get packages from the nix flake for the current system.")
		return nil
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
	if cfg.githubRepository == "" {
		if repository := gitRepositoryFromOrigin(origin); repository != "" {
			cfg.githubRepository = repository
			_ = os.Setenv("GITHUB_REPOSITORY", cfg.githubRepository)
		}
	}
	info("git repository: %s", firstNonEmpty(cfg.githubRepository, "<none>"))
	if cfg.githubServerURL == "" {
		if serverURL := gitServerURLFromOrigin(origin); serverURL != "" {
			cfg.githubServerURL = serverURL
			_ = os.Setenv("GITHUB_SERVER_URL", cfg.githubServerURL)
		}
	}
	info("git server: %s", firstNonEmpty(cfg.githubServerURL, "<none>"))

	provider, err := releaseType(origin)
	if err != nil {
		return err
	}
	info("git type: %s", provider)

	selectedTag, err := selectedReleaseTag()
	if err != nil {
		return err
	}
	tag, err := parseSelectedReleaseTag(selectedTag)
	if err != nil {
		return err
	}
	info("git tag: %s", tag.full)

	if cfg.githubActor == "" {
		cfg.githubActor, err = gitUser()
		if err != nil {
			return err
		}
		_ = os.Setenv("GITHUB_ACTOR", cfg.githubActor)
	}
	info("git user: %s", cfg.githubActor)

	if cfg.registryUsername == "" {
		cfg.registryUsername, err = gitUser()
		if err != nil {
			return err
		}
		_ = os.Setenv("REGISTRY_USERNAME", cfg.registryUsername)
	}
	info("registry user: %s", cfg.registryUsername)

	if cfg.registryPassword == "" && cfg.githubToken != "" {
		cfg.registryPassword = cfg.githubToken
		_ = os.Setenv("REGISTRY_PASSWORD", cfg.registryPassword)
	}

	if cfg.registry == "" && provider == releaseGitHub {
		cfg.registry = "ghcr.io"
		_ = os.Setenv("REGISTRY", cfg.registry)
	}
	info("registry: %s", firstNonEmpty(cfg.registry, "<none>"))

	applyPackageRegistryDefaults(&cfg, provider)
	if cfg.publishPackages != "" {
		info("package registry owner: %s", firstNonEmpty(cfg.packageRegistryOwner, "<none>"))
		info("package registry: %s", packageRegistryDisplayURL(cfg.packageRegistryURL))
		info("package registry user: %s", firstNonEmpty(cfg.packageRegistryUsername, "<none>"))
	}

	if len(packages) == 0 {
		system, systemErr := nixSystem()
		if systemErr != nil {
			return systemErr
		}
		info(dim("system: %s"), system)
		packages = append(packages, "packages."+system+".default")
	}
	releasePackages := prepareReleasePackages(packages)

	publications, err := preparePackagePublications(cfg, provider, tag, packages)
	if err != nil {
		return err
	}
	if publications != nil {
		defer publications.Close()
	}

	release := newReleaseClient(provider, cfg)
	changelog, err := gitChangelog(tag)
	if err != nil {
		return err
	}
	defer deletePath(changelog)
	imageRepository := containerImageRepository(cfg.githubRepository, tag)
	imageRoots, err := prepareReleaseImages(cfg, imageRepository, tag, releasePackages)
	if imageRoots != "" {
		defer deletePath(imageRoots)
	}
	if err != nil {
		return err
	}
	session := releaseSession{cfg: cfg, client: release, tag: tag, changelog: changelog}

	images := false
	var releaseErr error
	section("Publishing release artifacts")
	for _, pkg := range releasePackages {
		item("%s", pkg.pkg)
		if err := releasePackage(cfg, imageRepository, release, tag, pkg, session.ensureRelease, &images); err != nil {
			releaseErr = errors.Join(releaseErr, fmt.Errorf("%s: %w", pkg.pkg, err))
		}
	}
	if publications != nil {
		if err := session.ensureRelease(); err != nil {
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

	if err := publications.publish(); err != nil {
		return err
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

func prepareReleaseImages(cfg config, imageRepository string, tag releaseTag, packages []releasePackagePlan) (string, error) {
	return prepareReleaseImagesWith(cfg, imageRepository, tag, packages, nixBuildLinked)
}

func prepareReleaseImagesWith(cfg config, imageRepository string, tag releaseTag, packages []releasePackagePlan, build func(string, string) error) (string, error) {
	rootDir := ""
	started := false
	for i := range packages {
		pkg := &packages[i]
		if pkg.imageName == "" || pkg.imageTag == "" || pkg.platform.OS != "linux" || !packageMatchesReleaseTag(pkg.version, pkg.imageTag, tag) {
			continue
		}
		if !started {
			section("Preparing container images")
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
		if err := validateImagePackageDestination(cfg, imageRepository, tag, *pkg); err != nil {
			return rootDir, err
		}
	}
	return rootDir, nil
}

func releasePackage(cfg config, imageRepository string, release releaseClient, tag releaseTag, pkg releasePackagePlan, ensureRelease func() error, images *bool) error {
	if pkg.imageBuildErr != nil {
		status("skipping image publication because preparation failed")
		return nil
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
	if !pkg.image || !imageTagMatchesReleaseTag(pkg.imageTag, tag) {
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
	archivePath, err := archiveOutputs(outputs, osName, archName)
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
