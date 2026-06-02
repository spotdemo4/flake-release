package flakerelease

import "testing"

func TestRunHelp(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("DOCKER", "")
	t.Setenv("GITHUB_TOKEN", "")

	if err := Run([]string{"--help"}); err != nil {
		t.Fatal(err)
	}
}
