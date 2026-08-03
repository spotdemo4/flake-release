package flakerelease

import (
	"fmt"
	"regexp"
)

var strictPackageVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func requireStrictPackageVersion(version string) error {
	if !strictPackageVersion.MatchString(version) {
		return fmt.Errorf("version %q is not a strict semantic version", version)
	}
	return nil
}
