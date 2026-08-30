package flakerelease

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type fakePackageCommandRunner struct {
	requireFunc func(string) error
	runFunc     func(commandOptions) error
	captureFunc func(commandOptions) (string, error)
}

func (runner fakePackageCommandRunner) require(name string) error {
	return runner.requireFunc(name)
}

func (runner fakePackageCommandRunner) run(options commandOptions) error {
	return runner.runFunc(options)
}

func (runner fakePackageCommandRunner) capture(options commandOptions) (string, error) {
	return runner.captureFunc(options)
}

func TestApplyPackageRegistryDefaultsDoesNotReuseGitHubToken(t *testing.T) {
	cfg := config{
		githubRepository: "Owner/repository",
		githubActor:      "actor",
		githubToken:      "release-token",
	}
	applyPackageRegistryDefaults(&cfg, releaseGitHub)
	if cfg.packageRegistryOwner != "Owner" {
		t.Fatalf("package registry owner = %q; want Owner", cfg.packageRegistryOwner)
	}
	if cfg.packageRegistryURL != "https://npm.pkg.github.com" {
		t.Fatalf("package registry URL = %q", cfg.packageRegistryURL)
	}
	if cfg.packageRegistryUsername != "actor" {
		t.Fatalf("package registry username = %q; want actor", cfg.packageRegistryUsername)
	}
	if cfg.packageRegistryToken != "" {
		t.Fatal("package registry token fell back to GITHUB_TOKEN")
	}
}

func TestParsePackageKinds(t *testing.T) {
	kinds, err := parsePackageKinds("npm, go\npypi cargo npm")
	if err != nil {
		t.Fatal(err)
	}
	want := []packageKind{packageCargo, packageGo, packageNPM, packagePyPI}
	if len(kinds) != len(want) {
		t.Fatalf("kind count = %d; want %d", len(kinds), len(want))
	}
	for index := range want {
		if kinds[index] != want[index] {
			t.Fatalf("kind %d = %q; want %q", index, kinds[index], want[index])
		}
	}
	if _, err := parsePackageKinds("go;cargo"); err == nil {
		t.Fatal("semicolon-separated package kinds were accepted")
	}
	if _, err := parsePackageKinds("ruby"); err == nil {
		t.Fatal("unsupported package kind was accepted")
	}
}

func TestPackageMatchesScopedReleaseTagVersion(t *testing.T) {
	tag := parseReleaseTag("packages/cli/v1.2.3")
	if !packageMatchesReleaseTag("1.2.3", "", tag) {
		t.Fatal("Nix package version did not match the parsed scoped release version")
	}
	if !packageMatchesReleaseTag("", "1.2.3", tag) {
		t.Fatal("image tag did not match the parsed scoped release version")
	}
	if packageMatchesReleaseTag("packages/cli/v1.2.3", "", tag) {
		t.Fatal("full scoped tag incorrectly matched a Nix package version")
	}
	if packageMatchesReleaseTag("", "", parseReleaseTag("pkg/v")) {
		t.Fatal("empty package metadata matched an empty release version")
	}
}

func TestValidatePackageRegistryConfigDryRunDoesNotRequireCredentials(t *testing.T) {
	cfg := config{
		dryRun:               true,
		packageRegistryOwner: "owner",
		packageRegistryURL:   "https://git.example",
	}
	if err := validatePackageRegistryConfig(cfg, releaseForgejo, []packageKind{packageGo, packagePyPI}); err != nil {
		t.Fatal(err)
	}
	cfg.dryRun = false
	if err := validatePackageRegistryConfig(cfg, releaseForgejo, []packageKind{packageGo}); err == nil {
		t.Fatal("non-dry-run config without token was accepted")
	}
	cfg.packageRegistryToken = "token"
	if err := validatePackageRegistryConfig(cfg, releaseForgejo, []packageKind{packageGo}); err != nil {
		t.Fatalf("Go publishing unnecessarily required a username: %v", err)
	}
	if err := validatePackageRegistryConfig(cfg, releaseForgejo, []packageKind{packagePyPI}); err == nil {
		t.Fatal("PyPI config without username was accepted")
	}
}

func TestValidatePackageRegistryConfigRejectsUnsafeOwnerAndURL(t *testing.T) {
	cfg := config{
		packageRegistryOwner: "owner/name",
		packageRegistryURL:   "https://git.example",
		packageRegistryToken: "token",
	}
	if err := validatePackageRegistryConfig(cfg, releaseForgejo, []packageKind{packageGo}); err == nil {
		t.Fatal("owner containing a path separator was accepted")
	}
	cfg.packageRegistryOwner = "owner"
	cfg.packageRegistryURL = "file://git.example/packages"
	if err := validatePackageRegistryConfig(cfg, releaseForgejo, []packageKind{packageGo}); err == nil {
		t.Fatal("non-HTTP registry URL was accepted")
	}
}

func TestValidatePackageRegistryProviderMatrix(t *testing.T) {
	cfg := config{
		packageRegistryOwner:    "owner",
		packageRegistryURL:      "https://npm.pkg.github.com",
		packageRegistryToken:    "token",
		packageRegistryUsername: "actor",
	}
	if err := validatePackageRegistryConfig(cfg, releaseGitHub, []packageKind{packageNPM}); err != nil {
		t.Fatal(err)
	}
	if err := validatePackageRegistryConfig(cfg, releaseGitHub, []packageKind{packageGo}); err == nil {
		t.Fatal("GitHub Go package publishing was accepted")
	}
}

func TestGitHubNPMPackageUsesDefaultRegistryAndOwnerScope(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "package.json")
	writeTestFile(t, manifest, `{"name":"@owner/project","version":"1.2.3"}`)
	cfg := config{
		dryRun:               true,
		githubRepository:     "Owner/repository",
		packageRegistryOwner: "Owner",
	}
	applyPackageRegistryDefaults(&cfg, releaseGitHub)
	var published commandOptions
	set := &packagePublicationSet{
		cfg:          cfg,
		provider:     releaseGitHub,
		releaseTag:   parseReleaseTag("v1.2.3"),
		temporaryDir: t.TempDir(),
		commands: fakePackageCommandRunner{
			requireFunc: func(name string) error {
				if name != "npm" {
					t.Fatalf("required command = %q; want npm", name)
				}
				return nil
			},
			runFunc: func(options commandOptions) error {
				published = options
				return nil
			},
			captureFunc: func(commandOptions) (string, error) {
				t.Fatal("unexpected command capture")
				return "", nil
			},
		},
	}
	publication := &packagePublication{kind: packageNPM, dir: dir, manifest: manifest}
	if err := preflightNPMPackage(set, publication); err != nil {
		t.Fatal(err)
	}
	if publication.name != "@owner/project" || publication.version != "1.2.3" {
		t.Fatalf("npm identity = %s@%s", publication.name, publication.version)
	}
	if !slices.Contains(published.args, "https://npm.pkg.github.com/") || !slices.Contains(published.args, "--dry-run") {
		t.Fatalf("npm publish args = %q", published.args)
	}

	writeTestFile(t, manifest, `{"name":"@other/project","version":"1.2.3"}`)
	if err := preflightNPMPackage(set, publication); err == nil {
		t.Fatal("GitHub npm package with the wrong owner scope was accepted")
	}
}

func TestNixPkgSrcSkipsUnavailableSources(t *testing.T) {
	for name, capture := range map[string]func(...string) (string, error){
		"null": func(...string) (string, error) { return "null", nil },
		"unevaluable": func(...string) (string, error) {
			return "", errors.New("attribute has no src")
		},
	} {
		t.Run(name, func(t *testing.T) {
			source, err := nixPkgSrcWithCapture("packages.test", capture)
			if err != nil {
				t.Fatal(err)
			}
			if source != "" {
				t.Fatalf("source = %q; want empty", source)
			}
		})
	}

	root := t.TempDir()
	source, err := nixPkgSrcWithCapture("packages.test", func(args ...string) (string, error) {
		want := []string{"eval", "--json", ".#packages.test.src"}
		if !slices.Equal(args, want) {
			t.Fatalf("nix args = %q; want %q", args, want)
		}
		return `"` + root + `"`, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if source != root {
		t.Fatalf("source = %q; want %q", source, root)
	}
}

func TestPreparePackagePublicationsSkipsUnavailableSourcesAndCleansUp(t *testing.T) {
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "go.mod"), "module example.com/owner/project\n")
	writeTestFile(t, filepath.Join(source, "main.go"), "package project\n")
	archive := filepath.Join(t.TempDir(), "module.zip")
	writeTestFile(t, archive, "zip")

	var requested []string
	runner := fakePackageCommandRunner{
		requireFunc: func(name string) error {
			if name != "go" {
				t.Fatalf("required command = %q; want go", name)
			}
			return nil
		},
		runFunc: func(commandOptions) error {
			t.Fatal("unexpected command run")
			return nil
		},
		captureFunc: func(options commandOptions) (string, error) {
			if options.name != "go" {
				t.Fatalf("captured command = %q %q", options.name, options.args)
			}
			if !slices.Contains(options.env, "GOWORK=off") {
				t.Fatalf("go command environment = %q; want GOWORK=off", options.env)
			}
			switch {
			case slices.Equal(options.args, []string{"list", "-m", "-mod=readonly"}):
				return "example.com/owner/project", nil
			case slices.Equal(options.args, []string{"mod", "download", "-json", "example.com/owner/project@v1.2.3"}):
				return `{"Zip":"` + archive + `"}`, nil
			default:
				t.Fatalf("captured command = %q %q", options.name, options.args)
				return "", nil
			}
		},
	}
	cfg := config{
		dryRun:               true,
		githubRepository:     "owner/project",
		githubServerURL:      "https://example.com",
		publishPackages:      "go",
		packageRegistryOwner: "owner",
		packageRegistryURL:   "https://git.example",
	}
	set, err := preparePackagePublicationsWith(cfg, releaseForgejo, parseReleaseTag("v1.2.3"), []string{"null", "unevaluable", "valid"}, func(pkg string) (string, error) {
		requested = append(requested, pkg)
		if pkg == "valid" {
			return source, nil
		}
		return "", nil
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(requested, []string{"null", "unevaluable", "valid"}) {
		t.Fatalf("requested sources = %q", requested)
	}
	if len(set.packages) != 1 || set.packages[0].source != source {
		t.Fatalf("prepared packages = %#v", set.packages)
	}
	temporaryDir := set.temporaryDir
	set.Close()
	if _, err := os.Stat(temporaryDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary directory still exists after Close: %v", err)
	}
}

func TestCommandEnvironmentOverridesExistingValues(t *testing.T) {
	t.Setenv("TWINE_PASSWORD", "old-secret")
	environment := commandEnvironment([]string{"TWINE_PASSWORD=new-secret"})
	var values []string
	for _, value := range environment {
		if strings.HasPrefix(value, "TWINE_PASSWORD=") {
			values = append(values, value)
		}
	}
	if !slices.Equal(values, []string{"TWINE_PASSWORD=new-secret"}) {
		t.Fatalf("TWINE_PASSWORD entries = %q", values)
	}
}

func TestCopyWritableTree(t *testing.T) {
	source := t.TempDir()
	path := filepath.Join(source, "nested", "file")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Dir(path), 0o700)
	destination := filepath.Join(t.TempDir(), "stage")
	if err := copyWritableTree(source, destination); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(destination, "nested", "file")
	if err := os.WriteFile(staged, []byte("updated"), 0o600); err != nil {
		t.Fatalf("staged file is not writable: %v", err)
	}
}

func TestPreflightGoPackageUsesScopedVersionTagForDownload(t *testing.T) {
	source := t.TempDir()
	manifest := filepath.Join(source, "go.mod")
	writeTestFile(t, manifest, "module example.com/Owner/Repo/modules/api\n")
	archive := filepath.Join(t.TempDir(), "module.zip")
	writeTestFile(t, archive, "zip")

	var downloaded commandOptions
	set := &packagePublicationSet{
		cfg: config{
			dryRun:           true,
			githubServerURL:  "https://example.com",
			githubRepository: "Owner/Repo",
		},
		releaseTag:   parseReleaseTag("modules/api/v1.2.3"),
		temporaryDir: t.TempDir(),
		commands: fakePackageCommandRunner{
			requireFunc: func(name string) error {
				if name != "go" {
					t.Fatalf("required command = %q; want go", name)
				}
				return nil
			},
			runFunc: func(commandOptions) error {
				t.Fatal("unexpected command run")
				return nil
			},
			captureFunc: func(options commandOptions) (string, error) {
				switch {
				case slices.Equal(options.args, []string{"list", "-m", "-mod=readonly"}):
					if !slices.Contains(options.env, "GOWORK=off") {
						t.Fatalf("go list env = %q", options.env)
					}
					return "example.com/Owner/Repo/modules/api", nil
				case slices.Equal(options.args, []string{"mod", "download", "-json", "example.com/Owner/Repo/modules/api@v1.2.3"}):
					downloaded = options
					return `{"Zip":"` + archive + `"}`, nil
				default:
					t.Fatalf("unexpected go args: %q", options.args)
					return "", nil
				}
			},
		},
	}
	publication := &packagePublication{kind: packageGo, dir: source, manifest: manifest}
	if err := preflightGoPackage(set, publication); err != nil {
		t.Fatal(err)
	}
	if publication.name != "example.com/Owner/Repo/modules/api" || publication.version != "v1.2.3" {
		t.Fatalf("go identity = %s@%s", publication.name, publication.version)
	}
	if !slices.Equal(publication.artifacts, []string{archive}) {
		t.Fatalf("go artifacts = %q; want %q", publication.artifacts, archive)
	}
	for _, expected := range []string{"GONOSUMDB=example.com/Owner/Repo/modules/api", "GOPRIVATE=example.com/Owner/Repo/modules/api", "GOPROXY=direct", "GOWORK=off"} {
		if !slices.Contains(downloaded.env, expected) {
			t.Fatalf("go mod download env = %q; missing %q", downloaded.env, expected)
		}
	}
}

func TestPreflightGoPackageRejectsScopedTagForRootModuleBeforeDownload(t *testing.T) {
	source := t.TempDir()
	manifest := filepath.Join(source, "go.mod")
	writeTestFile(t, manifest, "module example.com/Owner/Repo\n")

	captureCalled := false
	set := &packagePublicationSet{
		cfg: config{
			dryRun:           true,
			githubServerURL:  "https://example.com",
			githubRepository: "owner/repo",
		},
		releaseTag:   parseReleaseTag("modules/api/v1.2.3"),
		temporaryDir: t.TempDir(),
		commands: fakePackageCommandRunner{
			requireFunc: func(string) error { return nil },
			captureFunc: func(commandOptions) (string, error) {
				captureCalled = true
				return "", nil
			},
		},
	}
	publication := &packagePublication{kind: packageGo, dir: source, manifest: manifest}
	if err := preflightGoPackage(set, publication); err == nil || !strings.Contains(err.Error(), "root Go module") {
		t.Fatalf("preflightGoPackage() error = %v; want scoped root-module rejection", err)
	}
	if captureCalled {
		t.Fatal("Go command ran before module tag namespace provenance was rejected")
	}
}

func TestValidateGoModuleTagNamespaceHandlesSemanticMajorSuffix(t *testing.T) {
	cfg := config{
		githubServerURL:  "https://example.com",
		githubRepository: "Owner/Repo",
	}
	if err := validateGoModuleTagNamespace(cfg, "example.com/Owner/Repo/modules/api/v2", parseReleaseTag("modules/api/v2.0.0")); err != nil {
		t.Fatalf("scoped v2 submodule was rejected: %v", err)
	}
	if err := validateGoModuleTagNamespace(cfg, "example.com/Owner/Repo/v2", parseReleaseTag("v2.0.0")); err != nil {
		t.Fatalf("unscoped v2 root module was rejected: %v", err)
	}
}

func TestValidateGoModuleTagNamespaceIncludesServerPath(t *testing.T) {
	cfg := config{
		githubServerURL:  "https://example.com/forgejo/",
		githubRepository: "Owner/Repo",
	}
	for _, module := range []string{
		"example.com/forgejo/Owner/Repo/modules/api/v2",
		"example.com/forgejo/Owner/Repo.git/modules/api/v2",
	} {
		if err := validateGoModuleTagNamespace(cfg, module, parseReleaseTag("modules/api/v2.0.0")); err != nil {
			t.Fatalf("scoped module %q on a subpath-hosted server was rejected: %v", module, err)
		}
	}
}

func TestValidateGoModuleTagNamespacePreservesUnscopedVanityModules(t *testing.T) {
	cfg := config{
		githubServerURL:  "https://example.com",
		githubRepository: "Owner/Repo",
	}
	if err := validateGoModuleTagNamespace(cfg, "go.example.com/library", parseReleaseTag("v1.2.3")); err != nil {
		t.Fatalf("unscoped vanity module was rejected: %v", err)
	}
}

func TestPublishGoPackageUsesNativePut(t *testing.T) {
	var method string
	var path string
	var authorization string
	var contentType string
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		method = request.Method
		path = request.URL.Path
		authorization = request.Header.Get("Authorization")
		contentType = request.Header.Get("Content-Type")
		body, _ = io.ReadAll(request.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	archive := filepath.Join(t.TempDir(), "module.zip")
	writeTestFile(t, archive, "zip")
	set := &packagePublicationSet{
		cfg: config{
			packageRegistryOwner:    "owner",
			packageRegistryURL:      server.URL,
			packageRegistryUsername: "actor",
			packageRegistryToken:    "secret",
		},
		provider: releaseForgejo,
	}
	publication := &packagePublication{artifacts: []string{archive}}
	if err := publishGoPackage(set, publication); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPut || path != "/api/packages/owner/go/upload" {
		t.Fatalf("request = %s %s", method, path)
	}
	if authorization != "token secret" {
		t.Fatalf("authorization = %q; want token credentials", authorization)
	}
	if contentType != "application/zip" {
		t.Fatalf("content type = %q; want application/zip", contentType)
	}
	if !bytes.Equal(body, []byte("zip")) {
		t.Fatalf("body = %q; want archive", body)
	}
}

func TestValidatePyPIArtifacts(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "example-1.2.3-py3-none-any.whl")
	file, err := os.Create(artifact)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	metadata, err := writer.Create("example-1.2.3.dist-info/METADATA")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = metadata.Write([]byte("Metadata-Version: 2.4\nName: Example_Package\nVersion: 1.2.3\n\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := validatePyPIArtifacts([]string{artifact}, "example-package", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := validatePyPIArtifacts([]string{artifact}, "example-package", "1.2.4"); err == nil {
		t.Fatal("mismatched artifact version was accepted")
	}
}

func TestPreflightPyPIPackageUsesBuiltArtifactMetadata(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	writeTestFile(t, filepath.Join(project, "pyproject.toml"), "[build-system]\nrequires = []\n")
	checked := false
	runner := fakePackageCommandRunner{
		requireFunc: func(name string) error {
			if name != "python3" {
				t.Fatalf("required command = %q; want python3", name)
			}
			return nil
		},
		runFunc: func(options commandOptions) error {
			if slices.Equal(options.args[:min(2, len(options.args))], []string{"-m", "build"}) {
				outdir := options.args[slices.Index(options.args, "--outdir")+1]
				writeTestWheel(t, filepath.Join(outdir, "example-1.2.3-py3-none-any.whl"), "Example_Package", "1.2.3")
				return nil
			}
			if slices.Equal(options.args[:min(3, len(options.args))], []string{"-m", "twine", "check"}) {
				checked = true
				return nil
			}
			t.Fatalf("unexpected command: %q %q", options.name, options.args)
			return nil
		},
		captureFunc: func(commandOptions) (string, error) {
			t.Fatal("unexpected captured command")
			return "", nil
		},
	}
	set := &packagePublicationSet{temporaryDir: root, commands: runner}
	publication := &packagePublication{kind: packagePyPI, source: project, dir: project, manifest: filepath.Join(project, "pyproject.toml")}
	if err := preflightPyPIPackage(set, publication); err != nil {
		t.Fatal(err)
	}
	if publication.name != "Example_Package" || publication.version != "1.2.3" {
		t.Fatalf("artifact identity = %s@%s", publication.name, publication.version)
	}
	if !checked || len(publication.artifacts) != 1 {
		t.Fatalf("twine checked = %v, artifacts = %q", checked, publication.artifacts)
	}
}

func TestPackagePreflightRejectsDuplicateRegistryIdentity(t *testing.T) {
	root := t.TempDir()
	var publications []packagePublication
	for _, source := range []string{"first", "second"} {
		dir := filepath.Join(root, source)
		manifest := filepath.Join(dir, "package.json")
		writeTestFile(t, manifest, `{"name":"same-package","version":"1.2.3"}`)
		publications = append(publications, packagePublication{kind: packageNPM, source: dir, dir: dir, manifest: manifest})
	}
	runner := fakePackageCommandRunner{
		requireFunc: func(string) error { return nil },
		runFunc:     func(commandOptions) error { return nil },
		captureFunc: func(commandOptions) (string, error) { return "", nil },
	}
	set := &packagePublicationSet{
		cfg: config{
			dryRun:               true,
			packageRegistryOwner: "owner",
			packageRegistryURL:   "https://git.example",
		},
		provider:     releaseForgejo,
		releaseTag:   parseReleaseTag("v1.2.3"),
		temporaryDir: root,
		packages:     publications,
		commands:     runner,
	}
	if err := set.preflight(); err == nil || !strings.Contains(err.Error(), "duplicate npm package") {
		t.Fatalf("duplicate preflight error = %v", err)
	}
}

func TestPackagePublishErrorsAreFatal(t *testing.T) {
	root := t.TempDir()
	runner := fakePackageCommandRunner{
		requireFunc: func(string) error { return nil },
		runFunc:     func(commandOptions) error { return errors.New("registry rejected upload") },
		captureFunc: func(commandOptions) (string, error) { return "", nil },
	}
	set := &packagePublicationSet{
		cfg:          config{packageRegistryOwner: "owner", packageRegistryURL: "https://git.example", packageRegistryToken: "token"},
		provider:     releaseForgejo,
		temporaryDir: root,
		packages:     []packagePublication{{kind: packageNPM, name: "example", version: "1.2.3", dir: root}},
		commands:     runner,
	}
	if err := set.publish(); err == nil || !strings.Contains(err.Error(), "registry rejected upload") {
		t.Fatalf("publish error = %v", err)
	}
}

func TestRequireStrictPackageVersion(t *testing.T) {
	for _, version := range []string{"1.2.3", "1.2.3-rc.1", "1.2.3+build.4"} {
		if err := requireStrictPackageVersion(version); err != nil {
			t.Fatalf("version %q rejected: %v", version, err)
		}
	}
	for _, version := range []string{"v1.2.3", "1.2", "01.2.3", "1.2.3-01"} {
		if err := requireStrictPackageVersion(version); err == nil {
			t.Fatalf("invalid version %q accepted", version)
		}
	}
}

func TestHTTPRequestRedactsAuthenticationFromErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, request.Header.Get("Authorization")+" secret", http.StatusBadRequest)
	}))
	defer server.Close()
	_, err := httpRequest(httpRequestOptions{
		method:   http.MethodGet,
		url:      server.URL,
		username: "actor",
		password: "secret",
	})
	if err == nil {
		t.Fatal("httpRequest returned no error")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "YWN0b3I6c2VjcmV0") {
		t.Fatalf("httpRequest error leaked authentication: %v", err)
	}
}

func TestPackageRegistryDisplayURLRemovesSecrets(t *testing.T) {
	got := packageRegistryDisplayURL("https://user:secret@git.example/packages?token=secret#secret")
	if got != "https://git.example/packages" {
		t.Fatalf("packageRegistryDisplayURL() = %q", got)
	}
}

func TestRedactSecrets(t *testing.T) {
	got := redactSecrets("token=secret secret", []string{"secret"})
	if strings.Contains(got, "secret") || got != "token=[REDACTED] [REDACTED]" {
		t.Fatalf("redactSecrets() = %q", got)
	}
}

func writeTestWheel(t *testing.T, path string, name string, version string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	metadata, err := writer.Create("package.dist-info/METADATA")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metadata.Write([]byte("Metadata-Version: 2.4\nName: " + name + "\nVersion: " + version + "\n\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTestFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
