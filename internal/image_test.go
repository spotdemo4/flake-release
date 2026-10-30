package flakerelease

import (
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	ggcrtypes "github.com/google/go-containerregistry/pkg/v1/types"
	"go.podman.io/image/v5/pkg/sysregistriesv2"
)

func TestDockerImageName(t *testing.T) {
	for _, test := range []struct {
		name       string
		registry   string
		repository string
		tag        string
		want       string
	}{
		{
			name:       "unscoped",
			registry:   "GHCR.IO",
			repository: "Owner/Repo",
			tag:        "v1.2.3",
			want:       "ghcr.io/owner/repo:v1.2.3",
		},
		{
			name:       "scoped architecture",
			registry:   "GHCR.IO",
			repository: "Owner/Repo/Packages/API",
			tag:        "1.2.3-amd64",
			want:       "ghcr.io/owner/repo/packages/api:1.2.3-amd64",
		},
		{
			name:       "nested scoped manifest",
			registry:   "registry.example.com",
			repository: "Owner/Repo/packages/api/client",
			tag:        "2.0.0-rc.1",
			want:       "registry.example.com/owner/repo/packages/api/client:2.0.0-rc.1",
		},
		{
			name:       "scoped latest",
			registry:   "ghcr.io",
			repository: "owner/repo/packages/api",
			tag:        "latest",
			want:       "ghcr.io/owner/repo/packages/api:latest",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := dockerImageName(test.registry, test.repository, test.tag)
			if got != test.want {
				t.Fatalf("dockerImageName() = %q; want %q", got, test.want)
			}
			if _, err := dockerImageReference(test.registry, test.repository, test.tag); err != nil {
				t.Fatalf("dockerImageReference() error = %v", err)
			}
		})
	}
}

func TestImageCleanupCandidates(t *testing.T) {
	retained := []releaseTag{parseReleaseTag("packages/api/v1.9.0"), parseReleaseTag("packages/api/v1.8.0-rc.1")}
	tags := []string{"latest", "2.0.0", "2.0.0-amd64", "2.0.0-arm64", "1.9.0", "1.9.0-amd64", "1.9.0-arm64", "1.8.0-rc.1", "1.8.0-rc.1-amd64", "1.7.0", "1.7.0-amd64", "1.9.01"}
	for _, test := range []struct {
		name         string
		tags         []string
		retained     []releaseTag
		want         []string
		currentFound bool
	}{
		{name: "retain previous releases", tags: tags, retained: retained, want: []string{"1.7.0", "1.7.0-amd64", "1.9.01"}, currentFound: true},
		{name: "boolean compatibility", tags: tags, want: tags[4:], currentFound: true},
		{name: "missing current", tags: []string{"latest", "1.9.0", "1.9.0-amd64", "1.7.0"}, retained: retained},
		{name: "current architecture only", tags: []string{"2.0.0-amd64", "1.9.0", "1.7.0"}, retained: retained, want: []string{"1.7.0"}, currentFound: true},
		{name: "empty registry", retained: retained},
		{name: "nothing to delete", tags: []string{"latest", "2.0.0", "1.9.0"}, retained: retained, currentFound: true},
		{name: "empty retained version ignored", tags: []string{"2.0.0", "-amd64", "1.0.0"}, retained: []releaseTag{parseReleaseTag("v")}, want: []string{"-amd64", "1.0.0"}, currentFound: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, found := imageCleanupCandidates(test.tags, "2.0.0", test.retained)
			if found != test.currentFound || !slices.Equal(got, test.want) {
				t.Fatalf("imageCleanupCandidates() = %v, %t; want %v, %t", got, found, test.want, test.currentFound)
			}
		})
	}
}

func TestExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !executable(path) {
		t.Fatal("executable() = false; want true")
	}

	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	if executable(plain) {
		t.Fatal("executable() = true for non-executable file")
	}
}

func TestImageSystemContextIgnoresHostRegistriesConf(t *testing.T) {
	dir := t.TempDir()
	v1RegistriesConf := filepath.Join(dir, "registries.conf")
	if err := os.WriteFile(v1RegistriesConf, []byte(`[registries.search]
registries = ["docker.io"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	v1OverrideConf := filepath.Join(dir, "registries-override.conf")
	if err := os.WriteFile(v1OverrideConf, []byte(`[registries.block]
registries = ["example.com"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTAINERS_REGISTRIES_CONF", v1RegistriesConf)
	t.Setenv("CONTAINERS_REGISTRIES_CONF_OVERRIDE", v1OverrideConf)
	sysregistriesv2.InvalidateCache()
	t.Cleanup(sysregistriesv2.InvalidateCache)

	sys, err := imageSystemContext(config{
		containerRegistryUsername: "user",
		containerRegistryPassword: "pass",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sys.SystemRegistriesConfPath != os.DevNull {
		t.Fatalf("SystemRegistriesConfPath = %q; want %q", sys.SystemRegistriesConfPath, os.DevNull)
	}
	if sys.SystemRegistriesConfDirPath == "" {
		t.Fatal("SystemRegistriesConfDirPath is empty")
	}
	if sys.DockerAuthConfig == nil {
		t.Fatal("DockerAuthConfig is nil")
	}
	if sys.DockerAuthConfig.Username != "user" || sys.DockerAuthConfig.Password != "pass" {
		t.Fatalf("DockerAuthConfig = %#v; want configured credentials", sys.DockerAuthConfig)
	}

	registries, err := sysregistriesv2.GetRegistries(sys)
	if err != nil {
		t.Fatal(err)
	}
	if len(registries) != 0 {
		t.Fatalf("GetRegistries() returned %d registries; want 0", len(registries))
	}
}

func TestManifestUpdate(t *testing.T) {
	cfg := testImageRegistry(t)
	const repository = "owner/repo"
	amd64 := pushTestImage(t, cfg, repository, "1.2.3-amd64", v1.Platform{OS: "linux", Architecture: "amd64"}, map[string]string{"org.opencontainers.image.version": "1.2.3"})
	arm64 := pushTestImage(t, cfg, repository, "1.2.3-arm64", v1.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}, nil)
	pushTestImage(t, cfg, repository, "1.2.30-amd64", v1.Platform{OS: "linux", Architecture: "amd64"}, nil)

	if err := manifestUpdate(cfg, repository, "1.2.3"); err != nil {
		t.Fatalf("manifestUpdate() error = %v", err)
	}

	want := map[string]v1.Descriptor{"amd64": amd64, "arm64": arm64}
	for _, tag := range []string{"1.2.3", "latest"} {
		t.Run(tag, func(t *testing.T) {
			ref, err := name.ParseReference(dockerImageName(cfg.containerRegistry, repository, tag), name.Insecure)
			if err != nil {
				t.Fatal(err)
			}
			index, err := remote.Index(ref)
			if err != nil {
				t.Fatalf("remote.Index() error = %v", err)
			}
			indexManifest, err := index.IndexManifest()
			if err != nil {
				t.Fatal(err)
			}
			if indexManifest.MediaType != ggcrtypes.OCIImageIndex {
				t.Fatalf("index media type = %q; want %q", indexManifest.MediaType, ggcrtypes.OCIImageIndex)
			}
			if got := indexManifest.Annotations["org.opencontainers.image.version"]; got != "1.2.3" {
				t.Fatalf("index annotation org.opencontainers.image.version = %q; want %q", got, "1.2.3")
			}
			if len(indexManifest.Manifests) != len(want) {
				t.Fatalf("index has %d manifests; want %d", len(indexManifest.Manifests), len(want))
			}
			for _, got := range indexManifest.Manifests {
				if got.Platform == nil {
					t.Fatalf("manifest %s has no platform", got.Digest)
				}
				expected, ok := want[got.Platform.Architecture]
				if !ok {
					t.Fatalf("unexpected platform %s", got.Platform)
				}
				if got.Digest != expected.Digest || got.Size != expected.Size || got.MediaType != expected.MediaType || !got.Platform.Equals(*expected.Platform) {
					t.Fatalf("manifest = %+v (platform %s); want %+v (platform %s)", got, got.Platform, expected, expected.Platform)
				}
			}
		})
	}
}

func TestManifestUpdateDuplicatePlatform(t *testing.T) {
	cfg := testImageRegistry(t)
	const repository = "owner/repo"
	platform := v1.Platform{OS: "linux", Architecture: "amd64"}
	pushTestImage(t, cfg, repository, "1.2.3-amd64", platform, nil)
	pushTestImage(t, cfg, repository, "1.2.3-x86_64", platform, nil)

	err := manifestUpdate(cfg, repository, "1.2.3")
	if err == nil || !strings.Contains(err.Error(), "both provide platform linux/amd64") {
		t.Fatalf("manifestUpdate() error = %v; want duplicate platform error", err)
	}
}

func testImageRegistry(t *testing.T) config {
	t.Helper()
	server := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")

	dir, err := imageSystemContextRegistriesConfDir()
	if err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "test-registry.conf")
	if err := os.WriteFile(conf, []byte("[[registry]]\nlocation = \""+host+"\"\ninsecure = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sysregistriesv2.InvalidateCache()
	t.Cleanup(func() {
		_ = os.Remove(conf)
		sysregistriesv2.InvalidateCache()
	})

	return config{
		containerRegistry:         host,
		containerRegistryUsername: "user",
		containerRegistryPassword: "pass",
	}
}

func pushTestImage(t *testing.T, cfg config, repository string, tag string, platform v1.Platform, labels map[string]string) v1.Descriptor {
	t.Helper()
	img, err := random.Image(256, 1)
	if err != nil {
		t.Fatal(err)
	}
	configFile, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	configFile.OS = platform.OS
	configFile.Architecture = platform.Architecture
	configFile.Variant = platform.Variant
	configFile.Config.Labels = labels
	img, err = mutate.ConfigFile(img, configFile)
	if err != nil {
		t.Fatal(err)
	}

	ref, err := name.ParseReference(dockerImageName(cfg.containerRegistry, repository, tag), name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatalf("remote.Write(%s) error = %v", ref, err)
	}

	digest, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	size, err := img.Size()
	if err != nil {
		t.Fatal(err)
	}
	mediaType, err := img.MediaType()
	if err != nil {
		t.Fatal(err)
	}
	return v1.Descriptor{MediaType: mediaType, Digest: digest, Size: size, Platform: &platform}
}
