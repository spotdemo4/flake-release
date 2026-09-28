package flakerelease

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const cargoRegistryName = "flake-release"

type cargoMetadata struct {
	Packages []struct {
		Name         string    `json:"name"`
		Version      string    `json:"version"`
		ManifestPath string    `json:"manifest_path"`
		Publish      *[]string `json:"publish"`
	} `json:"packages"`
}

type npmPackageDocument struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Private bool   `json:"private"`
}

func preflightCargoPackage(set *packagePublicationSet, publication *packagePublication) error {
	if err := set.commands.require("cargo"); err != nil {
		return err
	}
	output, err := set.commands.capture(commandOptions{
		name: "cargo",
		args: []string{"metadata", "--no-deps", "--format-version", "1", "--manifest-path", publication.manifest},
		dir:  publication.dir,
	})
	if err != nil {
		return err
	}
	var metadata cargoMetadata
	if err := json.Unmarshal([]byte(output), &metadata); err != nil {
		return fmt.Errorf("parsing cargo metadata: %w", err)
	}
	manifest, err := filepath.Abs(publication.manifest)
	if err != nil {
		return err
	}
	for _, pkg := range metadata.Packages {
		pkgManifest, err := filepath.Abs(pkg.ManifestPath)
		if err != nil {
			continue
		}
		if filepath.Clean(pkgManifest) == filepath.Clean(manifest) {
			if pkg.Publish != nil && len(*pkg.Publish) == 0 {
				return fmt.Errorf("cargo package %s sets publish = false: %w", pkg.Name, errPackageNotPublishable)
			}
			publication.name = pkg.Name
			publication.version = pkg.Version
			break
		}
	}
	if publication.name == "" || publication.version == "" {
		return fmt.Errorf("root cargo manifest does not define a [package]: %w", errPackageNotPublishable)
	}
	if err := requireStrictPackageVersion(publication.version); err != nil {
		return fmt.Errorf("cargo package: %w", err)
	}
	return set.commands.run(cargoCommand(set, publication, true))
}

func publishCargoPackage(set *packagePublicationSet, publication *packagePublication) error {
	return set.commands.run(cargoCommand(set, publication, false))
}

func cargoCommand(set *packagePublicationSet, publication *packagePublication, dryRun bool) commandOptions {
	args := []string{"publish", "--allow-dirty", "--registry", cargoRegistryName, "--manifest-path", publication.manifest}
	if dryRun {
		args = append(args, "--dry-run")
	}
	env := []string{"CARGO_REGISTRIES_FLAKE_RELEASE_INDEX=sparse+" + set.registryURL(packageCargo)}
	if set.cfg.packageRegistryToken != "" {
		env = append(env, "CARGO_REGISTRIES_FLAKE_RELEASE_TOKEN=Bearer "+set.cfg.packageRegistryToken)
	}
	return commandOptions{
		name:    "cargo",
		args:    args,
		dir:     publication.dir,
		env:     env,
		secrets: []string{set.cfg.packageRegistryToken},
	}
}

func preflightNPMPackage(set *packagePublicationSet, publication *packagePublication) error {
	if err := set.commands.require("npm"); err != nil {
		return err
	}
	data, err := os.ReadFile(publication.manifest)
	if err != nil {
		return err
	}
	var manifest npmPackageDocument
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parsing package.json: %w", err)
	}
	if manifest.Private {
		return fmt.Errorf("package.json is marked private: %w", errPackageNotPublishable)
	}
	if manifest.Name == "" || manifest.Version == "" {
		return fmt.Errorf("package.json requires name and version")
	}
	if err := requireStrictPackageVersion(manifest.Version); err != nil {
		return fmt.Errorf("npm package: %w", err)
	}
	if set.provider == releaseGitHub {
		expectedScope := "@" + strings.ToLower(set.cfg.packageRegistryOwner) + "/"
		if manifest.Name != strings.ToLower(manifest.Name) || !strings.HasPrefix(manifest.Name, expectedScope) || len(manifest.Name) == len(expectedScope) {
			return fmt.Errorf("GitHub npm package %q must be lowercase and use the %s scope", manifest.Name, strings.TrimSuffix(expectedScope, "/"))
		}
	}
	publication.name = manifest.Name
	publication.version = manifest.Version

	if isFile(filepath.Join(publication.dir, "package-lock.json")) {
		if err := set.commands.run(commandOptions{name: "npm", args: []string{"ci"}, dir: publication.dir}); err != nil {
			return err
		}
	}

	npmrc, err := set.npmConfig()
	if err != nil {
		return err
	}
	return set.commands.run(npmCommand(set, publication, npmrc, true))
}

func publishNPMPackage(set *packagePublicationSet, publication *packagePublication) error {
	npmrc, err := set.npmConfig()
	if err != nil {
		return err
	}
	return set.commands.run(npmCommand(set, publication, npmrc, false))
}

func npmCommand(set *packagePublicationSet, publication *packagePublication, npmrc string, dryRun bool) commandOptions {
	args := []string{"publish", "--registry", set.registryURL(packageNPM)}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return commandOptions{
		name:    "npm",
		args:    args,
		dir:     publication.dir,
		env:     []string{"NPM_CONFIG_USERCONFIG=" + npmrc},
		secrets: []string{set.cfg.packageRegistryToken},
	}
}

func (set *packagePublicationSet) npmConfig() (string, error) {
	path := filepath.Join(set.temporaryDir, ".npmrc")
	if isFile(path) {
		return path, nil
	}
	registry := set.registryURL(packageNPM)
	parsed, err := url.Parse(registry)
	if err != nil {
		return "", err
	}
	contents := fmt.Sprintf("registry=%s\n", registry)
	if set.cfg.packageRegistryToken != "" {
		authPath := strings.TrimRight(parsed.EscapedPath(), "/") + "/"
		contents += fmt.Sprintf("//%s%s:_authToken=%s\nalways-auth=true\n", parsed.Host, authPath, set.cfg.packageRegistryToken)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// pyPITool reports the tool used to build and upload PyPI distributions: uv when it
// is on PATH, then python3 when it has the build and twine modules, then uv from nixpkgs.
func pyPITool(set *packagePublicationSet) (string, error) {
	if set.commands.available("uv") {
		return "uv", nil
	}
	if set.commands.available("python3") && set.commands.run(commandOptions{name: "python3", args: []string{"-c", "import build, twine"}}) == nil {
		return "python3", nil
	}
	if err := set.commands.require("uv"); err != nil {
		return "", fmt.Errorf("pypi publishing requires uv, or python3 with the build and twine modules: %w", err)
	}
	return "uv", nil
}

func preflightPyPIPackage(set *packagePublicationSet, publication *packagePublication) error {
	tool, err := pyPITool(set)
	if err != nil {
		return err
	}

	artifactDir, err := os.MkdirTemp(set.temporaryDir, "pypi-")
	if err != nil {
		return err
	}
	buildArgs := []string{"-m", "build", "--outdir", artifactDir, publication.dir}
	if tool == "uv" {
		buildArgs = []string{"build", "--out-dir", artifactDir, publication.dir}
	}
	if err := set.commands.run(commandOptions{name: tool, args: buildArgs, dir: publication.dir}); err != nil {
		return err
	}
	files, err := findFiles(artifactDir)
	if err != nil {
		return err
	}
	// uv also writes a .gitignore into the output directory.
	var artifacts []string
	for _, file := range files {
		if isPyPIDistribution(file) {
			artifacts = append(artifacts, file)
		}
	}
	if len(artifacts) == 0 {
		return fmt.Errorf("python build produced no package artifacts")
	}
	name, version, err := pyPIArtifactIdentity(artifacts)
	if err != nil {
		return err
	}
	if err := requireStrictPackageVersion(version); err != nil {
		return fmt.Errorf("pypi package: %w", err)
	}
	publication.name = name
	publication.version = version
	publication.artifacts = artifacts
	checkArgs := []string{"-m", "twine", "check"}
	if tool == "uv" {
		// A dry run validates the distributions without contacting the registry.
		checkArgs = []string{"publish", "--dry-run", "--trusted-publishing", "never", "--publish-url", strings.TrimRight(set.registryURL(packagePyPI), "/")}
	}
	return set.commands.run(commandOptions{name: tool, args: append(checkArgs, artifacts...), dir: publication.dir})
}

func publishPyPIPackage(set *packagePublicationSet, publication *packagePublication) error {
	if len(publication.artifacts) == 0 {
		return fmt.Errorf("pypi package artifacts were not prepared")
	}
	tool, err := pyPITool(set)
	if err != nil {
		return err
	}
	registry := strings.TrimRight(set.registryURL(packagePyPI), "/")
	args := []string{"-m", "twine", "upload", "--non-interactive", "--repository-url", registry}
	env := []string{
		"TWINE_USERNAME=" + set.cfg.packageRegistryUsername,
		"TWINE_PASSWORD=" + set.cfg.packageRegistryToken,
	}
	if tool == "uv" {
		args = []string{"publish", "--trusted-publishing", "never", "--publish-url", registry}
		env = []string{
			"UV_PUBLISH_USERNAME=" + set.cfg.packageRegistryUsername,
			"UV_PUBLISH_PASSWORD=" + set.cfg.packageRegistryToken,
		}
	}
	return set.commands.run(commandOptions{
		name:    tool,
		args:    append(args, publication.artifacts...),
		dir:     publication.dir,
		env:     env,
		secrets: []string{set.cfg.packageRegistryToken},
	})
}
