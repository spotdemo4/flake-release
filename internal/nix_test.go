package flakerelease

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestSetupNixConfig(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("DOCKER", "")
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GIT_TOKEN", "git-token")

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

func TestSetupNixConfigIgnoresGitToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GIT_TOKEN", "forgejo-token")

	setupNixConfig()

	if config := os.Getenv("NIX_CONFIG"); strings.Contains(config, "access-tokens") {
		t.Fatalf("NIX_CONFIG = %q; want no access-tokens from GIT_TOKEN", config)
	}
}

func TestNixCommandString(t *testing.T) {
	got := nixCommandString("build", ".#default", "--no-link")
	want := "nix build .#default --no-link"
	if got != want {
		t.Fatalf("nixCommandString() = %q; want %q", got, want)
	}
}

func TestTailBufferKeepsBoundedDiagnosticSuffix(t *testing.T) {
	buffer := newTailBuffer(5)
	if _, err := buffer.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Write([]byte("defg")); err != nil {
		t.Fatal(err)
	}
	want := "[earlier output omitted]\ncdefg"
	if got := buffer.String(); got != want {
		t.Fatalf("tailBuffer.String() = %q; want %q", got, want)
	}
}

func TestNixCommandErrorIncludesCapturedOutput(t *testing.T) {
	err := nixCommandError("nix build .#default", errors.New("exit status 1"), "first line\nsecond line\n")
	want := "nix build .#default failed: exit status 1\nfirst line\nsecond line"
	if err.Error() != want {
		t.Fatalf("nixCommandError() = %q; want %q", err, want)
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

func TestParseReleaseNamespace(t *testing.T) {
	for _, test := range []struct {
		out  string
		want string
		err  string
	}{
		{out: "null"},
		{out: `"server"`, want: "server"},
		{out: `"packages/api"`, want: "packages/api"},
		{out: "1", err: "meta.releaseNamespace must be a string; got 1"},
		{out: `["server"]`, err: "meta.releaseNamespace must be a string"},
		{out: `""`, err: "meta.releaseNamespace must not be empty"},
		{out: `"/nix/store/0123456789abcdfghijklmnpqrsvwxyz-server"`, err: "without leading, trailing or repeated slashes"},
		{out: `"server/"`, err: "without leading, trailing or repeated slashes"},
		{out: `"packages//api"`, err: "without leading, trailing or repeated slashes"},
		{out: `"../server"`, err: "must not contain . or .. path segments"},
		{out: `"my server"`, err: "is not a valid tag namespace"},
		{out: "not json", err: "parsing meta.releaseNamespace"},
	} {
		t.Run(test.out, func(t *testing.T) {
			got, err := parseReleaseNamespace(test.out)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("parseReleaseNamespace() error = %v; want %q", err, test.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("parseReleaseNamespace() = %q; want %q", got, test.want)
			}
		})
	}
}

func TestNixPkgReleaseNamespaceEvaluatesMeta(t *testing.T) {
	var args []string
	got, err := nixPkgReleaseNamespaceWithCapture("packages.x86_64-linux.server", func(values ...string) (string, error) {
		args = values
		return `"server"`, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "server" {
		t.Fatalf("nixPkgReleaseNamespaceWithCapture() = %q; want server", got)
	}
	want := "eval --json .#packages.x86_64-linux.server --apply pkg: pkg.meta.releaseNamespace or null"
	if strings.Join(args, " ") != want {
		t.Fatalf("nix args = %q; want %q", strings.Join(args, " "), want)
	}

	if _, err := nixPkgReleaseNamespaceWithCapture("packages.x86_64-linux.server", func(...string) (string, error) {
		return "", errors.New("evaluation failed")
	}); err == nil || err.Error() != "evaluation failed" {
		t.Fatalf("nixPkgReleaseNamespaceWithCapture() error = %v; want evaluation failed", err)
	}
}
