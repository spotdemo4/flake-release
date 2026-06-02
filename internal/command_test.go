package flakerelease

import "testing"

func TestCommandString(t *testing.T) {
	got := commandString("git", "status", "--short")
	want := "git status --short"
	if got != want {
		t.Fatalf("commandString() = %q; want %q", got, want)
	}
}
