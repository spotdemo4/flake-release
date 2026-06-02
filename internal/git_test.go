package flakerelease

import "testing"

func TestSortChangelog(t *testing.T) {
	input := "* chore: one (1)\n* fix: two (2)\n* feat(ui): three (3)\n"
	want := "* feat(ui): three (3)\n* fix: two (2)\n* chore: one (1)\n"

	if got := sortChangelog(input); got != want {
		t.Fatalf("sortChangelog() = %q; want %q", got, want)
	}
}

func TestSplitLines(t *testing.T) {
	got := splitLines("one\ntwo\n")
	want := []string{"one", "two"}
	if len(got) != len(want) {
		t.Fatalf("splitLines() length = %d; want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("splitLines()[%d] = %q; want %q", i, got[i], want[i])
		}
	}

	if got := splitLines(""); got != nil {
		t.Fatalf("splitLines(empty) = %v; want nil", got)
	}
}
