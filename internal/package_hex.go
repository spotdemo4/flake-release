package flakerelease

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	hexPMRepository = "hexpm"
	// rebar3HexConfig is a global rebar3 config that loads the rebar3_hex plugin from
	// hex.pm, since nixpkgs does not package it.
	rebar3HexConfig = "{plugins, [{rebar3_hex, \"~> 7.0\"}]}.\n"
	// gleamVersionZeroConfirmation is the text gleam publish asks to be typed before
	// publishing a 0.x version, even with --yes.
	gleamVersionZeroConfirmation = "I am not using semantic versioning"
	// gleamNotPublishing is printed when gleam publish declines without failing.
	gleamNotPublishing = "Not publishing."
)

// hexMetadataField matches a top-level binary field of a Hex package's metadata.config.
var hexMetadataField = regexp.MustCompile(`(?m)^\{<<"(name|version)">>,\s*<<"([^"]*)"(?:/utf8)?>>\}\.`)

type gleamPackageInformation struct {
	Config struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"gleam.toml"`
}

func preflightRebar3Package(set *packagePublicationSet, publication *packagePublication) error {
	if err := set.commands.require("rebar3"); err != nil {
		return err
	}
	configDir, err := set.rebar3ConfigDir()
	if err != nil {
		return err
	}
	outputDir, err := os.MkdirTemp(set.temporaryDir, "hex-")
	if err != nil {
		return err
	}
	// rebar3_hex writes into a tmp directory under --output without creating it.
	if err := os.Mkdir(filepath.Join(outputDir, "tmp"), 0o755); err != nil {
		return err
	}
	if err := set.commands.run(commandOptions{
		name: "rebar3",
		args: []string{"hex", "build", "package", "--repo", hexPMRepository, "--output", outputDir},
		dir:  publication.dir,
		env:  []string{"REBAR_GLOBAL_CONFIG_DIR=" + configDir},
	}); err != nil {
		return err
	}
	files, err := findFiles(outputDir)
	if err != nil {
		return err
	}
	var tarballs []string
	for _, file := range files {
		if strings.HasSuffix(file, ".tar") {
			tarballs = append(tarballs, file)
		}
	}
	if len(tarballs) != 1 {
		return fmt.Errorf("rebar3 hex build produced %d package tarballs; want 1", len(tarballs))
	}
	name, version, err := readHexTarballMetadata(tarballs[0])
	if err != nil {
		return fmt.Errorf("reading hex package %s: %w", filepath.Base(tarballs[0]), err)
	}
	if err := requireStrictPackageVersion(version); err != nil {
		return fmt.Errorf("rebar3 package: %w", err)
	}
	publication.name = name
	publication.version = version
	return set.commands.run(rebar3PublishCommand(set, publication, configDir, true))
}

func publishRebar3Package(set *packagePublicationSet, publication *packagePublication) error {
	configDir, err := set.rebar3ConfigDir()
	if err != nil {
		return err
	}
	return set.commands.run(rebar3PublishCommand(set, publication, configDir, false))
}

// rebar3PublishCommand publishes the package and, when a doc provider is configured,
// its documentation.
func rebar3PublishCommand(set *packagePublicationSet, publication *packagePublication, configDir string, dryRun bool) commandOptions {
	args := []string{"hex", "publish", "--repo", hexPMRepository, "--yes"}
	if dryRun {
		args = append(args, "--dry-run")
	}
	env := []string{"REBAR_GLOBAL_CONFIG_DIR=" + configDir}
	if set.cfg.hexPMToken != "" {
		env = append(env, "HEX_API_KEY="+set.cfg.hexPMToken)
	}
	return commandOptions{
		name:    "rebar3",
		args:    args,
		dir:     publication.dir,
		env:     env,
		secrets: []string{set.cfg.hexPMToken},
	}
}

// rebar3ConfigDir returns a directory for REBAR_GLOBAL_CONFIG_DIR whose global config
// loads rebar3_hex, so projects need not list the plugin themselves.
func (set *packagePublicationSet) rebar3ConfigDir() (string, error) {
	dir := filepath.Join(set.temporaryDir, "rebar3")
	config := filepath.Join(dir, ".config", "rebar3", "rebar.config")
	if isFile(config) {
		return dir, nil
	}
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(config, []byte(rebar3HexConfig), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

// readHexTarballMetadata returns the package name and version from the metadata.config
// of a Hex package tarball.
func readHexTarballMetadata(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = file.Close() }()
	archive := tar.NewReader(file)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return "", "", fmt.Errorf("package contains no metadata.config")
		}
		if err != nil {
			return "", "", err
		}
		if header.Name != "metadata.config" {
			continue
		}
		data, err := io.ReadAll(archive)
		if err != nil {
			return "", "", err
		}
		fields := map[string]string{}
		for _, match := range hexMetadataField.FindAllStringSubmatch(string(data), -1) {
			fields[match[1]] = match[2]
		}
		if fields["name"] == "" || fields["version"] == "" {
			return "", "", fmt.Errorf("metadata.config has no name or version")
		}
		return fields["name"], fields["version"], nil
	}
}

func preflightGleamPackage(set *packagePublicationSet, publication *packagePublication) error {
	// gleam shells out to escript to build Erlang code.
	for _, command := range []string{"gleam", "escript"} {
		if err := set.commands.require(command); err != nil {
			return err
		}
	}
	output, err := set.commands.capture(commandOptions{
		name: "gleam",
		args: []string{"export", "package-information"},
		dir:  publication.dir,
	})
	if err != nil {
		return err
	}
	var information gleamPackageInformation
	if err := json.Unmarshal([]byte(output), &information); err != nil {
		return fmt.Errorf("parsing gleam package information: %w", err)
	}
	name, version := information.Config.Name, information.Config.Version
	if name == "" || version == "" {
		return fmt.Errorf("gleam.toml requires name and version")
	}
	// gleam publish asks for the core team password for these names, which can't be
	// answered without a terminal.
	if strings.HasPrefix(name, "gleam_") && !strings.HasPrefix(name, "gleam_community_") {
		return fmt.Errorf("gleam package %s uses the gleam_ prefix reserved for the Gleam core team", name)
	}
	if err := requireStrictPackageVersion(version); err != nil {
		return fmt.Errorf("gleam package: %w", err)
	}
	publication.name = name
	publication.version = version
	// Building the tarball checks the package metadata, dependencies, and code.
	return set.commands.run(commandOptions{
		name: "gleam",
		args: []string{"export", "hex-tarball"},
		dir:  publication.dir,
	})
}

func publishGleamPackage(set *packagePublicationSet, publication *packagePublication) error {
	output, err := set.commands.capture(gleamPublishCommand(set, publication))
	if err != nil {
		return err
	}
	if strings.Contains(output, gleamNotPublishing) {
		return fmt.Errorf("gleam publish declined to publish the package")
	}
	return nil
}

// gleamPublishCommand publishes the package and its documentation.
func gleamPublishCommand(set *packagePublicationSet, publication *packagePublication) commandOptions {
	command := commandOptions{
		name:    "gleam",
		args:    []string{"publish", "--yes"},
		dir:     publication.dir,
		env:     []string{"HEXPM_API_KEY=" + set.cfg.hexPMToken},
		secrets: []string{set.cfg.hexPMToken},
	}
	if strings.HasPrefix(publication.version, "0.") {
		command.stdin = gleamVersionZeroConfirmation + "\n"
	}
	return command
}
