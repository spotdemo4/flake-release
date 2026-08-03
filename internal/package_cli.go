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
		Name         string `json:"name"`
		Version      string `json:"version"`
		ManifestPath string `json:"manifest_path"`
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
			publication.name = pkg.Name
			publication.version = pkg.Version
			break
		}
	}
	if publication.name == "" || publication.version == "" {
		return fmt.Errorf("root cargo manifest does not define a publishable [package]")
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
		return fmt.Errorf("package.json is marked private")
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

func preflightPyPIPackage(set *packagePublicationSet, publication *packagePublication) error {
	if err := set.commands.require("python3"); err != nil {
		return err
	}

	artifactDir, err := os.MkdirTemp(set.temporaryDir, "pypi-")
	if err != nil {
		return err
	}
	if err := set.commands.run(commandOptions{
		name: "python3",
		args: []string{"-m", "build", "--outdir", artifactDir, publication.dir},
		dir:  publication.dir,
	}); err != nil {
		return err
	}
	artifacts, err := findFiles(artifactDir)
	if err != nil {
		return err
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
	args := append([]string{"-m", "twine", "check"}, artifacts...)
	return set.commands.run(commandOptions{name: "python3", args: args, dir: publication.dir})
}

func publishPyPIPackage(set *packagePublicationSet, publication *packagePublication) error {
	if len(publication.artifacts) == 0 {
		return fmt.Errorf("pypi package artifacts were not prepared")
	}
	args := []string{"-m", "twine", "upload", "--non-interactive", "--repository-url", strings.TrimRight(set.registryURL(packagePyPI), "/")}
	args = append(args, publication.artifacts...)
	return set.commands.run(commandOptions{
		name: "python3",
		args: args,
		dir:  publication.dir,
		env: []string{
			"TWINE_USERNAME=" + set.cfg.packageRegistryUsername,
			"TWINE_PASSWORD=" + set.cfg.packageRegistryToken,
		},
		secrets: []string{set.cfg.packageRegistryToken},
	})
}
