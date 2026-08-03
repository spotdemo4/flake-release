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

func pyPIArtifactIdentity(artifacts []string) (string, string, error) {
	if len(artifacts) == 0 {
		return "", "", fmt.Errorf("no pypi artifacts were provided")
	}
	name, version, err := readPyPIArtifactMetadata(artifacts[0])
	if err != nil {
		return "", "", fmt.Errorf("validating pypi artifact %s: %w", filepath.Base(artifacts[0]), err)
	}
	if err := validatePyPIArtifacts(artifacts[1:], name, version); err != nil {
		return "", "", err
	}
	return name, version, nil
}

func validatePyPIArtifacts(artifacts []string, expectedName string, expectedVersion string) error {
	for _, artifact := range artifacts {
		name, version, err := readPyPIArtifactMetadata(artifact)
		if err != nil {
			return fmt.Errorf("validating pypi artifact %s: %w", filepath.Base(artifact), err)
		}
		if normalizePyPIName(name) != normalizePyPIName(expectedName) {
			return fmt.Errorf("pypi artifact %s name %q does not match package name %q", filepath.Base(artifact), name, expectedName)
		}
		if version != expectedVersion {
			return fmt.Errorf("pypi artifact %s version %q does not match package version %q", filepath.Base(artifact), version, expectedVersion)
		}
	}
	return nil
}

func readPyPIArtifactMetadata(path string) (string, string, error) {
	switch {
	case strings.HasSuffix(path, ".whl"), strings.HasSuffix(path, ".zip"):
		return readPyPIZipMetadata(path)
	case strings.HasSuffix(path, ".tar.gz"), strings.HasSuffix(path, ".tgz"):
		return readPyPITarMetadata(path)
	default:
		return "", "", fmt.Errorf("unsupported distribution format")
	}
}

func readPyPIZipMetadata(path string) (string, string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", "", err
	}
	defer archive.Close()
	for _, file := range archive.File {
		if !strings.HasSuffix(file.Name, "/METADATA") && !strings.HasSuffix(file.Name, "/PKG-INFO") && file.Name != "METADATA" && file.Name != "PKG-INFO" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return "", "", err
		}
		name, version, metadataErr := readCoreMetadata(reader)
		closeErr := reader.Close()
		if metadataErr != nil {
			return "", "", metadataErr
		}
		if closeErr != nil {
			return "", "", closeErr
		}
		return name, version, nil
	}
	return "", "", fmt.Errorf("distribution contains no METADATA or PKG-INFO")
}

func readPyPITarMetadata(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return "", "", err
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", err
		}
		if header.Typeflag != tar.TypeReg || (filepath.Base(header.Name) != "PKG-INFO" && filepath.Base(header.Name) != "METADATA") {
			continue
		}
		return readCoreMetadata(archive)
	}
	return "", "", fmt.Errorf("distribution contains no METADATA or PKG-INFO")
}

func readCoreMetadata(reader io.Reader) (string, string, error) {
	name := ""
	version := ""
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
			name = strings.TrimSpace(value)
		case "version":
			version = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	if name == "" || version == "" {
		return "", "", fmt.Errorf("core metadata has no name or version")
	}
	return name, version, nil
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
