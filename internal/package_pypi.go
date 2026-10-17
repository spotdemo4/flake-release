package flakerelease

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// pyPIPrivateClassifier marks a project that PyPI refuses to upload.
const pyPIPrivateClassifier = "Private :: Do Not Upload"

type pyPICoreMetadata struct {
	name        string
	version     string
	classifiers []string
}

// pyPIArtifactIdentity returns the core metadata of the first artifact, after
// checking that every artifact has the same name and version.
func pyPIArtifactIdentity(artifacts []string) (pyPICoreMetadata, error) {
	if len(artifacts) == 0 {
		return pyPICoreMetadata{}, fmt.Errorf("no pypi artifacts were provided")
	}
	metadata, err := readPyPIArtifactMetadata(artifacts[0])
	if err != nil {
		return pyPICoreMetadata{}, fmt.Errorf("validating pypi artifact %s: %w", filepath.Base(artifacts[0]), err)
	}
	if err := validatePyPIArtifacts(artifacts[1:], metadata.name, metadata.version); err != nil {
		return pyPICoreMetadata{}, err
	}
	return metadata, nil
}

func validatePyPIArtifacts(artifacts []string, expectedName string, expectedVersion string) error {
	for _, artifact := range artifacts {
		metadata, err := readPyPIArtifactMetadata(artifact)
		if err != nil {
			return fmt.Errorf("validating pypi artifact %s: %w", filepath.Base(artifact), err)
		}
		if normalizePyPIName(metadata.name) != normalizePyPIName(expectedName) {
			return fmt.Errorf("pypi artifact %s name %q does not match package name %q", filepath.Base(artifact), metadata.name, expectedName)
		}
		if metadata.version != expectedVersion {
			return fmt.Errorf("pypi artifact %s version %q does not match package version %q", filepath.Base(artifact), metadata.version, expectedVersion)
		}
	}
	return nil
}

func isPyPIDistribution(path string) bool {
	for _, suffix := range []string{".whl", ".zip", ".tar.gz", ".tgz"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func readPyPIArtifactMetadata(path string) (pyPICoreMetadata, error) {
	switch {
	case strings.HasSuffix(path, ".whl"), strings.HasSuffix(path, ".zip"):
		return readPyPIZipMetadata(path)
	case strings.HasSuffix(path, ".tar.gz"), strings.HasSuffix(path, ".tgz"):
		return readPyPITarMetadata(path)
	default:
		return pyPICoreMetadata{}, fmt.Errorf("unsupported distribution format")
	}
}

func readPyPIZipMetadata(path string) (pyPICoreMetadata, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return pyPICoreMetadata{}, err
	}
	defer archive.Close()
	for _, file := range archive.File {
		if !strings.HasSuffix(file.Name, "/METADATA") && !strings.HasSuffix(file.Name, "/PKG-INFO") && file.Name != "METADATA" && file.Name != "PKG-INFO" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return pyPICoreMetadata{}, err
		}
		metadata, metadataErr := readCoreMetadata(reader)
		closeErr := reader.Close()
		if metadataErr != nil {
			return pyPICoreMetadata{}, metadataErr
		}
		if closeErr != nil {
			return pyPICoreMetadata{}, closeErr
		}
		return metadata, nil
	}
	return pyPICoreMetadata{}, fmt.Errorf("distribution contains no METADATA or PKG-INFO")
}

func readPyPITarMetadata(path string) (pyPICoreMetadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return pyPICoreMetadata{}, err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return pyPICoreMetadata{}, err
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return pyPICoreMetadata{}, err
		}
		if header.Typeflag != tar.TypeReg || (filepath.Base(header.Name) != "PKG-INFO" && filepath.Base(header.Name) != "METADATA") {
			continue
		}
		return readCoreMetadata(archive)
	}
	return pyPICoreMetadata{}, fmt.Errorf("distribution contains no METADATA or PKG-INFO")
}

func readCoreMetadata(reader io.Reader) (pyPICoreMetadata, error) {
	var metadata pyPICoreMetadata
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			metadata.name = strings.TrimSpace(value)
		case "version":
			metadata.version = strings.TrimSpace(value)
		case "classifier":
			metadata.classifiers = append(metadata.classifiers, strings.TrimSpace(value))
		}
	}
	if err := scanner.Err(); err != nil {
		return pyPICoreMetadata{}, err
	}
	if metadata.name == "" || metadata.version == "" {
		return pyPICoreMetadata{}, fmt.Errorf("core metadata has no name or version")
	}
	return metadata, nil
}

func normalizePyPIName(name string) string {
	var normalized strings.Builder
	separator := false
	for _, character := range strings.ToLower(name) {
		if character == '-' || character == '_' || character == '.' {
			separator = true
			continue
		}
		if separator && normalized.Len() > 0 {
			normalized.WriteByte('-')
		}
		separator = false
		normalized.WriteRune(character)
	}
	return normalized.String()
}
