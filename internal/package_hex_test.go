package flakerelease

import (
	"archive/tar"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestHexPMTokenEnablesGleamAndRebar3Targets(t *testing.T) {
	targets, _, err := selectPackageTargets(config{hexPMToken: "hex-token"}, releaseForgejo)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].registry != registryHexPM || !slices.Equal(targets[0].kinds, []packageKind{packageGleam, packageRebar3}) {
		t.Fatalf("targets = %#v; want Gleam and rebar3 packages for hex.pm", targets)
	}
	if name := packageRegistryName(registryHexPM, releaseForgejo); name != "hex.pm" {
		t.Fatalf("registry name = %q", name)
	}
	if err := validatePublicRegistryConfig(config{}, registryHexPM); err != nil {
		t.Fatal(err)
	}
}

func TestHexPackagesDiscoveredFromManifests(t *testing.T) {
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "gleam.toml"), "name = \"app\"\nversion = \"1.2.3\"\n")
	set, err := preparePackagePublicationsWith(config{dryRun: true, hexPMToken: "hex-token"}, releaseForgejo, parseReleaseTag("v1.2.3"), []string{"pkg"}, nil, func(string) (nixSource, error) { return nixSource{root: source}, nil }, fakePackageCommandRunner{
		requireFunc: func(string) error { return nil },
		runFunc:     func(commandOptions) error { return nil },
		captureFunc: func(commandOptions) (string, error) {
			return `{"gleam.toml":{"name":"app","version":"1.2.3"}}`, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(set.Close)
	if len(set.packages) != 1 || set.packages[0].kind != packageGleam || set.packages[0].registry != registryHexPM {
		t.Fatalf("prepared packages = %#v; want gleam for hex.pm only", set.packages)
	}
}

func TestPackageIdentitySharesHexForGleamAndRebar3(t *testing.T) {
	gleam := packagePublication{kind: packageGleam, registry: registryHexPM, name: "app", version: "1.2.3"}
	rebar3 := packagePublication{kind: packageRebar3, registry: registryHexPM, name: "app", version: "1.2.3"}
	if publicationIdentity(gleam) != publicationIdentity(rebar3) {
		t.Fatal("gleam and rebar3 packages with the same name have different hex.pm identities")
	}
}

func writeTestHexTarball(t *testing.T, path string, metadata string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	archive := tar.NewWriter(file)
	for _, entry := range []struct{ name, contents string }{{"VERSION", "3"}, {"metadata.config", metadata}} {
		if err := archive.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.contents))}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(entry.contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReadHexTarballMetadata(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct {
		format   string
		metadata string
	}{
		{"rebar3", "{<<\"app\">>,<<\"app\">>}.\n{<<\"name\">>,<<\"app\">>}.\n{<<\"requirements\">>,[]}.\n{<<\"version\">>,<<\"1.2.3\">>}.\n"},
		// Requirements nest name fields, which must not be taken for the package's.
		{"gleam", "{<<\"requirements\">>, [\n  [{<<\"name\">>, <<\"dep\"/utf8>>}]\n]}.\n{<<\"name\">>, <<\"app\"/utf8>>}.\n{<<\"version\">>, <<\"1.2.3\"/utf8>>}.\n"},
	} {
		path := filepath.Join(dir, test.format+".tar")
		writeTestHexTarball(t, path, test.metadata)
		name, version, err := readHexTarballMetadata(path)
		if err != nil {
			t.Fatalf("%s: %v", test.format, err)
		}
		if name != "app" || version != "1.2.3" {
			t.Fatalf("%s: metadata = %s@%s; want app@1.2.3", test.format, name, version)
		}
	}
	path := filepath.Join(dir, "missing.tar")
	writeTestHexTarball(t, path, "{<<\"name\">>,<<\"app\">>}.\n")
	if _, _, err := readHexTarballMetadata(path); err == nil {
		t.Fatal("metadata without a version was accepted")
	}
}

func TestRebar3PackagePublishesToHexPM(t *testing.T) {
	project := t.TempDir()
	var commands []commandOptions
	set := &packagePublicationSet{
		cfg:          config{hexPMToken: "hex-token"},
		temporaryDir: t.TempDir(),
		commands: fakePackageCommandRunner{
			requireFunc: func(string) error { return nil },
			runFunc: func(options commandOptions) error {
				commands = append(commands, options)
				if options.args[1] == "build" {
					output := options.args[slices.Index(options.args, "--output")+1]
					writeTestHexTarball(t, filepath.Join(output, "tmp", "app-1.2.3.tar"), "{<<\"name\">>,<<\"app\">>}.\n{<<\"version\">>,<<\"1.2.3\">>}.\n")
				}
				return nil
			},
		},
	}
	publication := &packagePublication{kind: packageRebar3, registry: registryHexPM, dir: project, manifest: filepath.Join(project, "rebar.config")}
	if err := publication.preflight(set); err != nil {
		t.Fatal(err)
	}
	if publication.name != "app" || publication.version != "1.2.3" {
		t.Fatalf("publication = %s@%s; want app@1.2.3", publication.name, publication.version)
	}
	if err := publication.publish(set); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 3 {
		t.Fatalf("commands = %v; want build, dry run, and publish", commands)
	}
	configDir := filepath.Join(set.temporaryDir, "rebar3")
	config, err := os.ReadFile(filepath.Join(configDir, ".config", "rebar3", "rebar.config"))
	if err != nil || !strings.Contains(string(config), "rebar3_hex") {
		t.Fatalf("global rebar3 config = %q, %v; want rebar3_hex plugin", config, err)
	}
	dryRun := []string{"hex", "publish", "--repo", "hexpm", "--yes", "--dry-run"}
	if !slices.Equal(commands[1].args, dryRun) {
		t.Fatalf("dry run args = %q; want %q", commands[1].args, dryRun)
	}
	publish := []string{"hex", "publish", "--repo", "hexpm", "--yes"}
	if !slices.Equal(commands[2].args, publish) {
		t.Fatalf("publish args = %q; want %q", commands[2].args, publish)
	}
	if !slices.Equal(commands[2].env, []string{"REBAR_GLOBAL_CONFIG_DIR=" + configDir, "HEX_API_KEY=hex-token"}) {
		t.Fatalf("publish env = %q; want global config and hex.pm key", commands[2].env)
	}
	if !slices.Equal(commands[2].secrets, []string{"hex-token"}) {
		t.Fatalf("publish secrets = %q", commands[2].secrets)
	}
}

func TestRebar3PackageRequiresOneTarball(t *testing.T) {
	project := t.TempDir()
	set := &packagePublicationSet{
		temporaryDir: t.TempDir(),
		commands: fakePackageCommandRunner{
			requireFunc: func(string) error { return nil },
			runFunc:     func(commandOptions) error { return nil },
		},
	}
	publication := &packagePublication{kind: packageRebar3, registry: registryHexPM, dir: project}
	if err := publication.preflight(set); err == nil || !strings.Contains(err.Error(), "produced 0 package tarballs") {
		t.Fatalf("error = %v; want missing tarball", err)
	}
}

func newGleamTestSet(t *testing.T, information string, publishOutput string, commands *[]commandOptions) *packagePublicationSet {
	t.Helper()
	return &packagePublicationSet{
		cfg:          config{hexPMToken: "hex-token"},
		temporaryDir: t.TempDir(),
		commands: fakePackageCommandRunner{
			requireFunc: func(string) error { return nil },
			runFunc: func(options commandOptions) error {
				*commands = append(*commands, options)
				return nil
			},
			captureFunc: func(options commandOptions) (string, error) {
				*commands = append(*commands, options)
				if options.args[0] == "publish" {
					return publishOutput, nil
				}
				return information, nil
			},
		},
	}
}

func TestGleamPackagePublishesToHexPM(t *testing.T) {
	project := t.TempDir()
	var commands []commandOptions
	set := newGleamTestSet(t, `{"gleam.toml":{"name":"app","version":"1.2.3"}}`, "Published package and documentation", &commands)
	publication := &packagePublication{kind: packageGleam, registry: registryHexPM, dir: project, manifest: filepath.Join(project, "gleam.toml")}
	if err := publication.preflight(set); err != nil {
		t.Fatal(err)
	}
	if publication.name != "app" || publication.version != "1.2.3" {
		t.Fatalf("publication = %s@%s; want app@1.2.3", publication.name, publication.version)
	}
	if err := publication.publish(set); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 3 || !slices.Equal(commands[1].args, []string{"export", "hex-tarball"}) {
		t.Fatalf("commands = %v; want package information, hex tarball, and publish", commands)
	}
	publish := commands[2]
	if !slices.Equal(publish.args, []string{"publish", "--yes"}) {
		t.Fatalf("publish args = %q", publish.args)
	}
	if !slices.Equal(publish.env, []string{"HEXPM_API_KEY=hex-token"}) || !slices.Equal(publish.secrets, []string{"hex-token"}) {
		t.Fatalf("publish env = %q secrets = %q; want hex.pm key", publish.env, publish.secrets)
	}
	if publish.stdin != "" {
		t.Fatalf("publish stdin = %q; want none for a stable version", publish.stdin)
	}
}

func TestGleamPackageConfirmsVersionZero(t *testing.T) {
	var commands []commandOptions
	set := newGleamTestSet(t, "", "", &commands)
	publication := &packagePublication{kind: packageGleam, registry: registryHexPM, name: "app", version: "0.1.0"}
	if err := publication.publish(set); err != nil {
		t.Fatal(err)
	}
	if commands[0].stdin != gleamVersionZeroConfirmation+"\n" {
		t.Fatalf("publish stdin = %q; want version zero confirmation", commands[0].stdin)
	}
}

func TestGleamPackageFailsWhenNotPublished(t *testing.T) {
	var commands []commandOptions
	set := newGleamTestSet(t, "", "Name: app\nVersion: 1.2.3\nNot publishing.", &commands)
	publication := &packagePublication{kind: packageGleam, registry: registryHexPM, name: "app", version: "1.2.3"}
	if err := publication.publish(set); err == nil {
		t.Fatal("declined gleam publish succeeded")
	}
}

func TestGleamPackageRejectsCoreTeamPrefix(t *testing.T) {
	for _, test := range []struct {
		name    string
		allowed bool
	}{
		{"gleam_json", false},
		{"gleam_community_colour", true},
		{"app", true},
	} {
		var commands []commandOptions
		set := newGleamTestSet(t, `{"gleam.toml":{"name":"`+test.name+`","version":"1.2.3"}}`, "", &commands)
		publication := &packagePublication{kind: packageGleam, registry: registryHexPM, dir: t.TempDir()}
		err := publication.preflight(set)
		if test.allowed && err != nil {
			t.Errorf("%s: %v; want allowed", test.name, err)
		}
		if !test.allowed && (err == nil || !strings.Contains(err.Error(), "gleam_ prefix")) {
			t.Errorf("%s: error = %v; want reserved prefix rejected", test.name, err)
		}
	}
}
