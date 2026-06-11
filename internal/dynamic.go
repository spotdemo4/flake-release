package flakerelease

import (
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

func dynamicArchive(path string, archName string) (string, error) {
	interpreter, err := linuxInterpreter(archName)
	if err != nil {
		return "", err
	}

	bundle, executables, err := prepareDynamicBundle(path)
	if err != nil {
		return "", err
	}
	defer deletePath(bundle)

	libraries, err := bundleDynamicLibraries(bundle, executables)
	if err != nil {
		return "", err
	}
	if err := patchDynamicBundle(executables, libraries, interpreter); err != nil {
		return "", err
	}

	outdir, err := os.MkdirTemp("", "flake-release-dynamic-archive-*")
	if err != nil {
		return "", err
	}
	out := filepath.Join(outdir, "archive.tar.xz")
	if err := tarXzDirectory(bundle, out); err != nil {
		return "", err
	}
	return out, nil
}

func prepareDynamicBundle(path string) (string, []dynamicExecutable, error) {
	bundle, err := os.MkdirTemp("", "flake-release-dynamic-*")
	if err != nil {
		return "", nil, err
	}

	cleanup := true
	defer func() {
		if cleanup {
			deletePath(bundle)
		}
	}()

	stat, err := os.Stat(path)
	if err != nil {
		return "", nil, err
	}

	var executables []dynamicExecutable
	if !stat.IsDir() {
		binDir := filepath.Join(bundle, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			return "", nil, err
		}

		dst := filepath.Join(binDir, filepath.Base(path))
		if err := copyFile(path, dst, stat); err != nil {
			return "", nil, err
		}
		executables = append(executables, dynamicExecutable{src: path, dst: dst})
	} else {
		if err := copyPathDereference(path, bundle); err != nil {
			return "", nil, err
		}

		binPath := filepath.Join(path, "bin")
		files, err := findFiles(binPath)
		if err != nil {
			return "", nil, err
		}
		for _, file := range files {
			if dynamic, _ := isDynamicELF(file); !dynamic {
				continue
			}

			rel, err := filepath.Rel(path, file)
			if err != nil {
				return "", nil, err
			}
			executables = append(executables, dynamicExecutable{
				src: file,
				dst: filepath.Join(bundle, rel),
			})
		}
	}

	if len(executables) == 0 {
		return "", nil, errors.New("no dynamic ELF executables found")
	}
	cleanup = false
	return bundle, executables, nil
}

func bundleDynamicLibraries(bundle string, executables []dynamicExecutable) ([]dynamicLibrary, error) {
	libDir := filepath.Join(bundle, "lib")
	copied := map[string]string{}
	queued := make([]string, 0, len(executables))
	for _, executable := range executables {
		queued = append(queued, executable.src)
	}

	var libraries []dynamicLibrary
	for len(queued) > 0 {
		path := queued[0]
		queued = queued[1:]

		dependencies, err := elfDependencies(path)
		if err != nil {
			return nil, err
		}
		for _, dependency := range dependencies {
			if isGlibcLibrary(dependency) {
				continue
			}

			lib, err := resolveELFLibrary(path, dependency)
			if err != nil {
				return nil, err
			}
			realLib, err := filepath.EvalSymlinks(lib)
			if err != nil {
				return nil, err
			}
			if existing, ok := copied[dependency]; ok {
				if existing != realLib {
					return nil, fmt.Errorf("conflicting libraries for %s: %s and %s", dependency, existing, realLib)
				}
				continue
			}

			if err := os.MkdirAll(libDir, 0o755); err != nil {
				return nil, err
			}
			info, err := os.Stat(lib)
			if err != nil {
				return nil, err
			}
			dst := filepath.Join(libDir, dependency)
			if err := copyFile(lib, dst, info); err != nil {
				return nil, err
			}

			copied[dependency] = realLib
			queued = append(queued, lib)
			libraries = append(libraries, dynamicLibrary{src: lib, dst: dst})
		}
	}
	return libraries, nil
}

func patchDynamicBundle(executables []dynamicExecutable, libraries []dynamicLibrary, interpreter string) error {
	for _, executable := range executables {
		if err := patchelf("--set-interpreter", interpreter, "--set-rpath", "$ORIGIN/../lib", executable.dst); err != nil {
			return err
		}
	}
	for _, library := range libraries {
		if err := patchelf("--set-rpath", "$ORIGIN", library.dst); err != nil {
			return err
		}
	}
	return nil
}

func allLinuxExecutables(path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !stat.IsDir() {
		return isStatic(path) || isDynamicELFPath(path)
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
		if !isStatic(file) && !isDynamicELFPath(file) {
			return false
		}
	}
	return true
}

func hasDynamicELF(path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !stat.IsDir() {
		return isDynamicELFPath(path)
	}

	files, err := findFiles(filepath.Join(path, "bin"))
	if err != nil {
		return false
	}
	return slices.ContainsFunc(files, isDynamicELFPath)
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

func copyPathDereference(src string, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	switch {
	case info.IsDir():
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyPathDereference(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
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

func fileExists(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.Mode().IsRegular()
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
