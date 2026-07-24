package flakerelease

import (
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

	tag := os.Getenv("TAG")
	if tag == "" {
		tag, err = gitLatestTag()
		if err != nil {
			return err
		}
	}
	info("git tag: %s", tag)

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
	release := newReleaseClient(provider, cfg)

	changelog, err := gitChangelog(tag)
	if err != nil {
		return err
	}
	defer deletePath(changelog)

	releaseCreated := false
	if cfg.dryRun {
		info("dry run: skipping release creation")
	} else if err := release.createRelease(tag, changelog); err != nil {
		warn("could not create release %s", tag)
	} else {
		releaseCreated = true
	}

	if len(packages) == 0 {
		system, err := nixSystem()
		if err != nil {
			return err
		}
		packages = append(packages, "packages."+system+".default")
	}

	images := false
	storePaths := map[string]bool{}
	for _, pkg := range packages {
		if err := releasePackage(cfg, release, tag, pkg, storePaths, &images); err != nil {
			warn("%v", err)
		}
	}

	info("")
	if images {
		if cfg.dryRun {
			info("dry run: skipping manifest update")
		} else {
			info("updating image manifest for tag %s", bold(tagVersion(tag)))
			if err := manifestUpdate(cfg, tagVersion(tag)); err != nil {
				warn("%v", err)
			}
		}
	}

	if truthy(cfg.deleteOldReleaseArtifacts) {
		switch {
		case cfg.dryRun:
			info("dry run: skipping old release artifact cleanup")
		case !releaseCreated:
			info("old release artifact cleanup requested, but no new release was created")
		default:
			if err := release.cleanupAssets(tag); err != nil {
				warn("old release asset cleanup failed")
			}
			if images {
				if err := imageCleanupOld(cfg, tagVersion(tag)); err != nil {
					warn("old image cleanup failed")
				}
			}
		}
	}

	return nil
}

func releasePackage(cfg config, release releaseClient, tag string, pkg string, storePaths map[string]bool, images *bool) error {
	info("")
	info("evaluating %s", bold(pkg))

	storePath, err := nixPkgPath(pkg)
	if err != nil {
		return err
	}
	if storePaths[storePath] {
		info("%s: already built, skipping", pkg)
		return nil
	}
	storePaths[storePath] = true

	pname := nixPkgPname(pkg)
	version := nixPkgVersion(pkg)
	mainProgram := nixPkgMainProgram(pkg)
	p := nixPkgPlatform(pkg)
	imageName := nixImageName(pkg)
	imageTag := nixImageTag(pkg)

	if version != tagVersion(tag) && imageTag != tagVersion(tag) {
		warn("package version '%s' does not match git tag '%s'", firstNonEmpty(version, imageTag), tagVersion(tag))
		return nil
	}

	if imageName != "" && imageTag != "" && p.OS == "linux" {
		if err := nixBuild(pkg); err != nil {
			warn("build failed")
			return nil
		}
		if isFile(storePath) {
			return releaseImage(cfg, storePath, imageName, imageTag, images)
		}
	}

	if pname == "" || version == "" {
		warn("unknown package type")
		return nil
	}

	outputs, err := nixBuildOutputs(pkg)
	if err != nil {
		warn("building package outputs failed")
		return nil
	}

	if mainProgram != "" && p.OS == "linux" {
		path := packageMainProgramPath(outputs, mainProgram)
		switch {
		case path == "":
			warn("main program %q was not found; archiving package outputs", mainProgram)
		case !isNativeBinary(path):
			info("main program is not a native binary, bundling as AppImage")
			archivePath, err := nixBundleAppImage(pkg)
			if err != nil {
				warn("bundling failed")
				return nil
			}
			return uploadArchive(cfg, release, tag, archivePath, pname, version, p.OS, p.Arch)
		}
	}

	info("archiving all package outputs")
	return releasePackageAsset(cfg, release, tag, outputs, pname, version, p.OS, p.Arch)
}

func releaseImage(cfg config, storePath string, imageName string, imageTag string, images *bool) error {
	info("detected as image %s", bold(imageName+":"+imageTag))
	*images = true

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

	if imageExists(cfg, imageTag, arch) {
		warn("image already exists, skipping upload")
		return nil
	}

	if cfg.dryRun {
		info("dry run: skipping image upload")
		return nil
	}
	if err := imageUpload(cfg, imagePath, imageTag, arch); err != nil {
		warn("upload failed: %v", err)
		return nil
	}
	return nil
}

func releasePackageAsset(cfg config, release releaseClient, tag string, outputs []packageOutput, pname string, version string, osName string, archName string) error {
	archivePath, err := archiveOutputs(outputs, osName, archName)
	if err != nil {
		warn("archiving package outputs failed")
		return nil
	}
	defer deletePath(filepath.Dir(archivePath))
	return uploadArchive(cfg, release, tag, archivePath, pname, version, osName, archName)
}

func uploadArchive(cfg config, release releaseClient, tag string, archivePath string, pname string, version string, osName string, archName string) error {
	asset, err := renameAsset(archivePath, pname, version, osName, archName)
	if err != nil {
		return err
	}
	defer func() {
		deletePath(asset)
		_ = os.Remove(filepath.Dir(asset))
	}()

	if cfg.dryRun {
		info("dry run: skipping upload")
		return nil
	}
	if err := release.uploadAsset(tag, asset); err != nil {
		warn("uploading failed")
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
