package flakerelease

import (
	"os"
	"strings"
	"testing"
)

func TestSetupNixConfig(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("DOCKER", "")
	t.Setenv("GITHUB_TOKEN", "test-token")

	setupNixConfig()

	config := os.Getenv("NIX_CONFIG")
	for _, want := range []string{
		"extra-experimental-features = nix-command flakes\n",
		"accept-flake-config = true\n",
		"warn-dirty = false\n",
		"always-allow-substitutes = true\n",
		"fallback = true\n",
		"access-tokens = github.com=test-token\n",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("NIX_CONFIG = %q; want to contain %q", config, want)
		}
	}
}

func TestNixCommandString(t *testing.T) {
	got := nixCommandString("build", ".#default", "--no-link")
	want := "nix build .#default --no-link"
	if got != want {
		t.Fatalf("nixCommandString() = %q; want %q", got, want)
	}
}

func TestParseNixBuildOutputs(t *testing.T) {
	outputs, err := parseNixBuildOutputs(`[{"drvPath":"/nix/store/package.drv","outputs":{"out":"/nix/store/package","doc":"/nix/store/package-doc","dev":"/nix/store/package-dev"}}]`)
	if err != nil {
		t.Fatal(err)
	}

	want := []packageOutput{
		{Name: "dev", Path: "/nix/store/package-dev"},
		{Name: "doc", Path: "/nix/store/package-doc"},
		{Name: "out", Path: "/nix/store/package"},
	}
	if len(outputs) != len(want) {
		t.Fatalf("output count = %d; want %d", len(outputs), len(want))
	}
	for index := range want {
		if outputs[index] != want[index] {
			t.Fatalf("output %d = %#v; want %#v", index, outputs[index], want[index])
		}
	}
}

func TestParseNixBuildOutputsRejectsEmptyOutputs(t *testing.T) {
	if _, err := parseNixBuildOutputs(`[{"outputs":{}}]`); err == nil {
		t.Fatal("parseNixBuildOutputs returned no error for empty outputs")
	}
}
