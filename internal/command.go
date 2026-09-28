package flakerelease

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type commandOptions struct {
	name    string
	args    []string
	dir     string
	env     []string
	secrets []string
}

type packageCommandRunner interface {
	available(name string) bool
	require(name string) error
	run(options commandOptions) error
	capture(options commandOptions) (string, error)
}

type execPackageCommandRunner struct{}

func (execPackageCommandRunner) available(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (execPackageCommandRunner) require(name string) error {
	return requireCommand(name)
}

func (execPackageCommandRunner) run(options commandOptions) error {
	return runCommand(options)
}

func (execPackageCommandRunner) capture(options commandOptions) (string, error) {
	return captureCommand(options)
}

// nixCommandPackages lists the nixpkgs attributes that provide a command missing from
// PATH, along with the toolchain the command needs to run.
var nixCommandPackages = map[string][]string{
	"cargo":    {"cargo", "rustc", "stdenv.cc"},
	"go":       {"go"},
	"gradle":   {"gradle"},
	"mvn":      {"maven"},
	"npm":      {"nodejs"},
	"patchelf": {"patchelf"},
	"uv":       {"uv", "python3"},
}

// requireCommand ensures name is on PATH. A command missing from PATH is built from the
// flake's nixpkgs input, or the registry's nixpkgs when the flake has none, and its
// bin directories are appended to PATH.
func requireCommand(name string) error {
	_, err := exec.LookPath(name)
	if err == nil {
		return nil
	}
	attrs, ok := nixCommandPackages[name]
	if !ok {
		return fmt.Errorf("required command %q was not found: %w", name, err)
	}
	if err := addNixPackagesToPath(name, attrs); err != nil {
		return fmt.Errorf("required command %q was not found on PATH or in nixpkgs: %w", name, err)
	}
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("required command %q was not found in nixpkgs#%s: %w", name, attrs[0], err)
	}
	return nil
}

func addNixPackagesToPath(name string, attrs []string) error {
	installables := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		installables = append(installables, "nixpkgs#"+attr)
	}
	info("%s is not on PATH, using %s", name, strings.Join(installables, " "))
	args := append([]string{"build", "--no-link", "--print-out-paths", "--inputs-from", "."}, installables...)
	output, err := nixCaptureLogged(args...)
	if err != nil {
		return err
	}
	path := filepath.SplitList(os.Getenv("PATH"))
	for storePath := range strings.FieldsSeq(output) {
		bin := filepath.Join(storePath, "bin")
		if stat, err := os.Stat(bin); err == nil && stat.IsDir() && !slices.Contains(path, bin) {
			path = append(path, bin)
		}
	}
	return os.Setenv("PATH", strings.Join(path, string(filepath.ListSeparator)))
}

func runCommand(options commandOptions) error {
	_, err := captureCommand(options)
	return err
}

func captureCommand(options commandOptions) (string, error) {
	if options.name == "" {
		return "", fmt.Errorf("command name is empty")
	}

	display := redactSecrets(commandString(options.name, options.args...), options.secrets)
	detail("command: %s", display)

	cmd := exec.Command(options.name, options.args...)
	cmd.Dir = options.dir
	cmd.Env = commandEnvironment(options.env)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	text := strings.TrimSpace(stdout.String())
	if stderrText := strings.TrimSpace(stderr.String()); stderrText != "" {
		if text != "" {
			text += "\n"
		}
		text += stderrText
	}
	text = redactSecrets(text, options.secrets)
	if text != "" && os.Getenv("DEBUG") != "" {
		detailBlock(text)
	}
	if err != nil {
		if text != "" {
			return "", fmt.Errorf("%s failed: %w: %s", display, err, text)
		}
		return "", fmt.Errorf("%s failed: %w", display, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func commandEnvironment(overrides []string) []string {
	if len(overrides) == 0 {
		return os.Environ()
	}
	overridden := make(map[string]bool, len(overrides))
	for _, value := range overrides {
		if key, _, ok := strings.Cut(value, "="); ok {
			overridden[key] = true
		}
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, value := range os.Environ() {
		key, _, ok := strings.Cut(value, "=")
		if !ok || !overridden[key] {
			environment = append(environment, value)
		}
	}
	return append(environment, overrides...)
}

func commandString(name string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, strconv.Quote(name))
	for _, arg := range args {
		parts = append(parts, strconv.Quote(arg))
	}
	return strings.Join(parts, " ")
}

func redactSecrets(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}
