package flakerelease

import (
	"runtime/debug"
	"strings"
)

// version is set at build time with
// -ldflags "-X trev.zip/llc/flake-release/internal.version=...".
var version string

// currentVersion returns the build's version: the one set at build time, otherwise
// the main module's version from `go install ...@version`, otherwise "dev".
func currentVersion() string {
	if version != "" {
		return strings.TrimPrefix(version, "v")
	}
	if build, ok := debug.ReadBuildInfo(); ok && build.Main.Version != "" && build.Main.Version != "(devel)" {
		return strings.TrimPrefix(build.Main.Version, "v")
	}
	return "dev"
}
