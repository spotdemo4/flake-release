package flakerelease

import (
	"bytes"
	"errors"
	"testing"

	"github.com/fatih/color"
)

func captureHumanOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previousOutput := color.Error
	previousNoColor := color.NoColor
	color.Error = &output
	color.NoColor = true
	t.Cleanup(func() {
		color.Error = previousOutput
		color.NoColor = previousNoColor
	})
	return &output
}

func TestTextStyles(t *testing.T) {
	if got := bold("value"); got != "value" {
		t.Fatalf("bold() = %q; want value", got)
	}
	if got := dim("value"); got != "value" {
		t.Fatalf("dim() = %q; want value", got)
	}
}

func TestStructuredHumanOutput(t *testing.T) {
	output := captureHumanOutput(t)

	section("Evaluating packages")
	item("packages.%s", "default")
	status("building package outputs")
	detail("path: %s", "/nix/store/package")
	itemWarn("build failed: first line\nsecond line")

	want := "\nEvaluating packages\n" +
		"  packages.default\n" +
		"    building package outputs\n" +
		"    path: /nix/store/package\n" +
		"    warning: build failed: first line\n" +
		"             second line\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
}

func TestPrintErrorAlignsJoinedErrors(t *testing.T) {
	output := captureHumanOutput(t)

	PrintError(errors.Join(errors.New("first"), errors.New("second")))

	want := "error: first\n       second\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
}
