package flakerelease

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type platform struct {
	OS   string `json:"GOOS"`
	Arch string `json:"GOARCH"`
}

type packageOutput struct {
	Name string
	Path string
}

type nixBuildResult struct {
	Outputs map[string]string `json:"outputs"`
}

func setupNixConfig() {
	if os.Getenv("DOCKER") == "true" && os.Getenv("CI") != "" {
		userName := os.Getenv("USER")
		home := os.Getenv("HOME")
		if userName != "" && home != "" {
			chownRecursive(userName, home)
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

func chownRecursive(userName string, path string) {
	uid, gid, err := userAndGroupIDs(userName)
	if err != nil {
		return
	}

	_ = filepath.WalkDir(path, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		_ = os.Lchown(path, uid, gid)
		return nil
	})
}

func userAndGroupIDs(userName string) (int, int, error) {
	account, err := user.Lookup(userName)
	if err != nil {
		account, err = user.LookupId(userName)
	}
	if err != nil {
		return 0, 0, err
	}

	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return 0, 0, err
	}

	gidValue := account.Gid
	if group, err := user.LookupGroup(userName); err == nil {
		gidValue = group.Gid
	} else if group, err := user.LookupGroupId(userName); err == nil {
		gidValue = group.Gid
	}

	gid, err := strconv.Atoi(gidValue)
	if err != nil {
		return 0, 0, err
	}
	return uid, gid, nil
}

func nixSystem() (string, error) {
	system, err := nixCapture("eval", "--impure", "--raw", "--expr", "builtins.currentSystem")
	if err == nil && system != "" {
		info(dim("system: %s"), system)
	}
	return system, err
}

func nixPkgPath(pkg string) (string, error) {
	path, err := nixCapture("eval", "--raw", ".#"+pkg)
	if err == nil && path != "" {
		info(dim("path: %s"), path)
	}
	return path, err
}

func nixPkgPname(pkg string) string {
	pname, err := nixCapture("eval", "--raw", ".#"+pkg+".pname")
	if err == nil && pname != "" {
		info(dim("pname: %s"), pname)
		return pname
	}
	return ""
}

func nixPkgVersion(pkg string) string {
	version, err := nixCapture("eval", "--raw", ".#"+pkg+".version")
	if err == nil && version != "" {
		info(dim("version: %s"), version)
		return version
	}
	return ""
}

func nixPkgMainProgram(pkg string) string {
	mainProgram, err := nixCapture("eval", "--raw", ".#"+pkg+".meta.mainProgram")
	if err == nil && mainProgram != "" {
		info(dim("main program: %s"), mainProgram)
		return mainProgram
	}
	return ""
}

func nixPkgPlatform(pkg string) platform {
	out, err := nixCapture("eval", "--json", ".#"+pkg+".stdenv.hostPlatform.go")
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

func nixImageName(pkg string) string {
	imageName, err := nixCapture("eval", "--raw", ".#"+pkg+".imageName")
	if err == nil && imageName != "" {
		info(dim("image name: %s"), imageName)
		return imageName
	}
	return ""
}

func nixImageTag(pkg string) string {
	imageTag, err := nixCapture("eval", "--raw", ".#"+pkg+".imageTag")
	if err == nil && imageTag != "" {
		info(dim("image tag: %s"), imageTag)
		return imageTag
	}
	return ""
}

func nixBuild(pkg string) error {
	return nixRun("build", ".#"+pkg, "--no-link")
}

func nixBuildOutputs(pkg string) ([]packageOutput, error) {
	out, err := nixCaptureLogged("build", ".#"+pkg+"^*", "--no-link", "--json")
	if err != nil {
		return nil, err
	}
	outputs, err := parseNixBuildOutputs(out)
	if err != nil {
		return nil, err
	}
	for _, output := range outputs {
		info(dim("output %s: %s"), output.Name, output.Path)
	}
	return outputs, nil
}

func parseNixBuildOutputs(out string) ([]packageOutput, error) {
	var results []nixBuildResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		return nil, err
	}

	paths := map[string]string{}
	for _, result := range results {
		for name, path := range result.Outputs {
			if name == "" || path == "" {
				continue
			}
			if previous := paths[name]; previous != "" && previous != path {
				return nil, fmt.Errorf("nix build returned conflicting paths for output %q", name)
			}
			paths[name] = path
		}
	}
	if len(paths) == 0 {
		return nil, errors.New("nix build returned no package outputs")
	}

	outputs := make([]packageOutput, 0, len(paths))
	for name, path := range paths {
		outputs = append(outputs, packageOutput{Name: name, Path: path})
	}
	sort.Slice(outputs, func(i int, j int) bool {
		return outputs[i].Name < outputs[j].Name
	})
	return outputs, nil
}

func nixBundleAppImage(pkg string) (string, error) {
	tmpLink, err := tempName()
	if err != nil {
		return "", err
	}
	defer deletePath(tmpLink)

	if err := nixRun("bundle", "--bundler", "github:spotdemo4/nur#appimage", ".#"+pkg, "-o", tmpLink); err != nil {
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

func nixRun(args ...string) error {
	cmd := exec.Command("nix", args...)

	if os.Getenv("DEBUG") != "" {
		info(nixCommandString(args...))
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	if os.Getenv("CI") != "" {
		fmt.Fprintf(os.Stderr, "::group::%s\n", nixCommandString(args...))
		defer fmt.Fprintln(os.Stderr, "::endgroup::")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func nixCapture(args ...string) (string, error) {
	cmd := exec.Command("nix", args...)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if os.Getenv("DEBUG") != "" {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = io.Discard
	}

	err := cmd.Run()
	return strings.TrimRight(stdout.String(), "\n"), err
}

func nixCaptureLogged(args ...string) (string, error) {
	cmd := exec.Command("nix", args...)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if os.Getenv("DEBUG") != "" {
		info(nixCommandString(args...))
		cmd.Stderr = os.Stderr
	} else if os.Getenv("CI") != "" {
		fmt.Fprintf(os.Stderr, "::group::%s\n", nixCommandString(args...))
		defer fmt.Fprintln(os.Stderr, "::endgroup::")
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = io.Discard
	}

	err := cmd.Run()
	return strings.TrimRight(stdout.String(), "\n"), err
}

func nixCommandString(args ...string) string {
	parts := append([]string{"nix"}, args...)
	return strings.Join(parts, " ")
}
