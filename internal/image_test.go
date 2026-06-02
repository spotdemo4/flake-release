package flakerelease

import (
	"os"
	"path/filepath"
	"testing"

	"go.podman.io/image/v5/pkg/sysregistriesv2"
)

func TestDockerImageName(t *testing.T) {
	got := dockerImageName("GHCR.IO", "Owner/Repo", "v1.2.3")
	want := "ghcr.io/owner/repo:v1.2.3"
	if got != want {
		t.Fatalf("dockerImageName() = %q; want %q", got, want)
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
