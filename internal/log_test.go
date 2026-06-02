package flakerelease

import "testing"

func TestTextStyles(t *testing.T) {
	if got := bold("value"); got != "value" {
		t.Fatalf("bold() = %q; want value", got)
	}
	if got := dim("value"); got != "value" {
		t.Fatalf("dim() = %q; want value", got)
	}
}
