package flakerelease

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type runner struct{}

func (runner) run(name string, args ...string) error {
	return runCommand("", true, name, args...)
}

func (runner) runDir(dir string, name string, args ...string) error {
	return runCommand(dir, true, name, args...)
}

func (runner) capture(name string, args ...string) (string, error) {
	return captureCommand("", name, args...)
}

func runCommand(dir string, logged bool, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}

	if os.Getenv("DEBUG") != "" {
		if logged {
			info(commandString(name, args...))
		}
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	if os.Getenv("CI") != "" {
		if logged {
			fmt.Fprintf(os.Stderr, "::group::%s\n", commandString(name, args...))
			defer fmt.Fprintln(os.Stderr, "::endgroup::")
		}
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func captureCommand(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if os.Getenv("DEBUG") != "" {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = io.Discard
	}

	err := cmd.Run()
	return strings.TrimRight(stdout.String(), "\n"), err
}

func commandString(name string, args ...string) string {
	parts := append([]string{name}, args...)
	return strings.Join(parts, " ")
}
