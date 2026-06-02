package flakerelease

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func archive(run runner, path string, osName string) (string, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	if !stat.IsDir() {
		info(dim("archive: skipped"))
		return filepath.EvalSymlinks(path)
	}

	files, err := findFiles(path)
	if err != nil {
		return "", err
	}

	binPath := filepath.Join(path, "bin")
	binFiles, _ := findFiles(binPath)

	workdir := path
	if len(files) == len(binFiles) && len(binFiles) > 0 {
		workdir = binPath
	}

	outdir, err := os.MkdirTemp("", "flake-release-archive-*")
	if err != nil {
		return "", err
	}

	if len(binFiles) == 1 {
		info(dim("archive: skipped"))
		return filepath.EvalSymlinks(binFiles[0])
	}

	if osName == "windows" {
		out := filepath.Join(outdir, "archive.zip")
		if err := run.runDir(workdir, "zip", "-9r", out, "."); err != nil {
			return "", err
		}
		return out, nil
	}

	out := filepath.Join(outdir, "archive.tar.xz")
	if err := run.runDir(workdir, "tar", "-I", "xz -9e", "-chf", out, "."); err != nil {
		return "", err
	}
	return out, nil
}

func renameAsset(run runner, filepathName string, name string, version string, osName string, arch string) (string, error) {
	filename := filepath.Base(filepathName)
	ext := strings.TrimPrefix(filepath.Ext(filename), ".")

	outdir, err := os.MkdirTemp("", "flake-release-asset-*")
	if err != nil {
		return "", err
	}

	var final string
	if ext != "" {
		if strings.EqualFold(ext, "appimage") {
			final = filepath.Join(outdir, name+"_"+version+"_"+arch+"."+ext)
		} else {
			final = filepath.Join(outdir, name+"_"+version+"_"+osName+"_"+arch+"."+ext)
		}
	} else {
		final = filepath.Join(outdir, name+"_"+version+"_"+osName+"_"+arch)
	}

	info(dim("rename: %s -> %s"), filename, final)
	if err := runCommand("", false, "cp", "-R", filepathName, final); err != nil {
		return "", err
	}
	return final, nil
}

func isStatic(run runner, file string) bool {
	encoding, err := run.capture("file", "-b", "--mime-encoding", file)
	if err != nil || encoding != "binary" {
		return false
	}

	info, err := run.capture("file", file)
	return err == nil && !strings.Contains(info, "dynamically linked")
}

func allStatic(run runner, path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !stat.IsDir() {
		return isStatic(run, path)
	}

	binPath := filepath.Join(path, "bin")
	if stat, err := os.Stat(binPath); err != nil || !stat.IsDir() {
		return false
	}

	files, err := findFiles(binPath)
	if err != nil {
		return false
	}
	for _, file := range files {
		if !isStatic(run, file) {
			return false
		}
	}
	return true
}

func findFiles(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		info, err := os.Stat(entryPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if info.IsDir() {
			childFiles, err := findFiles(entryPath)
			if err != nil {
				return nil, err
			}
			files = append(files, childFiles...)
			continue
		}
		files = append(files, entryPath)
	}
	return files, nil
}

func tempName() (string, error) {
	file, err := os.CreateTemp("", "flake-release-*")
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return name, nil
}
