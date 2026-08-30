package flakerelease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureCommandSeparatesOutputStreams(t *testing.T) {
	writeScript := func(name string, contents string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+contents), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}

	success := writeScript("success", "printf '%s\\n' 'trev.zip/llc/flake-release'\nprintf '%s\\n' 'go: downloading go1.27.0 (linux/amd64)' >&2\n")
	output, err := captureCommand(commandOptions{name: success})
	if err != nil {
		t.Fatal(err)
	}
	if output != "trev.zip/llc/flake-release" {
		t.Fatalf("captured output = %q; want stdout only", output)
	}

	failure := writeScript("failure", "printf '%s\\n' 'stdout details'\nprintf '%s\\n' 'stderr secret' >&2\nexit 1\n")
	_, err = captureCommand(commandOptions{name: failure, secrets: []string{"secret"}})
	if err == nil {
		t.Fatal("failing command returned no error")
	}
	if !strings.Contains(err.Error(), "stdout details\nstderr [REDACTED]") {
		t.Fatalf("command error = %q; want redacted stdout and stderr", err)
	}
}
