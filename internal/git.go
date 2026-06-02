package flakerelease

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

func gitCheckSafe(run runner, dir string) error {
	current, err := user.Current()
	if err == nil {
		if stat, statErr := os.Stat(dir); statErr == nil {
			if sys, ok := stat.Sys().(*syscall.Stat_t); ok && current.Uid == fmt.Sprint(sys.Uid) {
				return nil
			}
		}
	}

	safeDirs, _ := run.capture("git", "config", "--global", "--get-all", "safe.directory")
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		realDir, _ = filepath.Abs(dir)
	}

	for _, safeDir := range strings.Split(safeDirs, "\n") {
		if safeDir == "" {
			continue
		}
		realSafeDir, err := filepath.EvalSymlinks(safeDir)
		if err != nil {
			realSafeDir, _ = filepath.Abs(safeDir)
		}
		if realDir == realSafeDir {
			return nil
		}
	}

	info("adding '%s' to git safe directories", dir)
	return run.run("git", "config", "--global", "--add", "safe.directory", dir)
}

func gitLatestTag(run runner) (string, error) {
	return run.capture("git", "describe", "--tags", "--abbrev=0")
}

func gitUser(run runner) (string, error) {
	return run.capture("git", "config", "user.name")
}

func gitOrigin(run runner) (string, error) {
	return run.capture("git", "remote", "get-url", "origin")
}

func gitChangelog(run runner, tag string) (string, error) {
	file, err := os.CreateTemp("", "flake-release-changelog-*")
	if err != nil {
		return "", err
	}
	_ = file.Close()

	lastTag, err := previousTag(run, tag)
	if err != nil {
		return "", err
	}

	log, err := run.capture("git", "log", "--pretty=format:* %s (%H)", lastTag+".."+tag)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(file.Name(), []byte(sortChangelog(log)), 0o600); err != nil {
		return "", err
	}

	return file.Name(), nil
}

func previousTag(run runner, tag string) (string, error) {
	tagsOut, err := run.capture("git", "tag", "--sort=v:refname")
	if err != nil {
		return "", err
	}

	tags := splitLines(tagsOut)
	for i, candidate := range tags {
		if candidate != tag {
			continue
		}
		if i > 0 {
			return tags[i-1], nil
		}
		return candidate, nil
	}

	return run.capture("git", "rev-list", "--max-parents=0", "HEAD")
}

func sortChangelog(log string) string {
	featRE := regexp.MustCompile(`^\* feat(\(.*\))?!?:`)
	fixRE := regexp.MustCompile(`^\* fix(\(.*\))?!?:`)

	var feat, fix, other []string
	for _, line := range splitLines(log) {
		switch {
		case featRE.MatchString(line):
			feat = append(feat, line)
		case fixRE.MatchString(line):
			fix = append(fix, line)
		default:
			other = append(other, line)
		}
	}

	ordered := append(feat, fix...)
	ordered = append(ordered, other...)
	if len(ordered) == 0 {
		return ""
	}
	return strings.Join(ordered, "\n") + "\n"
}

func splitLines(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(value, "\n"), "\n")
}
