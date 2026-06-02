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
