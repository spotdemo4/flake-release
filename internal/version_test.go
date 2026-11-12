package flakerelease

import "testing"

func TestCurrentVersion(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	for _, test := range []struct {
		value string
		want  string
	}{
		{value: "1.2.3", want: "1.2.3"},
		{value: "v1.2.3", want: "1.2.3"},
	} {
		version = test.value
		if got := currentVersion(); got != test.want {
			t.Errorf("currentVersion() with version %q = %q; want %q", test.value, got, test.want)
		}
	}

	version = ""
	if got := currentVersion(); got == "" {
		t.Error("currentVersion() without version is empty")
	}
}

func TestParseRunArgsVersion(t *testing.T) {
	cfg := config{}
	if _, mode := parseRunArgs(&cfg, []string{"packages.one", "--version"}); mode != runVersion {
		t.Fatalf("parseRunArgs(--version) mode = %v; want version", mode)
	}
}

func TestRunVersion(t *testing.T) {
	if err := Run([]string{"--version"}); err != nil {
		t.Fatal(err)
	}
}
