package flakerelease

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	gitconfig "github.com/go-git/go-git/v6/plumbing/format/config"
)

func setupContainerEnvironment() error {
	if os.Getenv("DOCKER") != "true" || os.Getenv("CI") == "" {
		return nil
	}

	if err := setupContainerTempDir(); err != nil {
		return fmt.Errorf("preparing container temporary directory: %w", err)
	}

	if userName, home := os.Getenv("USER"), os.Getenv("HOME"); userName != "" && home != "" {
		chownRecursive(userName, home)
	}

	if err := setupContainerGitTrust(); err != nil {
		return fmt.Errorf("trusting container workspace: %w", err)
	}
	return nil
}

func setupContainerTempDir() error {
	if err := checkTempDir(os.TempDir()); err == nil {
		return nil
	}
	if err := checkTempDir("/tmp"); err != nil {
		return err
	}
	return os.Setenv("TMPDIR", "/tmp")
}

func checkTempDir(dir string) error {
	file, err := os.CreateTemp(dir, "flake-release-probe-*")
	if err != nil {
		return err
	}
	return errors.Join(file.Close(), os.Remove(file.Name()))
}

func setupContainerGitTrust() error {
	repo, err := openGitRepository()
	if err != nil {
		return err
	}
	defer closeGitRepository(repo)
	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}
	root, err := filepath.Abs(worktree.Filesystem().Root())
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Nix's libgit2 reads this file independently of Git CLI config overrides.
	return addGitSafeDirectory(filepath.Join(home, ".gitconfig"), root)
}

func addGitSafeDirectory(configPath string, root string) error {
	if info, err := os.Lstat(configPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		configPath, err = filepath.EvalSymlinks(configPath)
		if err != nil {
			return err
		}
	}

	lockPath := configPath + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		_ = lock.Close()
		if !committed {
			_ = os.Remove(lockPath)
		}
	}()

	contents, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		info, err := os.Stat(configPath)
		if err != nil {
			return err
		}
		if err := lock.Chmod(info.Mode().Perm()); err != nil {
			return err
		}
	}
	config := gitconfig.New()
	if err := gitconfig.NewDecoder(bytes.NewReader(contents)).Decode(config); err != nil {
		return fmt.Errorf("reading git config %q: %w", configPath, err)
	}
	trusted := false
	for _, value := range config.Section("safe").OptionAll("directory") {
		if value == "" {
			trusted = false
		} else if value == root {
			trusted = true
		}
	}
	if trusted {
		return nil
	}

	// Append an encoded section to preserve existing comments and formatting.
	updated := bytes.NewBuffer(contents)
	if len(contents) > 0 && contents[len(contents)-1] != '\n' {
		updated.WriteByte('\n')
	}
	addition := gitconfig.New().AddOption("safe", "", "directory", root)
	if err := gitconfig.NewEncoder(updated).Encode(addition); err != nil {
		return err
	}
	if _, err := lock.Write(updated.Bytes()); err != nil {
		return err
	}
	if err := lock.Close(); err != nil {
		return err
	}
	if err := os.Rename(lockPath, configPath); err != nil {
		return err
	}
	committed = true
	return nil
}
