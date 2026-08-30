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

var errScopedImageRelease = errors.New("container image outputs cannot be published with scoped tags; use an unscoped TAG or exclude the image output")

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
		info("dry run: skipping release creation")
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

func Run(args []string) error {
	setupNixConfig()

	cfg := config{
		dryRun:                    os.Getenv("DRY_RUN") == "true",
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

	var packages []string
	for _, arg := range args {
		switch arg {
		case "--help":
			info("Usage: flake-release [packages...] [--dry-run]")
			info("")
			info("If no packages are provided as arguments, the command will attempt to get packages from the nix flake for the current system.")
			return nil
		case "--dry-run":
			cfg.dryRun = true
		default:
			packages = append(packages, arg)
		}
	}
	packages = append(packages, splitPackages(os.Getenv("PACKAGES"))...)

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
	imageRoots, err := prepareReleaseImages(tag, releasePackages)
	if imageRoots != "" {
		defer deletePath(imageRoots)
	}
	if err != nil {
		return err
	}
	session := releaseSession{cfg: cfg, client: release, tag: tag, changelog: changelog}

	images := false
	var releaseErr error
	for _, pkg := range releasePackages {
		if err := releasePackage(cfg, release, tag, pkg, session.ensureRelease, &images); err != nil {
			warn("%v", err)
			releaseErr = errors.Join(releaseErr, err)
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

	info("")
	if images {
		if cfg.dryRun {
			info("dry run: skipping manifest update")
		} else {
			info("updating image manifest for tag %s", bold(tag.version))
			if err := manifestUpdate(cfg, tag.version); err != nil {
				return fmt.Errorf("updating image manifest: %w", err)
			}
		}
	}

	if err := publications.publish(); err != nil {
		return err
	}

	if truthy(cfg.deleteOldReleaseArtifacts) {
		switch {
		case cfg.dryRun:
			info("dry run: skipping old release artifact cleanup")
		case !session.created:
			info("old release artifact cleanup requested, but no new release was created")
		default:
			if err := release.cleanupAssets(tag); err != nil {
				return fmt.Errorf("cleaning up old release assets: %w", err)
			}
			if images {
				if err := imageCleanupOld(cfg, tag.version); err != nil {
					return fmt.Errorf("cleaning up old images: %w", err)
				}
			}
		}
	}

	return nil
}

type releasePackagePlan struct {
	pkg              string
	storePath        string
	pname            string
	version          string
	mainProgram      string
	platform         platform
	imageName        string
	imageTag         string
	imageBuildFailed bool
	image            bool
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
	storePaths := map[string]bool{}
	plans := make([]releasePackagePlan, 0, len(packages))
	for _, pkg := range packages {
		info("")
		info("evaluating %s", bold(pkg))

		plan, err := load(pkg)
		if err != nil {
			warn("%v", err)
			continue
		}
		if storePaths[plan.storePath] {
			info("%s: already built, skipping", pkg)
			continue
		}
		storePaths[plan.storePath] = true
		plans = append(plans, plan)
	}
	return plans
}

func prepareReleaseImages(tag releaseTag, packages []releasePackagePlan) (string, error) {
	rootDir := ""
	for i := range packages {
		pkg := &packages[i]
		if pkg.imageName == "" || pkg.imageTag == "" || pkg.platform.OS != "linux" || !packageMatchesReleaseTag(pkg.version, pkg.imageTag, tag) {
			continue
		}
		if rootDir == "" {
			var err error
			rootDir, err = os.MkdirTemp("", "flake-release-roots-*")
			if err != nil {
				return "", err
			}
		}
		root := filepath.Join(rootDir, fmt.Sprintf("%d", i))
		if err := nixBuildLinked(pkg.pkg, root); err != nil {
			pkg.imageBuildFailed = true
			continue
		}
		pkg.image = publishableImagePath(pkg.storePath)
		if err := validateScopedImagePackage(tag, *pkg); err != nil {
			return rootDir, err
		}
	}
	return rootDir, nil
}

func releasePackage(cfg config, release releaseClient, tag releaseTag, pkg releasePackagePlan, ensureRelease func() error, images *bool) error {
	if pkg.imageBuildFailed {
		warn("build failed")
		return nil
	}
	if pkg.image {
		if !imageTagMatchesReleaseTag(pkg.imageTag, tag) {
			warn("image tag '%s' does not match git tag '%s'", pkg.imageTag, tag.version)
			return nil
		}
		return releaseImage(cfg, pkg.storePath, pkg.imageName, pkg.imageTag, ensureRelease, images)
	}
	if !packageVersionMatchesReleaseTag(pkg.version, tag) {
		warn("package version '%s' does not match git tag '%s'", firstNonEmpty(pkg.version, pkg.imageTag), tag.version)
		return nil
	}
	if pkg.pname == "" {
		warn("unknown package type")
		return nil
	}

	outputs, err := nixBuildOutputs(pkg.pkg)
	if err != nil {
		warn("building package outputs failed")
		return nil
	}

	if pkg.mainProgram != "" && pkg.platform.OS == "linux" {
		path := packageMainProgramPath(outputs, pkg.mainProgram)
		switch {
		case path == "":
			warn("main program %q was not found; archiving package outputs", pkg.mainProgram)
		case !isNativeBinary(path):
			info("main program is not a native binary, bundling as AppImage")
			archivePath, err := nixBundleAppImage(pkg.pkg)
			if err != nil {
				warn("bundling failed")
				return nil
			}
			return uploadArchive(cfg, release, tag.full, archivePath, pkg.pname, pkg.version, pkg.platform.OS, pkg.platform.Arch, ensureRelease)
		}
	}

	info("archiving all package outputs")
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

func validateScopedImagePackage(tag releaseTag, pkg releasePackagePlan) error {
	if tag.namespace == "" || !pkg.image {
		return nil
	}
	return fmt.Errorf("%w: %s (%s:%s)", errScopedImageRelease, pkg.pkg, pkg.imageName, pkg.imageTag)
}

func publishableImagePath(path string) bool {
	return isFile(path) && (strings.HasSuffix(path, ".tar.gz") || executable(path))
}

func releaseImage(cfg config, storePath string, imageName string, imageTag string, ensureRelease func() error, images *bool) error {
	info("detected as image %s", bold(imageName+":"+imageTag))

	imagePath := storePath
	if strings.HasSuffix(storePath, ".tar.gz") {
		info("image type: buildLayeredImage")
	} else if executable(storePath) {
		info("image type: streamLayeredImage, zipping")
		var err error
		imagePath, err = imageGzip(storePath)
		if err != nil {
			return err
		}
	} else {
		warn("could not determine image type")
		return nil
	}

	arch, err := imageArch(imagePath)
	if err != nil {
		return err
	}
	info("image arch: %s", arch)
	*images = true
	if err := ensureRelease(); err != nil {
		return err
	}

	if imageExists(cfg, imageTag, arch) {
		warn("image already exists, skipping upload")
		return nil
	}

	if cfg.dryRun {
		info("dry run: skipping image upload")
		return nil
	}
	if err := imageUpload(cfg, imagePath, imageTag, arch); err != nil {
		return fmt.Errorf("uploading image %s:%s: %w", imageName, imageTag, err)
	}
	return nil
}

func releasePackageAsset(cfg config, release releaseClient, tag string, outputs []packageOutput, pname string, version string, osName string, archName string, ensureRelease func() error) error {
	archivePath, err := archiveOutputs(outputs, osName, archName)
	if err != nil {
		warn("archiving package outputs failed")
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
	if err := ensureRelease(); err != nil {
		return err
	}

	if cfg.dryRun {
		info("dry run: skipping upload")
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
