package flakerelease

import "testing"

func TestSplitPackages(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "spaces", input: "a b  c", want: []string{"a", "b", "c"}},
		{name: "newlines", input: "a\nb\n\nc\n", want: []string{"a", "b", "c"}},
		{name: "empty", input: "", want: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := splitPackages(test.input)
			if len(got) != len(test.want) {
				t.Fatalf("splitPackages(%q) length = %d; want %d (%v)", test.input, len(got), len(test.want), got)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("splitPackages(%q)[%d] = %q; want %q", test.input, i, got[i], test.want[i])
				}
			}
		})
	}
}

func TestTruthy(t *testing.T) {
	for _, value := range []string{"true", "TRUE", "1", "yes", "on"} {
		if !truthy(value) {
			t.Fatalf("truthy(%q) = false; want true", value)
		}
	}
	for _, value := range []string{"", "false", "0", "no", "off"} {
		if truthy(value) {
			t.Fatalf("truthy(%q) = true; want false", value)
		}
	}
}

func TestTagVersion(t *testing.T) {
	if got := tagVersion("v1.2.3"); got != "1.2.3" {
		t.Fatalf("tagVersion() = %q; want 1.2.3", got)
	}
	if got := tagVersion("release-1"); got != "release-1" {
		t.Fatalf("tagVersion() = %q; want release-1", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "one", "two"); got != "one" {
		t.Fatalf("firstNonEmpty() = %q; want one", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Fatalf("firstNonEmpty(empty) = %q; want empty", got)
	}
}
