package flakerelease

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	cargoRegistryName    = "flake-release"
	cratesIORegistryName = "crates-io"
	npmjsRegistryURL     = "https://registry.npmjs.org/"
	pyPIUploadURL        = "https://upload.pypi.org/legacy/"
)

type cargoMetadata struct {
	Packages []struct {
		Name         string    `json:"name"`
		Version      string    `json:"version"`
		ManifestPath string    `json:"manifest_path"`
		Publish      *[]string `json:"publish"`
	} `json:"packages"`
}

type npmPackageDocument struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	Private       bool   `json:"private"`
	PublishConfig struct {
		Access string `json:"access"`
	} `json:"publishConfig"`
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
			// Cargo refuses to publish to a registry missing from the publish list, and
			// the git host's registry is always named flake-release.
			registryName := cargoRegistryName
			if publication.registry == registryCratesIO {
				registryName = cratesIORegistryName
			}
			if pkg.Publish != nil && !slices.Contains(*pkg.Publish, registryName) {
				return fmt.Errorf("cargo package %s does not list %s in publish: %w", pkg.Name, registryName, errPackageNotPublishable)
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
	registry := cargoRegistryName
	token := set.cfg.packageRegistryToken
	var env []string
	if publication.registry == registryCratesIO {
		// Naming crates-io also bypasses source replacement, such as vendored sources.
		registry = cratesIORegistryName
		token = set.cfg.cratesIOToken
		if token != "" {
			env = append(env, "CARGO_REGISTRY_TOKEN="+token)
		}
	} else {
		env = append(env, "CARGO_REGISTRIES_FLAKE_RELEASE_INDEX=sparse+"+set.registryURL(packageCargo))
		if token != "" {
			env = append(env, "CARGO_REGISTRIES_FLAKE_RELEASE_TOKEN=Bearer "+token)
		}
	}
	args := []string{"publish", "--allow-dirty", "--registry", registry, "--manifest-path", publication.manifest}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return commandOptions{
		name:    "cargo",
		args:    args,
		dir:     publication.dir,
		env:     env,
		secrets: []string{token},
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
	if publication.registry == registryHost && set.provider == releaseGitHub {
		scoped, err := gitHubNPMPackageName(manifest.Name, set.cfg.packageRegistryOwner)
		if err != nil {
			return err
		}
		if scoped != manifest.Name {
			status("scoping package %s as %s for GitHub Packages", manifest.Name, scoped)
			if err := set.commands.run(commandOptions{name: "npm", args: []string{"pkg", "set", "name=" + scoped}, dir: publication.dir}); err != nil {
				return err
			}
			manifest.Name = scoped
		}
	}
	publication.name = manifest.Name
	publication.version = manifest.Version
	// npmjs.com publishes new scoped packages as restricted, which needs a paid plan.
	publication.publishArgs = nil
	if publication.registry == registryNPMJS && strings.HasPrefix(manifest.Name, "@") && manifest.PublishConfig.Access == "" {
		publication.publishArgs = []string{"--access", "public"}
	}

	if isFile(filepath.Join(publication.dir, "package-lock.json")) {
		if err := set.commands.run(commandOptions{name: "npm", args: []string{"ci"}, dir: publication.dir}); err != nil {
			return err
		}
	}

	npmrc, err := set.npmConfig(publication.registry)
	if err != nil {
		return err
	}
	return set.commands.run(npmCommand(set, publication, npmrc, true))
}

// gitHubNPMPackageName returns name under the @owner scope that GitHub Packages
// requires, adding the scope to an unscoped name or replacing a different scope.
func gitHubNPMPackageName(name string, owner string) (string, error) {
	bare := name
	if strings.HasPrefix(bare, "@") {
		_, bare, _ = strings.Cut(bare, "/")
	}
	if bare == "" || bare != strings.ToLower(bare) {
		return "", fmt.Errorf("GitHub npm package %q must have a lowercase name", name)
	}
	return "@" + strings.ToLower(owner) + "/" + bare, nil
}

func publishNPMPackage(set *packagePublicationSet, publication *packagePublication) error {
	npmrc, err := set.npmConfig(publication.registry)
	if err != nil {
		return err
	}
	return set.commands.run(npmCommand(set, publication, npmrc, false))
}

func npmCommand(set *packagePublicationSet, publication *packagePublication, npmrc string, dryRun bool) commandOptions {
	registry, token := set.npmRegistry(publication.registry)
	args := append([]string{"publish", "--registry", registry}, publication.publishArgs...)
	if dryRun {
		args = append(args, "--dry-run")
	}
	return commandOptions{
		name:    "npm",
		args:    args,
		dir:     publication.dir,
		env:     []string{"NPM_CONFIG_USERCONFIG=" + npmrc},
		secrets: []string{token},
	}
}

// npmRegistry returns the npm registry URL and write token for registry.
func (set *packagePublicationSet) npmRegistry(registry packageRegistry) (string, string) {
	if registry == registryNPMJS {
		return npmjsRegistryURL, set.cfg.npmjsToken
	}
	return set.registryURL(packageNPM), set.cfg.packageRegistryToken
}

func (set *packagePublicationSet) npmConfig(registry packageRegistry) (string, error) {
	name := ".npmrc"
	if registry != registryHost {
		name += "-" + string(registry)
	}
	path := filepath.Join(set.temporaryDir, name)
	if isFile(path) {
		return path, nil
	}
	registryURL, token := set.npmRegistry(registry)
	parsed, err := url.Parse(registryURL)
	if err != nil {
		return "", err
	}
	contents := fmt.Sprintf("registry=%s\n", registryURL)
	if token != "" {
		authPath := strings.TrimRight(parsed.EscapedPath(), "/") + "/"
		contents += fmt.Sprintf("//%s%s:_authToken=%s\nalways-auth=true\n", parsed.Host, authPath, token)
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
	metadata, err := pyPIArtifactIdentity(artifacts)
	if err != nil {
		return err
	}
	if publication.registry == registryPyPI && slices.Contains(metadata.classifiers, pyPIPrivateClassifier) {
		return fmt.Errorf("pypi package %s is classified %q: %w", metadata.name, pyPIPrivateClassifier, errPackageNotPublishable)
	}
	if err := requireStrictPackageVersion(metadata.version); err != nil {
		return fmt.Errorf("pypi package: %w", err)
	}
	publication.name = metadata.name
	publication.version = metadata.version
	publication.artifacts = artifacts
	checkArgs := []string{"-m", "twine", "check"}
	if tool == "uv" {
		// A dry run validates the distributions without contacting the registry.
		registry, _, _ := set.pyPIRegistry(publication.registry)
		checkArgs = []string{"publish", "--dry-run", "--trusted-publishing", "never", "--publish-url", registry}
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
	registry, username, token := set.pyPIRegistry(publication.registry)
	args := []string{"-m", "twine", "upload", "--non-interactive", "--repository-url", registry}
	env := []string{
		"TWINE_USERNAME=" + username,
		"TWINE_PASSWORD=" + token,
	}
	if tool == "uv" {
		args = []string{"publish", "--trusted-publishing", "never", "--publish-url", registry}
		env = []string{
			"UV_PUBLISH_USERNAME=" + username,
			"UV_PUBLISH_PASSWORD=" + token,
		}
	}
	return set.commands.run(commandOptions{
		name:    tool,
		args:    append(args, publication.artifacts...),
		dir:     publication.dir,
		env:     env,
		secrets: []string{token},
	})
}

// pyPIRegistry returns the PyPI upload URL and credentials for registry. pypi.org
// API tokens authenticate with the __token__ username.
func (set *packagePublicationSet) pyPIRegistry(registry packageRegistry) (string, string, string) {
	if registry == registryPyPI {
		return pyPIUploadURL, "__token__", set.cfg.pypiToken
	}
	return strings.TrimRight(set.registryURL(packagePyPI), "/"), set.cfg.packageRegistryUsername, set.cfg.packageRegistryToken
}
