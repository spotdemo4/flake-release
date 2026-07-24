package flakerelease

import (
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type dynamicExecutable struct {
	src string
	dst string
}

type dynamicLibrary struct {
	src string
	dst string
}

type bundledOutput struct {
	packageOutput
	root            string
	flattenedSource string
	flattenedPath   string
}

func preparePackageBundle(outputs []packageOutput, osName string, archName string) (string, error) {
	if len(outputs) == 0 {
		return "", errors.New("package has no outputs")
	}

	outputs = append([]packageOutput(nil), outputs...)
	sort.Slice(outputs, func(i int, j int) bool {
		return outputs[i].Name < outputs[j].Name
	})

	bundle, err := os.MkdirTemp("", "flake-release-package-*")
	if err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			deletePath(bundle)
		}
	}()

	flattenedSource := singleOutputExecutable(outputs)
	var bundled []bundledOutput
	for _, output := range outputs {
		if !validPackageOutputName(output.Name) {
			return "", fmt.Errorf("invalid package output name %q", output.Name)
		}

		stat, err := os.Stat(output.Path)
		if err != nil {
			return "", err
		}

		root := filepath.Join(bundle, output.Name)
		if len(outputs) == 1 && output.Name == "out" {
			root = bundle
		}
		item := bundledOutput{packageOutput: output, root: root}

		switch {
		case flattenedSource != "":
			item.flattenedSource = flattenedSource
			item.flattenedPath = filepath.Join(root, filepath.Base(flattenedSource))
			resolved, err := filepath.EvalSymlinks(flattenedSource)
			if err != nil {
				return "", err
			}
			if !pathWithin(resolved, output.Path) && !pathWithin(resolved, "/nix/store") {
				return "", fmt.Errorf("executable symlink %s points outside its output", flattenedSource)
			}
			info, err := os.Stat(flattenedSource)
			if err != nil {
				return "", err
			}
			if err := copyFile(flattenedSource, item.flattenedPath, info); err != nil {
				return "", err
			}
		case stat.IsDir() && root == bundle:
			if err := copyDirectoryContents(output.Path, root); err != nil {
				return "", err
			}
		case stat.IsDir():
			if err := copyPathWritable(output.Path, root); err != nil {
				return "", err
			}
		default:
			if err := os.MkdirAll(root, 0o755); err != nil {
				return "", err
			}
			item.flattenedSource = output.Path
			item.flattenedPath = filepath.Join(root, filepath.Base(output.Path))
			if err := copyPathWritable(output.Path, item.flattenedPath); err != nil {
				return "", err
			}
		}
		bundled = append(bundled, item)
	}

	if osName == "linux" {
		for _, output := range bundled {
			if output.Name != "out" && output.Name != "bin" {
				continue
			}
			executables, err := output.dynamicExecutables()
			if err != nil {
				return "", err
			}
			if len(executables) == 0 {
				continue
			}

			interpreter, err := linuxInterpreter(archName)
			if err != nil {
				return "", err
			}
			libraries, replacements, err := bundleDynamicLibraries(output.root, executables)
			if err != nil {
				return "", err
			}
			if err := patchDynamicBundle(output.root, executables, libraries, replacements, interpreter); err != nil {
				return "", err
			}
		}
	}

	cleanup = false
	return bundle, nil
}

func singleOutputExecutable(outputs []packageOutput) string {
	if len(outputs) != 1 || outputs[0].Name != "out" {
		return ""
	}
	stat, err := os.Stat(outputs[0].Path)
	if err != nil || !stat.IsDir() {
		return ""
	}
	files, err := findFiles(outputs[0].Path)
	if err != nil || len(files) != 1 || !executable(files[0]) {
		return ""
	}
	relative, err := filepath.Rel(outputs[0].Path, files[0])
	if err != nil || filepath.Dir(relative) != "bin" {
		return ""
	}
	return files[0]
}

func (output bundledOutput) dynamicExecutables() ([]dynamicExecutable, error) {
	stat, err := os.Stat(output.Path)
	if err != nil {
		return nil, err
	}

	var files []string
	if stat.IsDir() {
		files, err = findFiles(filepath.Join(output.Path, "bin"))
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	} else {
		files = []string{output.Path}
	}

	var executables []dynamicExecutable
	for _, file := range files {
		if !isDynamicELFPath(file) {
			continue
		}

		dst := output.flattenedPath
		if file != output.flattenedSource {
			relative, err := filepath.Rel(output.Path, file)
			if err != nil {
				return nil, err
			}
			dst = filepath.Join(output.root, relative)
		}
		relativeParent, err := filepath.Rel(output.root, filepath.Dir(dst))
		if err != nil {
			return nil, err
		}
		if err := materializeDirectoryTree(output.root, relativeParent); err != nil {
			return nil, err
		}
		if err := materializeDynamicExecutable(file, dst, output.Path); err != nil {
			return nil, err
		}
		executables = append(executables, dynamicExecutable{src: file, dst: dst})
	}
	return executables, nil
}

func bundleDynamicLibraries(bundle string, executables []dynamicExecutable) ([]dynamicLibrary, map[string]string, error) {
	libDir := filepath.Join(bundle, "lib")
	copied := map[string]string{}
	queued := make([]string, 0, len(executables))
	for _, executable := range executables {
		queued = append(queued, executable.src)
	}

	var libraries []dynamicLibrary
	replacements := map[string]string{}
	for len(queued) > 0 {
		path := queued[0]
		queued = queued[1:]

		dependencies, err := elfDependencies(path)
		if err != nil {
			return nil, nil, err
		}
		for _, dependency := range dependencies {
			name, err := libraryBundleName(dependency)
			if err != nil {
				return nil, nil, err
			}
			if name != dependency {
				replacements[dependency] = name
			}
			if isGlibcLibrary(name) {
				continue
			}

			lib, err := resolveELFLibrary(path, dependency)
			if err != nil {
				return nil, nil, err
			}
			realLib, err := filepath.EvalSymlinks(lib)
			if err != nil {
				return nil, nil, err
			}
			if existing, ok := copied[name]; ok {
				if existing != realLib {
					return nil, nil, fmt.Errorf("conflicting libraries for %s: %s and %s", name, existing, realLib)
				}
				continue
			}

			if err := materializeDirectoryTree(bundle, "lib"); err != nil {
				return nil, nil, err
			}
			info, err := os.Stat(lib)
			if err != nil {
				return nil, nil, err
			}
			dst := filepath.Join(libDir, name)
			if info, err := os.Lstat(dst); err == nil && info.Mode()&os.ModeSymlink != 0 {
				if err := os.Remove(dst); err != nil {
					return nil, nil, err
				}
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, nil, err
			}
			if err := copyFile(lib, dst, info); err != nil {
				return nil, nil, err
			}

			copied[name] = realLib
			queued = append(queued, lib)
			libraries = append(libraries, dynamicLibrary{src: lib, dst: dst})
		}
	}
	return libraries, replacements, nil
}

func libraryBundleName(dependency string) (string, error) {
	name := filepath.Base(filepath.Clean(dependency))
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("invalid ELF dependency %q", dependency)
	}
	return name, nil
}

func patchDynamicBundle(root string, executables []dynamicExecutable, libraries []dynamicLibrary, replacements map[string]string, interpreter string) error {
	for _, executable := range executables {
		if err := makeWritable(executable.dst); err != nil {
			return err
		}
		rpath, err := dynamicRPath(root, executable.dst)
		if err != nil {
			return err
		}
		if err := patchELFDependencies(executable.dst, replacements); err != nil {
			return err
		}
		if err := patchelf("--set-interpreter", interpreter, "--set-rpath", rpath, executable.dst); err != nil {
			return err
		}
	}
	for _, library := range libraries {
		if err := makeWritable(library.dst); err != nil {
			return err
		}
		if err := patchELFDependencies(library.dst, replacements); err != nil {
			return err
		}
		if err := patchelf("--set-rpath", "$ORIGIN", library.dst); err != nil {
			return err
		}
	}
	return nil
}

func patchELFDependencies(path string, replacements map[string]string) error {
	names := make([]string, 0, len(replacements))
	for name := range replacements {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := patchelf("--replace-needed", name, replacements[name], path); err != nil {
			return err
		}
	}
	return nil
}

func dynamicRPath(root string, executablePath string) (string, error) {
	relative, err := filepath.Rel(filepath.Dir(executablePath), filepath.Join(root, "lib"))
	if err != nil {
		return "", err
	}
	if relative == "." {
		return "$ORIGIN", nil
	}
	return "$ORIGIN/" + filepath.ToSlash(relative), nil
}

func isNativeBinary(path string) bool {
	return isStatic(path) || isDynamicELFPath(path)
}

func validPackageOutputName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsAny(name, `/\`)
}

func copyDirectoryContents(src string, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := copyPathWritable(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyPathWritable(src string, dst string) error {
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
		mode := info.Mode().Perm() | 0o200
		if err := os.MkdirAll(dst, mode); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyPathWritable(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
				return err
			}
		}
		return os.Chmod(dst, mode)
	case info.Mode().IsRegular():
		return copyFile(src, dst, info)
	default:
		return errors.New("unsupported file type: " + src)
	}
}

func materializeDynamicExecutable(src string, dst string, sourceRoot string) error {
	info, err := os.Lstat(dst)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	if !pathWithin(resolved, sourceRoot) && !pathWithin(resolved, "/nix/store") {
		return fmt.Errorf("executable symlink %s points outside its output", src)
	}
	if err := os.Remove(dst); err != nil {
		return err
	}
	info, err = os.Stat(src)
	if err != nil {
		return err
	}
	return copyFile(src, dst, info)
}

func materializeDirectoryTree(root string, relative string) error {
	relative = filepath.Clean(relative)
	if relative == "." {
		return nil
	}
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("directory %q escapes bundle root", relative)
	}

	current := root
	for part := range strings.SplitSeq(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return err
			}
			if !pathWithin(resolved, root) && !pathWithin(resolved, "/nix/store") {
				return fmt.Errorf("directory symlink %s points outside the bundle", current)
			}
			if pathWithin(current, resolved) {
				return fmt.Errorf("directory symlink %s points to an ancestor", current)
			}
			resolvedInfo, err := os.Stat(resolved)
			if err != nil {
				return err
			}
			if !resolvedInfo.IsDir() {
				return fmt.Errorf("directory symlink %s does not point to a directory", current)
			}

			temporary, err := os.MkdirTemp("", "flake-release-directory-*")
			if err != nil {
				return err
			}
			if err := copyDirectoryContents(resolved, temporary); err != nil {
				deletePath(temporary)
				return err
			}
			if err := os.Chmod(temporary, resolvedInfo.Mode().Perm()|0o200); err != nil {
				deletePath(temporary)
				return err
			}
			if err := os.Remove(current); err != nil {
				deletePath(temporary)
				return err
			}
			if err := os.Rename(temporary, current); err != nil {
				deletePath(temporary)
				return err
			}
			continue
		}
		if !info.IsDir() {
			return fmt.Errorf("bundle path %s is not a directory", current)
		}
	}
	return nil
}

func pathWithin(path string, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func isDynamicELFPath(path string) bool {
	dynamic, err := isDynamicELF(path)
	return err == nil && dynamic
}

func isDynamicELF(path string) (bool, error) {
	if !executable(path) {
		return false, nil
	}

	file, err := elf.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	if file.Type != elf.ET_EXEC && file.Type != elf.ET_DYN {
		return false, nil
	}
	if hasELFInterpreter(file) {
		return true, nil
	}

	libraries, err := file.ImportedLibraries()
	if err != nil {
		return false, err
	}
	return len(libraries) > 0, nil
}

func elfDependencies(path string) ([]string, error) {
	file, err := elf.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return file.ImportedLibraries()
}

func resolveELFLibrary(path string, dependency string) (string, error) {
	if strings.ContainsRune(dependency, filepath.Separator) {
		if fileExists(dependency) {
			return dependency, nil
		}
		return "", fmt.Errorf("could not resolve %s needed by %s", dependency, path)
	}

	paths, err := elfSearchPaths(path)
	if err != nil {
		return "", err
	}
	for _, searchPath := range paths {
		candidate := filepath.Join(searchPath, dependency)
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not resolve %s needed by %s", dependency, path)
}

func elfSearchPaths(path string) ([]string, error) {
	file, err := elf.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var paths []string
	for _, tag := range []elf.DynTag{elf.DT_RUNPATH, elf.DT_RPATH} {
		values, err := file.DynString(tag)
		if err != nil {
			continue
		}
		for _, value := range values {
			for searchPath := range strings.SplitSeq(value, ":") {
				if searchPath == "" {
					continue
				}
				searchPath = expandELFOrigin(searchPath, path)
				if searchPath != "" && !slices.Contains(paths, searchPath) {
					paths = append(paths, searchPath)
				}
			}
		}
	}
	return paths, nil
}

func expandELFOrigin(path string, binary string) string {
	origin := filepath.Dir(binary)
	path = strings.ReplaceAll(path, "$ORIGIN", origin)
	path = strings.ReplaceAll(path, "${ORIGIN}", origin)
	return filepath.Clean(path)
}

func hasELFInterpreter(file *elf.File) bool {
	for _, program := range file.Progs {
		if program.Type == elf.PT_INTERP {
			return true
		}
	}
	return false
}

func isGlibcLibrary(name string) bool {
	name = filepath.Base(name)
	return strings.HasPrefix(name, "ld-linux-") || strings.HasPrefix(name, "ld64.so") || slices.Contains([]string{
		"ld.so.1",
		"libanl.so.1",
		"libBrokenLocale.so.1",
		"libc.so.6",
		"libdl.so.2",
		"libm.so.6",
		"libmvec.so.1",
		"libnsl.so.1",
		"libnss_compat.so.2",
		"libnss_dns.so.2",
		"libnss_files.so.2",
		"libnss_hesiod.so.2",
		"libpthread.so.0",
		"libresolv.so.2",
		"librt.so.1",
		"libthread_db.so.1",
		"libutil.so.1",
	}, name)
}

func linuxInterpreter(archName string) (string, error) {
	switch archName {
	case "386":
		return "/lib/ld-linux.so.2", nil
	case "amd64":
		return "/lib64/ld-linux-x86-64.so.2", nil
	case "arm":
		return "/lib/ld-linux-armhf.so.3", nil
	case "arm64":
		return "/lib/ld-linux-aarch64.so.1", nil
	case "loong64":
		return "/lib64/ld-linux-loongarch-lp64d.so.1", nil
	case "ppc64le":
		return "/lib64/ld64.so.2", nil
	case "riscv64":
		return "/lib/ld-linux-riscv64-lp64d.so.1", nil
	case "s390x":
		return "/lib/ld64.so.1", nil
	default:
		return "", fmt.Errorf("unsupported linux architecture %q", archName)
	}
}

func fileExists(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.Mode().IsRegular()
}

func makeWritable(path string) error {
	stat, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.Chmod(path, stat.Mode().Perm()|0o200)
}

func patchelf(args ...string) error {
	cmd := exec.Command("patchelf", args...)
	if os.Getenv("DEBUG") != "" {
		info("patchelf %s", strings.Join(args, " "))
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message != "" {
			return fmt.Errorf("patchelf %s: %w: %s", strings.Join(args, " "), err, message)
		}
		return fmt.Errorf("patchelf %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
