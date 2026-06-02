package flakerelease

import (
	"encoding/json"
	"os"
	"os/exec"
)

type platform struct {
	OS   string `json:"GOOS"`
	Arch string `json:"GOARCH"`
}

func setupNixConfig() {
	if os.Getenv("DOCKER") == "true" && os.Getenv("CI") != "" {
		user := os.Getenv("USER")
		home := os.Getenv("HOME")
		if user != "" && home != "" {
			_ = exec.Command("chown", "-R", user+":"+user, home).Run()
		}
	}

	config := "extra-experimental-features = nix-command flakes\n"
	config += "accept-flake-config = true\n"
	config += "warn-dirty = false\n"
	config += "always-allow-substitutes = true\n"
	config += "fallback = true\n"

	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		config += "access-tokens = github.com=" + token + "\n"
	}

	_ = os.Setenv("NIX_CONFIG", config)
}

func nixSystem(run runner) (string, error) {
	system, err := run.capture("nix", "eval", "--impure", "--raw", "--expr", "builtins.currentSystem")
	if err == nil && system != "" {
		info(dim("system: %s"), system)
	}
	return system, err
}

func nixPkgPath(run runner, pkg string) (string, error) {
	path, err := run.capture("nix", "eval", "--raw", ".#"+pkg)
	if err == nil && path != "" {
		info(dim("path: %s"), path)
	}
	return path, err
}

func nixPkgPname(run runner, pkg string) string {
	pname, err := run.capture("nix", "eval", "--raw", ".#"+pkg+".pname")
	if err == nil && pname != "" {
		info(dim("pname: %s"), pname)
		return pname
	}
	return ""
}

func nixPkgVersion(run runner, pkg string) string {
	version, err := run.capture("nix", "eval", "--raw", ".#"+pkg+".version")
	if err == nil && version != "" {
		info(dim("version: %s"), version)
		return version
	}
	return ""
}

func nixPkgPlatform(run runner, pkg string) platform {
	out, err := run.capture("nix", "eval", "--json", ".#"+pkg+".stdenv.hostPlatform.go")
	if err != nil || out == "" {
		return platform{}
	}

	var p platform
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		return platform{}
	}

	if p.OS != "" {
		info(dim("os: %s"), p.OS)
	}
	if p.Arch != "" {
		info(dim("arch: %s"), p.Arch)
	}
	return p
}

func nixImageName(run runner, pkg string) string {
	imageName, err := run.capture("nix", "eval", "--raw", ".#"+pkg+".imageName")
	if err == nil && imageName != "" {
		info(dim("image name: %s"), imageName)
		return imageName
	}
	return ""
}

func nixImageTag(run runner, pkg string) string {
	imageTag, err := run.capture("nix", "eval", "--raw", ".#"+pkg+".imageTag")
	if err == nil && imageTag != "" {
		info(dim("image tag: %s"), imageTag)
		return imageTag
	}
	return ""
}

func nixBuild(run runner, pkg string) error {
	return run.run("nix", "build", ".#"+pkg, "--no-link")
}

func nixBundleAppImage(run runner, pkg string) (string, error) {
	tmpLink, err := tempName()
	if err != nil {
		return "", err
	}

	if err := run.run("nix", "bundle", "--bundler", "github:spotdemo4/nur#appimage", ".#"+pkg, "-o", tmpLink); err != nil {
		warn("AppImage bundle failed")
		return "", err
	}

	target, err := os.Readlink(tmpLink)
	if err != nil {
		return "", err
	}

	files, err := findFiles(target)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", os.ErrNotExist
	}
	return files[0], nil
}
