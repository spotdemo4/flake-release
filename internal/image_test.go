package flakerelease

import (
	"os"
	"path/filepath"
	"testing"

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
		registryUsername: "user",
		registryPassword: "pass",
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
