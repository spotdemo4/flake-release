package flakerelease

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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
	require(name string) error
	run(options commandOptions) error
	capture(options commandOptions) (string, error)
}

type execPackageCommandRunner struct{}

func (execPackageCommandRunner) require(name string) error {
	return requireCommand(name)
}

func (execPackageCommandRunner) run(options commandOptions) error {
	return runCommand(options)
}

func (execPackageCommandRunner) capture(options commandOptions) (string, error) {
	return captureCommand(options)
}

func requireCommand(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("required command %q was not found: %w", name, err)
	}
	return nil
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
	info(dim("command: %s"), display)

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
	if text != "" && (err != nil || os.Getenv("DEBUG") != "") {
		info("%s", text)
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
