package flakerelease

import (
	"archive/tar"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
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
	if err := preflightAndVerify(set, publication); err != nil {
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
	if err := preflightAndVerify(set, publication); err == nil || !strings.Contains(err.Error(), "produced 0 package tarballs") {
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
	if err := preflightAndVerify(set, publication); err != nil {
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

func TestHexSourceWithRebar3AndGleamPublishesWithRebar3(t *testing.T) {
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "rebar.config"), "{deps, []}.\n")
	writeTestFile(t, filepath.Join(source, "gleam.toml"), "name = \"app\"\nversion = \"1.2.3\"\n")
	writeTestFile(t, filepath.Join(source, "src", "app.app.src"), "{application, app, [{vsn, \"1.2.3\"}]}.\n")
	// Gleam only publishes modules from src.
	writeTestFile(t, filepath.Join(source, "test", "app_test.gleam"), "pub fn main() { Nil }\n")
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
	if len(set.packages) != 1 || set.packages[0].kind != packageRebar3 {
		t.Fatalf("prepared packages = %#v; want rebar3 only", set.packages)
	}
	if set.packages[0].manifestName != "app" {
		t.Fatalf("rebar3 manifest name = %q; want app from the .app.src", set.packages[0].manifestName)
	}
}

func TestHexSourceWithRebar3AndGleamModulesPublishesWithGleam(t *testing.T) {
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "rebar.config"), "{deps, []}.\n")
	writeTestFile(t, filepath.Join(source, "gleam.toml"), "name = \"a\"\nversion = \"1.2.3\"\n")
	writeTestFile(t, filepath.Join(source, "src", "a.app.src"), "{application, a, [{vsn, \"1.2.3\"}]}.\n")
	writeTestFile(t, filepath.Join(source, "src", "a_runtime.erl"), "-module(a_runtime).\n")
	writeTestFile(t, filepath.Join(source, "src", "a", "b.gleam"), "pub fn main() { Nil }\n")
	var commands []commandOptions
	set, err := preparePackagePublicationsWith(config{dryRun: true, hexPMToken: "hex-token"}, releaseForgejo, parseReleaseTag("v1.2.3"), []string{"pkg"}, nil, func(string) (nixSource, error) { return nixSource{root: source}, nil }, fakePackageCommandRunner{
		requireFunc: func(string) error { return nil },
		runFunc: func(options commandOptions) error {
			commands = append(commands, options)
			return nil
		},
		captureFunc: func(options commandOptions) (string, error) {
			commands = append(commands, options)
			return `{"gleam.toml":{"name":"a","version":"1.2.3"}}`, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(set.Close)
	if len(set.packages) != 1 || set.packages[0].kind != packageGleam || set.packages[0].manifestName != "a" {
		t.Fatalf("prepared packages = %#v; want gleam package a only", set.packages)
	}
	publication := &set.packages[0]
	if err := publication.verify(set); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if command.name != "gleam" || command.dir != publication.dir {
			t.Fatalf("commands = %v; want gleam only, in the staged source", commands)
		}
	}
	if last := commands[len(commands)-1]; !slices.Equal(last.args, []string{"export", "hex-tarball"}) {
		t.Fatalf("commands = %v; want verification with gleam export hex-tarball", commands)
	}
}

func TestHasGleamModules(t *testing.T) {
	for _, test := range []struct {
		files []string
		want  bool
	}{
		{[]string{"src/a.gleam"}, true},
		{[]string{"src/a.erl", "src/a/b/c.gleam"}, true},
		{[]string{"src/a.erl", "test/a_test.gleam", "a.gleam"}, false},
		{nil, false},
	} {
		dir := t.TempDir()
		for _, file := range test.files {
			writeTestFile(t, filepath.Join(dir, file), "")
		}
		if got := hasGleamModules(dir); got != test.want {
			t.Errorf("hasGleamModules(%q) = %v; want %v", test.files, got, test.want)
		}
	}
}

func TestRebar3PackageNameFromSource(t *testing.T) {
	for _, test := range []struct {
		appSrc string
		want   string
	}{
		{"{application, trevrpc, [\n    {vsn, \"1.12.0\"}\n]}.\n", "trevrpc"},
		{"%% Application\n{application, 'app', [{vsn, \"1.0.0\"}, {pkg_name, app_pkg}]}.\n", "app_pkg"},
		{"{application, app, [{pkg_name, \"app_pkg\"}]}.\n", "app_pkg"},
		{"{application, app, [{pkg_name, <<\"app_pkg\">>}]}.\n", "app_pkg"},
	} {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "src", "app.app.src"), test.appSrc)
		if got := rebar3PackageNameFromSource(dir); got != test.want {
			t.Errorf("rebar3PackageNameFromSource(%q) = %q; want %q", test.appSrc, got, test.want)
		}
	}
	if got := rebar3PackageNameFromSource(t.TempDir()); got != "" {
		t.Errorf("name without .app.src = %q; want none", got)
	}
}

func TestGleamPreflightReadsDependencies(t *testing.T) {
	var commands []commandOptions
	set := newGleamTestSet(t, `{"gleam.toml":{"name":"b","version":"1.0.0","dependencies":{
		"gleam_stdlib":{"version":">= 0.44.0 and < 2.0.0"},
		"a":{"path":"../a"},
		"c":{"git":"https://example.com/c.git","ref":"main","path":"c"}
	},"dev_dependencies":{"gleeunit":{"version":">= 1.0.0 and < 2.0.0"}}}}`, "", &commands)
	publication := &packagePublication{kind: packageGleam, registry: registryHexPM, dir: t.TempDir()}
	if err := publication.preflight(set); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(publication.dependencies, []string{"a", "c", "gleam_stdlib"}) {
		t.Fatalf("dependencies = %q; want a, c and gleam_stdlib", publication.dependencies)
	}
	if len(publication.pathDependencies) != 1 || publication.pathDependencies["a"] != "../a" {
		t.Fatalf("path dependencies = %v; want a from ../a", publication.pathDependencies)
	}
}

func TestGleamHexRequirement(t *testing.T) {
	for _, test := range []struct{ version, want string }{
		{"1.12.0", ">= 1.12.0 and < 2.0.0"},
		{"0.3.1", ">= 0.3.1 and < 1.0.0"},
		{"2.0.0-rc.1+build.5", ">= 2.0.0-rc.1 and < 3.0.0"},
	} {
		got, err := gleamHexRequirement(test.version)
		if err != nil || got != test.want {
			t.Errorf("gleamHexRequirement(%q) = %q, %v; want %q", test.version, got, err, test.want)
		}
	}
	if _, err := gleamHexRequirement("1.0"); err == nil {
		t.Error("gleamHexRequirement accepted a non-semantic version")
	}
}

// testGleamPathDependencyRelease returns a release of Gleam package b, staged in a
// temporary directory, that depends on a from ../a.
func testGleamPathDependencyRelease(t *testing.T) (preparedRelease, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "b")
	writeTestFile(t, filepath.Join(dir, "gleam.toml"), `name = "b"
version = "1.0.0"

[dependencies]
gleam_stdlib = ">= 0.44.0 and < 2.0.0"
a = { path = "../a" }

[dev-dependencies]
gleeunit = ">= 1.0.0 and < 2.0.0"
`)
	writeTestFile(t, filepath.Join(dir, "manifest.toml"), `packages = [
  { name = "a", version = "1.0.0", build_tools = ["rebar3"], requirements = [], source = "local", path = "../a" },
  { name = "gleam_stdlib", version = "1.0.5", build_tools = ["gleam"], requirements = [], otp_app = "gleam_stdlib", source = "hex", outer_checksum = "CEE5" },
]

[requirements]
a = { path = "../a" }
gleam_stdlib = { version = ">= 0.44.0 and < 2.0.0" }
`)
	return preparedRelease{
		releaseGroup: releaseGroup{tag: parseReleaseTag("b/v1.0.0")},
		publications: &packagePublicationSet{packages: []packagePublication{{
			kind:             packageGleam,
			registry:         registryHexPM,
			source:           "/src/b",
			dir:              dir,
			manifest:         filepath.Join(dir, "gleam.toml"),
			name:             "b",
			version:          "1.0.0",
			manifestName:     "b",
			dependencies:     []string{"a", "gleam_stdlib"},
			pathDependencies: map[string]string{"a": "../a"},
		}}},
	}, dir
}

// noReleasedTags returns released tags that fail the test when they are consulted.
func noReleasedTags(t *testing.T) func() ([]releaseTag, error) {
	t.Helper()
	return func() ([]releaseTag, error) {
		t.Fatal("released tags consulted")
		return nil, nil
	}
}

// releasedTags returns released tags parsed from names.
func releasedTags(names ...string) func() ([]releaseTag, error) {
	return func() ([]releaseTag, error) { return parseReleaseTags(names...), nil }
}

// testGleamPathDependencyReleaseFromSource returns the release of b from
// testGleamPathDependencyRelease, with its source at b in a temporary repository whose
// files are given by path, and the staged gleam.toml.
func testGleamPathDependencyReleaseFromSource(t *testing.T, files map[string]string) (preparedRelease, string) {
	t.Helper()
	gleam, dir := testGleamPathDependencyRelease(t)
	root := t.TempDir()
	for path, contents := range files {
		writeTestFile(t, filepath.Join(root, filepath.FromSlash(path)), contents)
	}
	gleam.publications.packages[0].root = root
	gleam.publications.packages[0].source = filepath.Join(root, "b")
	return gleam, filepath.Join(dir, "gleam.toml")
}

// gleamDependency returns the requirement on name in the gleam.toml at path.
func gleamDependency(t *testing.T, path string, name string) any {
	t.Helper()
	var config struct {
		Dependencies map[string]any `toml:"dependencies"`
	}
	if _, err := toml.DecodeFile(path, &config); err != nil {
		t.Fatal(err)
	}
	return config.Dependencies[name]
}

func TestGleamPathDependencyOnReleasedVersionIsPublishedFromHex(t *testing.T) {
	for _, test := range []struct {
		name  string
		files map[string]string
		path  string
		tags  []string
	}{
		{
			name:  "gleam package with its own tag",
			files: map[string]string{"a/gleam.toml": "name = \"a\"\nversion = \"1.0.3\"\n"},
			tags:  []string{"v2.0.0", "b/v1.0.0", "a/v1.0.2", "a/v1.0.3"},
		},
		{
			name:  "rebar3 package with its own tag",
			files: map[string]string{"a/src/a.app.src": "{application, a, [{vsn, \"1.0.3\"}, {applications, [kernel, stdlib]}]}.\n"},
			tags:  []string{"a/v1.0.3"},
		},
		{
			name:  "package in the nearest tagged directory",
			files: map[string]string{"lib/a/gleam.toml": "name = \"a\"\nversion = \"1.0.3\"\n"},
			path:  "../lib/a",
			tags:  []string{"lib/v1.0.3", "v1.0.3"},
		},
		{
			name:  "package at the repository root",
			files: map[string]string{"gleam.toml": "name = \"a\"\nversion = \"1.0.3\"\n"},
			path:  "..",
			tags:  []string{"other/v1.0.3", "v1.0.3"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			gleam, manifest := testGleamPathDependencyReleaseFromSource(t, test.files)
			if test.path != "" {
				gleam.publications.packages[0].pathDependencies["a"] = test.path
			}
			if err := resolvePathDependencies([]preparedRelease{gleam}, releasedTags(test.tags...)); err != nil {
				t.Fatal(err)
			}
			if got := gleamDependency(t, manifest, "a"); got != ">= 1.0.3 and < 2.0.0" {
				t.Fatalf("dependency a = %v; want it from hex.pm at 1.0.3", got)
			}
		})
	}
}

func TestGleamPathDependencyOnUnreleasedVersionIsRejected(t *testing.T) {
	for _, test := range []struct {
		name  string
		files map[string]string
		path  string
		tags  []string
		err   string
	}{
		{
			name:  "version bumped without a tag",
			files: map[string]string{"a/gleam.toml": "name = \"a\"\nversion = \"1.0.4\"\n"},
			tags:  []string{"a/v1.0.3", "v1.0.4"},
			err:   `dependency a from path "../a" is at 1.0.4, which has no release tag; release it in this run or tag it`,
		},
		{
			name:  "version tagged for another directory",
			files: map[string]string{"a/gleam.toml": "name = \"a\"\nversion = \"1.0.3\"\n"},
			tags:  []string{"other/v1.0.3", "ab/v1.0.3"},
			err:   `dependency a from path "../a" is at 1.0.3, which has no release tag`,
		},
		{
			name: "no manifest",
			tags: []string{"a/v1.0.3"},
			err:  `dependency a from path "../a" is not published by another release in this run, and its released version can't be found: found no gleam.toml and 0 .app.src files`,
		},
		{
			name:  "version that isn't literal",
			files: map[string]string{"a/src/a.app.src": "{application, a, [{vsn, git}]}.\n"},
			tags:  []string{"a/v1.0.3"},
			err:   "a.app.src has no literal vsn",
		},
		{
			name: "outside the src",
			path: "../../a",
			tags: []string{"a/v1.0.3"},
			err:  "it is outside the package's src",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			gleam, _ := testGleamPathDependencyReleaseFromSource(t, test.files)
			if test.path != "" {
				gleam.publications.packages[0].pathDependencies["a"] = test.path
			}
			err := resolvePathDependencies([]preparedRelease{gleam}, releasedTags(test.tags...))
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("error = %v; want %q", err, test.err)
			}
		})
	}
}

func TestGleamPathDependencyReleasedInTheRunWinsOverReleasedVersion(t *testing.T) {
	gleam, manifest := testGleamPathDependencyReleaseFromSource(t, map[string]string{
		"a/gleam.toml": "name = \"a\"\nversion = \"1.2.0\"\n",
	})
	prepared := []preparedRelease{gleam, testRebar3Release("a/v1.2.0", "a")}
	if err := resolvePathDependencies(prepared, noReleasedTags(t)); err != nil {
		t.Fatal(err)
	}
	if got := gleamDependency(t, manifest, "a"); got != ">= 1.2.0 and < 2.0.0" {
		t.Fatalf("dependency a = %v; want it from hex.pm at 1.2.0", got)
	}
	var published []string
	err := publishReleases(config{}, prepared, func(ready preparedRelease, _ func(packagePublication) []string) error {
		published = append(published, ready.tag.full)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(published, ","), "a/v1.2.0,b/v1.0.0"; got != want {
		t.Fatalf("published = %s; want %s", got, want)
	}
}

// testRebar3Release returns a release of rebar3 package name, whose name and version
// are only known from its .app.src and tag until verification.
func testRebar3Release(tag string, name string) preparedRelease {
	return preparedRelease{
		releaseGroup: releaseGroup{tag: parseReleaseTag(tag)},
		publications: &packagePublicationSet{packages: []packagePublication{{
			kind:         packageRebar3,
			registry:     registryHexPM,
			manifestName: name,
		}}},
	}
}

func TestGleamPathDependencyOnRebar3ReleaseIsPublishedFromHex(t *testing.T) {
	gleam, dir := testGleamPathDependencyRelease(t)
	prepared := []preparedRelease{gleam, testRebar3Release("a/v1.0.0", "a")}
	if err := resolvePathDependencies(prepared, noReleasedTags(t)); err != nil {
		t.Fatal(err)
	}
	var gleamConfig struct {
		Dependencies    map[string]any `toml:"dependencies"`
		DevDependencies map[string]any `toml:"dev-dependencies"`
		Name            string         `toml:"name"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "gleam.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := toml.Decode(string(data), &gleamConfig); err != nil {
		t.Fatal(err)
	}
	if gleamConfig.Name != "b" || gleamConfig.Dependencies["a"] != ">= 1.0.0 and < 2.0.0" || gleamConfig.Dependencies["gleam_stdlib"] != ">= 0.44.0 and < 2.0.0" || gleamConfig.DevDependencies["gleeunit"] != ">= 1.0.0 and < 2.0.0" {
		t.Fatalf("rewritten gleam.toml = %s; want a from hex.pm and everything else unchanged", data)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), `name = "a"`) || !strings.Contains(string(manifest), `name = "gleam_stdlib"`) {
		t.Fatalf("rewritten manifest.toml = %s; want only the local a package removed", manifest)
	}

	// b is published after a, and skipped when a fails.
	var published []string
	err = publishReleases(config{}, prepared, func(ready preparedRelease, _ func(packagePublication) []string) error {
		published = append(published, ready.tag.full)
		if ready.tag.full == "a/v1.0.0" {
			return errors.New("hex.pm unavailable")
		}
		return nil
	})
	if got, want := strings.Join(published, ","), "a/v1.0.0"; got != want {
		t.Fatalf("published = %s; want %s", got, want)
	}
	if err == nil || !strings.Contains(err.Error(), "b/v1.0.0: skipped because a/v1.0.0 failed") {
		t.Fatalf("publishReleases() error = %v; want b skipped", err)
	}

	// A dry run can't verify b, which needs a from hex.pm.
	pending := map[string][]string{}
	err = publishReleases(config{dryRun: true}, prepared, func(ready preparedRelease, unpublished func(packagePublication) []string) error {
		pending[ready.tag.full] = unpublished(ready.publicationList()[0])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pending["b/v1.0.0"], []string{"a"}) {
		t.Fatalf("unpublished dependencies = %v; want a for b", pending)
	}
}

func TestGleamPathDependencyOnGleamReleaseIsPublishedFromHex(t *testing.T) {
	gleam, dir := testGleamPathDependencyRelease(t)
	// a has a rebar.config too, but its Gleam modules make it a Gleam package.
	provider := preparedRelease{
		releaseGroup: releaseGroup{tag: parseReleaseTag("a/v1.2.0")},
		publications: &packagePublicationSet{packages: []packagePublication{{
			kind:         packageGleam,
			registry:     registryHexPM,
			name:         "a",
			version:      "1.2.0",
			manifestName: "a",
		}}},
	}
	prepared := []preparedRelease{gleam, provider}
	if err := resolvePathDependencies(prepared, noReleasedTags(t)); err != nil {
		t.Fatal(err)
	}
	var gleamConfig struct {
		Dependencies map[string]any `toml:"dependencies"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "gleam.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := toml.Decode(string(data), &gleamConfig); err != nil {
		t.Fatal(err)
	}
	if gleamConfig.Dependencies["a"] != ">= 1.2.0 and < 2.0.0" {
		t.Fatalf("rewritten gleam.toml = %s; want a from hex.pm", data)
	}

	var published []string
	err = publishReleases(config{}, prepared, func(ready preparedRelease, _ func(packagePublication) []string) error {
		published = append(published, ready.tag.full)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(published, ","), "a/v1.2.0,b/v1.0.0"; got != want {
		t.Fatalf("published = %s; want %s", got, want)
	}
}

func TestGleamPathDependencyMustBeReleasedByAnotherRelease(t *testing.T) {
	gleam, _ := testGleamPathDependencyRelease(t)
	err := resolvePathDependencies([]preparedRelease{gleam, testRebar3Release("c/v1.0.0", "c")}, noReleasedTags(t))
	if err == nil || !strings.Contains(err.Error(), `dependency a from path "../a" is not published by another release in this run`) {
		t.Fatalf("error = %v; want unreleased path dependency rejected", err)
	}

	gleam, _ = testGleamPathDependencyRelease(t)
	gleam.publications.packages = append(gleam.publications.packages, testRebar3Release("b/v1.0.0", "a").publicationList()...)
	err = resolvePathDependencies([]preparedRelease{gleam}, noReleasedTags(t))
	if err == nil || !strings.Contains(err.Error(), "published by the same release") {
		t.Fatalf("error = %v; want path dependency in the same release rejected", err)
	}
}
