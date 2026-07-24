package flakerelease

import (
	"archive/tar"
	"archive/zip"
	"compress/flate"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

func archive(path string, osName string) (string, error) {
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
		if err := zipDirectory(workdir, out); err != nil {
			return "", err
		}
		return out, nil
	}

	out := filepath.Join(outdir, "archive.tar.xz")
	if err := tarXzDirectory(workdir, out); err != nil {
		return "", err
	}
	return out, nil
}

func archiveOutputs(outputs []packageOutput) (string, error) {
	outdir, err := os.MkdirTemp("", "flake-release-outputs-archive-*")
	if err != nil {
		return "", err
	}

	out := filepath.Join(outdir, "archive.tar.xz")
	if err := tarXzOutputs(outputs, out); err != nil {
		return "", err
	}
	return out, nil
}

func tarXzDirectory(root string, out string) error {
	return writeTarXz(out, func(writer *tar.Writer) error {
		return writeTarPath(writer, root, "")
	})
}

func tarXzOutputs(outputs []packageOutput, out string) error {
	return writeTarXz(out, func(writer *tar.Writer) error {
		return writeTarOutputs(writer, outputs)
	})
}

func writeTarOutputs(writer *tar.Writer, outputs []packageOutput) error {
	outputs = append([]packageOutput(nil), outputs...)
	sort.Slice(outputs, func(i int, j int) bool {
		return outputs[i].Name < outputs[j].Name
	})

	for _, output := range outputs {
		if output.Name == "" || output.Name == "." || output.Name == ".." || filepath.Base(output.Name) != output.Name {
			return fmt.Errorf("invalid package output name %q", output.Name)
		}

		stat, err := os.Stat(output.Path)
		if err != nil {
			return err
		}
		if stat.IsDir() {
			if err := writeTarPath(writer, output.Path, output.Name); err != nil {
				return err
			}
			continue
		}

		if err := writer.WriteHeader(&tar.Header{
			Name:     output.Name + "/",
			Mode:     0o755,
			Typeflag: tar.TypeDir,
		}); err != nil {
			return err
		}
		if err := writeTarPath(writer, output.Path, filepath.Join(output.Name, filepath.Base(output.Path))); err != nil {
			return err
		}
	}
	return nil
}

func writeTarXz(out string, writeEntries func(*tar.Writer) error) error {
	file, err := os.Create(out)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()

	xzWriter, err := newLZMAWriter(file)
	if err != nil {
		return err
	}
	tarWriter := tar.NewWriter(xzWriter)

	if err := writeEntries(tarWriter); err != nil {
		_ = tarWriter.Close()
		_ = xzWriter.Close()
		return err
	}

	if err := tarWriter.Close(); err != nil {
		_ = xzWriter.Close()
		return err
	}
	if err := xzWriter.Close(); err != nil {
		return err
	}
	closed = true
	return file.Close()
}

func writeTarPath(writer *tar.Writer, root string, archiveRoot string) error {
	return filepath.WalkDir(root, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root && archiveRoot == "" {
			return nil
		}

		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}

		name := archiveRoot
		if path != root {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			name = filepath.Join(archiveRoot, relative)
		}
		name = filepath.ToSlash(name)

		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = name
		if info.IsDir() {
			header.Name += "/"
		}

		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		src, err := os.Open(path)
		if err != nil {
			return err
		}

		_, copyErr := io.Copy(writer, src)
		closeErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func zipDirectory(root string, out string) error {
	file, err := os.Create(out)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()

	writer := zip.NewWriter(file)
	writer.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestCompression)
	})

	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}

		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}

		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)

		if info.IsDir() {
			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			header.Name = name + "/"
			_, err = writer.CreateHeader(header)
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = name
		header.Method = zip.Deflate

		dst, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}

		src, err := os.Open(path)
		if err != nil {
			return err
		}

		_, copyErr := io.Copy(dst, src)
		closeErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}); err != nil {
		_ = writer.Close()
		return err
	}

	if err := writer.Close(); err != nil {
		return err
	}
	closed = true
	return file.Close()
}

func renameAsset(filepathName string, name string, version string, osName string, arch string) (string, error) {
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
	if err := copyPath(filepathName, final); err != nil {
		return "", err
	}
	return final, nil
}

func copyPath(src string, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case info.IsDir():
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyPath(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
				return err
			}
		}
		return os.Chmod(dst, info.Mode().Perm())
	case info.Mode().IsRegular():
		return copyFile(src, dst, info)
	default:
		return errors.New("unsupported file type: " + src)
	}
}

func copyFile(src string, dst string, info fs.FileInfo) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	mode := info.Mode().Perm()
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o200)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(dstFile, srcFile)
	closeErr := dstFile.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(dst, mode)
}

func isStatic(path string) bool {
	if !executable(path) {
		return false
	}

	if static, err := isStaticELF(path); err == nil {
		return static
	}
	if static, err := isStaticMachO(path); err == nil {
		return static
	}
	if static, err := isStaticPE(path); err == nil {
		return static
	}
	return false
}

func isStaticELF(path string) (bool, error) {
	file, err := elf.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	if file.Type != elf.ET_EXEC && file.Type != elf.ET_DYN {
		return false, nil
	}
	for _, program := range file.Progs {
		if program.Type == elf.PT_INTERP {
			return false, nil
		}
	}

	libraries, err := file.ImportedLibraries()
	if err == nil && len(libraries) > 0 {
		return false, nil
	}
	return true, nil
}

func isStaticMachO(path string) (bool, error) {
	file, err := macho.Open(path)
	if err == nil {
		defer file.Close()
		return file.Type == macho.TypeExec, nil
	}

	fatFile, fatErr := macho.OpenFat(path)
	if fatErr != nil {
		return false, err
	}
	defer fatFile.Close()

	for _, arch := range fatFile.Arches {
		if arch.File.Type != macho.TypeExec {
			return false, nil
		}
	}
	return true, nil
}

func isStaticPE(path string) (bool, error) {
	file, err := pe.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	return file.FileHeader.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE != 0 &&
		file.FileHeader.Characteristics&pe.IMAGE_FILE_DLL == 0, nil
}

func allStatic(path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !stat.IsDir() {
		return isStatic(path)
	}

	binPath := filepath.Join(path, "bin")
	if stat, err := os.Stat(binPath); err != nil || !stat.IsDir() {
		return false
	}

	files, err := findFiles(binPath)
	if err != nil || len(files) == 0 {
		return false
	}
	for _, file := range files {
		if !isStatic(file) {
			return false
		}
	}
	return true
}

func outputsHaveExecutables(outputs []packageOutput) (bool, error) {
	for _, output := range outputs {
		stat, err := os.Stat(output.Path)
		if err != nil {
			return false, err
		}
		if !stat.IsDir() {
			if executable(output.Path) {
				return true, nil
			}
			continue
		}

		files, err := findFiles(filepath.Join(output.Path, "bin"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if slices.ContainsFunc(files, executable) {
			return true, nil
		}
	}
	return false, nil
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
