package flakerelease

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v6"
)

type recordingReleaseClient struct {
	createCalls int
	createdTag  string
	createErr   error
}

func (client *recordingReleaseClient) createRelease(tag string, _ string) error {
	client.createCalls++
	client.createdTag = tag
	return client.createErr
}

func (*recordingReleaseClient) uploadAsset(_ string, _ string) error {
	return nil
}

func (*recordingReleaseClient) cleanupAssets(_ releaseTag) error {
	return nil
}

func TestRunHelp(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("DOCKER", "")
	t.Setenv("GITHUB_TOKEN", "")

	if err := Run([]string{"--help"}); err != nil {
		t.Fatal(err)
	}
}

func TestConfigFromEnvBundleAppImage(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "empty", want: false},
		{name: "false", value: "false", want: false},
		{name: "true", value: "true", want: true},
		{name: "one", value: "1", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("BUNDLE_APPIMAGE", test.value)
			if got := configFromEnv().bundleAppImage; got != test.want {
				t.Fatalf("configFromEnv().bundleAppImage = %t; want %t", got, test.want)
			}
		})
	}
}

func TestParseRunArgsBundleAppImage(t *testing.T) {
	cfg := config{}
	packages, help := parseRunArgs(&cfg, []string{"packages.one", "--bundle-appimage", "packages.two"})
	if help {
		t.Fatal("parseRunArgs() requested help")
	}
	if !cfg.bundleAppImage {
		t.Fatal("parseRunArgs() did not enable AppImage bundling")
	}
	if len(packages) != 2 || packages[0] != "packages.one" || packages[1] != "packages.two" {
		t.Fatalf("parseRunArgs() packages = %q; want [packages.one packages.two]", packages)
	}
}

func TestSelectedReleaseTagUsesExactTagEvent(t *testing.T) {
	t.Setenv("TAG", "")
	t.Setenv("GITHUB_REF_NAME", "packages/api/v1.2.3")
	t.Setenv("GITHUB_REF", "refs/tags/packages/api/v1.2.3")

	got, err := selectedReleaseTag()
	if err != nil {
		t.Fatal(err)
	}
	if got != "packages/api/v1.2.3" {
		t.Fatalf("selectedReleaseTag() = %q; want exact event tag", got)
	}
}

func TestSelectedReleaseTagRejectsNonTagEvents(t *testing.T) {
	for _, test := range []struct {
		name    string
		refName string
		ref     string
	}{
		{name: "branch", refName: "main", ref: "refs/heads/main"},
		{name: "mismatch", refName: "v1.2.3", ref: "refs/tags/v1.2.4"},
		{name: "missing name", ref: "refs/tags/v1.2.3"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TAG", "")
			t.Setenv("GITHUB_REF_NAME", test.refName)
			t.Setenv("GITHUB_REF", test.ref)
			if _, err := selectedReleaseTag(); err == nil {
				t.Fatal("selectedReleaseTag() returned nil error")
			}
		})
	}
}

func TestSelectedReleaseTagTreatsEmptyTagAsUnspecified(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeGitRepository(repo)
	commit := commitGitTestFile(t, repo, dir, "release")
	createGitTestTag(t, repo, "v1.2.3", commit)
	chdir(t, dir)
	t.Setenv("TAG", "")
	t.Setenv("GITHUB_REF_NAME", "")
	t.Setenv("GITHUB_REF", "")

	got, err := selectedReleaseTag()
	if err != nil {
		t.Fatal(err)
	}
	if got != "v1.2.3" {
		t.Fatalf("selectedReleaseTag() = %q; want automatic tag", got)
	}
}

func TestReleaseSessionRejectsRunWithoutOutputs(t *testing.T) {
	client := &recordingReleaseClient{}
	session := releaseSession{client: client, tag: parseReleaseTag("v1.0.0")}

	if err := session.requireOutput(); err == nil {
		t.Fatal("release session returned no error without outputs")
	}
	if client.createCalls != 0 {
		t.Fatalf("release creation calls = %d; want 0", client.createCalls)
	}
}

func TestReleaseSessionCreatesReleaseOnce(t *testing.T) {
	client := &recordingReleaseClient{}
	session := releaseSession{client: client, tag: parseReleaseTag("packages/cli/v1.0.0")}

	if err := session.ensureRelease(); err != nil {
		t.Fatal(err)
	}
	if err := session.ensureRelease(); err != nil {
		t.Fatal(err)
	}

	if err := session.requireOutput(); err != nil {
		t.Fatal(err)
	}
	if client.createCalls != 1 {
		t.Fatalf("release creation calls = %d; want 1", client.createCalls)
	}
	if client.createdTag != "packages/cli/v1.0.0" {
		t.Fatalf("release tag = %q; want full scoped tag", client.createdTag)
	}
	if !session.created {
		t.Fatal("release session did not record the created release")
	}
}

func TestReleaseSessionPropagatesCreationFailure(t *testing.T) {
	client := &recordingReleaseClient{createErr: errors.New("unauthorized")}
	session := releaseSession{client: client, tag: parseReleaseTag("v1.0.0")}

	if err := session.ensureRelease(); err == nil {
		t.Fatal("ensureRelease() returned nil error")
	}
	if client.createCalls != 1 {
		t.Fatalf("release creation calls = %d; want 1", client.createCalls)
	}
	if err := session.requireOutput(); err == nil {
		t.Fatal("requireOutput() returned nil after release creation failure")
	}
	if session.created {
		t.Fatal("failed release creation was recorded as successful")
	}
}

func TestReleaseSessionDryRunRecordsOutputWithoutCreatingRelease(t *testing.T) {
	client := &recordingReleaseClient{}
	session := releaseSession{cfg: config{dryRun: true}, client: client, tag: parseReleaseTag("v1.0.0")}

	if err := session.ensureRelease(); err != nil {
		t.Fatal(err)
	}

	if err := session.requireOutput(); err != nil {
		t.Fatal(err)
	}
	if client.createCalls != 0 {
		t.Fatalf("release creation calls = %d; want 0", client.createCalls)
	}
	if session.created {
		t.Fatal("dry-run release session recorded a created release")
	}
}

func TestPrepareReleasePackagesSkipsEvaluationFailuresAndAliases(t *testing.T) {
	plans := prepareReleasePackagesWith([]string{"broken", "valid", "alias"}, func(pkg string) (releasePackagePlan, error) {
		switch pkg {
		case "broken":
			return releasePackagePlan{}, errors.New("evaluation failed")
		case "valid":
			return releasePackagePlan{pkg: pkg, storePath: "/nix/store/valid"}, nil
		case "alias":
			return releasePackagePlan{pkg: pkg, storePath: "/nix/store/valid"}, nil
		default:
			return releasePackagePlan{}, errors.New("unexpected package")
		}
	})
	if len(plans) != 1 || plans[0].pkg != "valid" {
		t.Fatalf("prepareReleasePackagesWith() = %#v; want only valid package", plans)
	}
}

func TestArtifactMatchingUsesItsOwnVersion(t *testing.T) {
	tag := parseReleaseTag("packages/api/v1.2.3")
	if packageVersionMatchesReleaseTag("0.9.0", tag) {
		t.Fatal("mismatched archive version matched release tag")
	}
	if imageTagMatchesReleaseTag("latest", tag) {
		t.Fatal("mismatched image tag matched release tag")
	}
	if !packageMatchesReleaseTag("0.9.0", "1.2.3", tag) {
		t.Fatal("matching image tag was not recognized for image classification")
	}
	if !packageMatchesReleaseTag("1.2.3", "latest", tag) {
		t.Fatal("matching package version was not recognized for image classification")
	}
}

func TestReleasePackageSkipsImageWithMismatchedImageTag(t *testing.T) {
	created := false
	images := false
	pkg := releasePackagePlan{
		pkg:       "packages.image",
		storePath: "/nix/store/image.tar.gz",
		version:   "1.2.3",
		imageName: "owner/app",
		imageTag:  "latest",
		image:     true,
	}
	if err := releasePackage(config{}, "owner/repo", nil, parseReleaseTag("v1.2.3"), pkg, func() error {
		created = true
		return nil
	}, &images); err != nil {
		t.Fatal(err)
	}
	if created || images {
		t.Fatal("mismatched image tag reached release side effects")
	}
}

func TestPublishableImagePathRequiresKnownImageOutput(t *testing.T) {
	dir := t.TempDir()
	layered := filepath.Join(dir, "image.tar.gz")
	streamed := filepath.Join(dir, "stream-image")
	archive := filepath.Join(dir, "package.zip")
	if err := os.WriteFile(layered, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(streamed, []byte("image"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !publishableImagePath(layered) {
		t.Fatal("buildLayeredImage output was not classified as an image")
	}
	if !publishableImagePath(streamed) {
		t.Fatal("streamLayeredImage output was not classified as an image")
	}
	if publishableImagePath(archive) {
		t.Fatal("arbitrary regular file was classified as an image")
	}
	if publishableImagePath(dir) {
		t.Fatal("directory output was classified as an image")
	}
}

func TestContainerImageRepository(t *testing.T) {
	for _, test := range []struct {
		name       string
		repository string
		tag        string
		want       string
	}{
		{name: "unscoped", repository: "Owner/Repo", tag: "v1.2.3", want: "Owner/Repo"},
		{name: "scoped", repository: "Owner/Repo", tag: "packages/api/v1.2.3", want: "Owner/Repo/packages/api"},
		{name: "nested", repository: "Owner/Repo", tag: "packages/api/client/v1.2.3", want: "Owner/Repo/packages/api/client"},
		{name: "mixed case", repository: "Owner/Repo", tag: "Packages/API/v1.2.3", want: "Owner/Repo/Packages/API"},
		{name: "empty repository", repository: "", tag: "packages/api/v1.2.3", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := containerImageRepository(test.repository, parseReleaseTag(test.tag))
			if got != test.want {
				t.Fatalf("containerImageRepository() = %q; want %q", got, test.want)
			}
		})
	}
}

func TestValidateImagePackageDestination(t *testing.T) {
	image := releasePackagePlan{
		pkg:       "packages.image",
		imageName: "owner/app",
		imageTag:  "1.2.3",
		image:     true,
	}
	mismatchedImage := image
	mismatchedImage.imageTag = "latest"
	for _, test := range []struct {
		name       string
		cfg        config
		repository string
		pkg        releasePackagePlan
		wantErr    bool
	}{
		{name: "unscoped", cfg: config{registry: "ghcr.io"}, repository: "Owner/Repo", pkg: image},
		{name: "scoped", cfg: config{registry: "ghcr.io"}, repository: "Owner/Repo/packages/api", pkg: image},
		{name: "mixed case scope", cfg: config{registry: "ghcr.io"}, repository: "Owner/Repo/Packages/API", pkg: image},
		{name: "invalid scope", cfg: config{registry: "ghcr.io"}, repository: "owner/repo/packages/@api", pkg: image, wantErr: true},
		{name: "missing registry", cfg: config{}, repository: "owner/repo/packages/api", pkg: image, wantErr: true},
		{name: "missing repository", cfg: config{registry: "ghcr.io"}, repository: "", pkg: image, wantErr: true},
		{name: "dry run still validates", cfg: config{registry: "ghcr.io", dryRun: true}, repository: "owner/repo/packages/@api", pkg: image, wantErr: true},
		{name: "mismatched image tag", cfg: config{}, repository: "", pkg: mismatchedImage},
		{name: "archive only", cfg: config{}, repository: "", pkg: releasePackagePlan{pkg: "packages.archive"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateImagePackageDestination(test.cfg, test.repository, parseReleaseTag("v1.2.3"), test.pkg)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateImagePackageDestination() error = %v; wantErr %v", err, test.wantErr)
			}
		})
	}

	if err := validateImageDestination(config{registry: "ghcr.io"}, "owner/repo/packages/api", "1.2.3/bad"); err == nil {
		t.Fatal("invalid container image tag was accepted")
	}
}

func TestPackageMainProgramPathPrefersBinOutput(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	bin := filepath.Join(root, "bin")
	for _, path := range []string{filepath.Join(out, "bin", "app"), filepath.Join(bin, "bin", "app")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("app"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got := packageMainProgramPath([]packageOutput{{Name: "out", Path: out}, {Name: "bin", Path: bin}}, "app")
	want := filepath.Join(bin, "bin", "app")
	if got != want {
		t.Fatalf("packageMainProgramPath() = %q; want %q", got, want)
	}
}

func TestShouldBundleAppImage(t *testing.T) {
	script := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	native, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		cfg  config
		pkg  releasePackagePlan
		path string
		want bool
	}{
		{
			name: "disabled script",
			pkg:  releasePackagePlan{mainProgram: "app", platform: platform{OS: "linux"}},
			path: script,
		},
		{
			name: "enabled script",
			cfg:  config{bundleAppImage: true},
			pkg:  releasePackagePlan{mainProgram: "app", platform: platform{OS: "linux"}},
			path: script,
			want: true,
		},
		{
			name: "native binary",
			cfg:  config{bundleAppImage: true},
			pkg:  releasePackagePlan{mainProgram: "app", platform: platform{OS: "linux"}},
			path: native,
		},
		{
			name: "non-linux script",
			cfg:  config{bundleAppImage: true},
			pkg:  releasePackagePlan{mainProgram: "app", platform: platform{OS: "darwin"}},
			path: script,
		},
		{
			name: "missing main program",
			cfg:  config{bundleAppImage: true},
			pkg:  releasePackagePlan{platform: platform{OS: "linux"}},
			path: script,
		},
		{
			name: "missing path",
			cfg:  config{bundleAppImage: true},
			pkg:  releasePackagePlan{mainProgram: "app", platform: platform{OS: "linux"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldBundleAppImage(test.cfg, test.pkg, test.path); got != test.want {
				t.Fatalf("shouldBundleAppImage() = %t; want %t", got, test.want)
			}
		})
	}
}

func TestIsNativeBinary(t *testing.T) {
	native, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !isNativeBinary(native) {
		t.Fatalf("isNativeBinary(%q) = false; want true", native)
	}

	script := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if isNativeBinary(script) {
		t.Fatalf("isNativeBinary(%q) = true; want false", script)
	}
}
