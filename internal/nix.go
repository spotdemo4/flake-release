package flakerelease

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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

const nixDiagnosticLimit = 1024 * 1024

type tailBuffer struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit}
}

func (buffer *tailBuffer) Write(value []byte) (int, error) {
	written := len(value)
	buffer.mu.Lock()
	defer buffer.mu.Unlock()

	if buffer.limit <= 0 {
		buffer.truncated = buffer.truncated || written > 0
		return written, nil
	}
	if len(value) >= buffer.limit {
		buffer.data = append(buffer.data[:0], value[len(value)-buffer.limit:]...)
		buffer.truncated = true
		return written, nil
	}
	if overflow := len(buffer.data) + len(value) - buffer.limit; overflow > 0 {
		copy(buffer.data, buffer.data[overflow:])
		buffer.data = buffer.data[:len(buffer.data)-overflow]
		buffer.truncated = true
	}
	buffer.data = append(buffer.data, value...)
	return written, nil
}

func (buffer *tailBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.truncated {
		return "[earlier output omitted]\n" + string(buffer.data)
	}
	return string(buffer.data)
}

func setupNixConfig() {
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
	return nixCapture("eval", "--impure", "--raw", "--expr", "builtins.currentSystem")
}

func nixPkgPath(pkg string) (string, error) {
	return nixCapture("eval", "--raw", ".#"+pkg)
}

func nixPkgSrc(pkg string) (string, error) {
	return nixPkgSrcWithCapture(pkg, nixCapture)
}

func nixPkgSrcWithCapture(pkg string, capture func(...string) (string, error)) (string, error) {
	out, err := capture("eval", "--json", ".#"+pkg+".src")
	if err != nil || out == "" || out == "null" {
		return "", nil
	}
	var path string
	if err := json.Unmarshal([]byte(out), &path); err != nil || path == "" {
		return "", nil
	}
	stat, err := os.Stat(path)
	if err != nil || !stat.IsDir() {
		return "", nil
	}
	return path, nil
}

func nixPkgPname(pkg string) string {
	pname, err := nixCapture("eval", "--raw", ".#"+pkg+".pname")
	if err == nil {
		return pname
	}
	return ""
}

func nixPkgVersion(pkg string) string {
	version, err := nixCapture("eval", "--raw", ".#"+pkg+".version")
	if err == nil {
		return version
	}
	return ""
}

func nixPkgMainProgram(pkg string) string {
	mainProgram, err := nixCapture("eval", "--raw", ".#"+pkg+".meta.mainProgram")
	if err == nil {
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

	return p
}

func nixImageName(pkg string) string {
	imageName, err := nixCapture("eval", "--raw", ".#"+pkg+".imageName")
	if err == nil {
		return imageName
	}
	return ""
}

func nixImageTag(pkg string) string {
	imageTag, err := nixCapture("eval", "--raw", ".#"+pkg+".imageTag")
	if err == nil {
		return imageTag
	}
	return ""
}

func nixBuildLinked(pkg string, outLink string) error {
	return nixRun("build", ".#"+pkg, "--out-link", outLink)
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

	if err := nixRun("bundle", "--bundler", "github:spotdemo4/trevpkgs#appimage", ".#"+pkg, "-o", tmpLink); err != nil {
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
	command := nixCommandString(args...)

	if os.Getenv("DEBUG") != "" {
		detail("command: %s", command)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return nixCommandError(command, err, "")
		}
		return nil
	}

	if os.Getenv("CI") != "" {
		fmt.Fprintf(os.Stderr, "::group::%s\n", command)
		defer fmt.Fprintln(os.Stderr, "::endgroup::")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return nixCommandError(command, err, "")
		}
		return nil
	}

	output := newTailBuffer(nixDiagnosticLimit)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return nixCommandError(command, err, output.String())
	}
	return nil
}

func nixCapture(args ...string) (string, error) {
	cmd := exec.Command("nix", args...)
	command := nixCommandString(args...)

	var stdout bytes.Buffer
	stderr := newTailBuffer(nixDiagnosticLimit)
	cmd.Stdout = &stdout
	if os.Getenv("DEBUG") != "" {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = stderr
	}

	if err := cmd.Run(); err != nil {
		return "", nixCommandError(command, err, stderr.String())
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

func nixCaptureLogged(args ...string) (string, error) {
	cmd := exec.Command("nix", args...)
	command := nixCommandString(args...)

	var stdout bytes.Buffer
	stderr := newTailBuffer(nixDiagnosticLimit)
	cmd.Stdout = &stdout
	if os.Getenv("DEBUG") != "" {
		detail("command: %s", command)
		cmd.Stderr = os.Stderr
	} else if os.Getenv("CI") != "" {
		fmt.Fprintf(os.Stderr, "::group::%s\n", command)
		defer fmt.Fprintln(os.Stderr, "::endgroup::")
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = stderr
	}

	if err := cmd.Run(); err != nil {
		return "", nixCommandError(command, err, stderr.String())
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

func nixCommandError(command string, err error, output string) error {
	output = strings.TrimSpace(output)
	if output != "" {
		return fmt.Errorf("%s failed: %w\n%s", command, err, output)
	}
	return fmt.Errorf("%s failed: %w", command, err)
}

func nixCommandString(args ...string) string {
	parts := append([]string{"nix"}, args...)
	return strings.Join(parts, " ")
}
