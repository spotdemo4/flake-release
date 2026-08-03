package flakerelease

import (
	"os"
	"path/filepath"
	"testing"
)

type recordingReleaseClient struct {
	createCalls int
}

func (client *recordingReleaseClient) createRelease(_ string, _ string) error {
	client.createCalls++
	return nil
}

func (*recordingReleaseClient) uploadAsset(_ string, _ string) error {
	return nil
}

func (*recordingReleaseClient) cleanupAssets(_ string) error {
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

func TestReleaseSessionRejectsRunWithoutOutputs(t *testing.T) {
	client := &recordingReleaseClient{}
	session := releaseSession{client: client, tag: "v1.0.0"}

	if err := session.requireOutput(); err == nil {
		t.Fatal("release session returned no error without outputs")
	}
	if client.createCalls != 0 {
		t.Fatalf("release creation calls = %d; want 0", client.createCalls)
	}
}

func TestReleaseSessionCreatesReleaseOnce(t *testing.T) {
	client := &recordingReleaseClient{}
	session := releaseSession{client: client, tag: "v1.0.0"}

	session.ensureRelease()
	session.ensureRelease()

	if err := session.requireOutput(); err != nil {
		t.Fatal(err)
	}
	if client.createCalls != 1 {
		t.Fatalf("release creation calls = %d; want 1", client.createCalls)
	}
	if !session.created {
		t.Fatal("release session did not record the created release")
	}
}

func TestReleaseSessionDryRunRecordsOutputWithoutCreatingRelease(t *testing.T) {
	client := &recordingReleaseClient{}
	session := releaseSession{cfg: config{dryRun: true}, client: client, tag: "v1.0.0"}

	session.ensureRelease()

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
