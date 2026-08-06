package flakerelease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMavenParseSimple(t *testing.T) {
	dir := t.TempDir()
	pom := filepath.Join(dir, "pom.xml")
	writeTestFile(t, pom, `<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>my-lib</artifactId>
  <version>1.2.3</version>
</project>`)
	group, artifact, version, err := parseMavenPom(pom)
	if err != nil {
		t.Fatal(err)
	}
	if group != "com.example" || artifact != "my-lib" || version != "1.2.3" {
		t.Fatalf("got %s %s %s", group, artifact, version)
	}
	if err := requireStrictPackageVersion(version); err != nil {
		t.Fatal(err)
	}
}

func TestMavenParseWithParent(t *testing.T) {
	dir := t.TempDir()
	pom := filepath.Join(dir, "pom.xml")
	writeTestFile(t, pom, `<project>
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example.parent</groupId>
    <artifactId>parent</artifactId>
    <version>1.2.3</version>
  </parent>
  <artifactId>my-lib</artifactId>
</project>`)
	group, artifact, version, err := parseMavenPom(pom)
	if err != nil {
		t.Fatal(err)
	}
	if group != "com.example.parent" || artifact != "my-lib" || version != "1.2.3" {
		t.Fatalf("got %s %s %s", group, artifact, version)
	}
}

func TestMavenParseWithRevision(t *testing.T) {
	dir := t.TempDir()
	pom := filepath.Join(dir, "pom.xml")
	writeTestFile(t, pom, `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>my-lib</artifactId>
  <version>${revision}</version>
  <properties>
    <revision>1.2.3</revision>
  </properties>
</project>`)
	group, artifact, version, err := parseMavenPom(pom)
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.2.3" {
		t.Fatalf("expected resolved revision 1.2.3, got %s", version)
	}
	if group != "com.example" || artifact != "my-lib" {
		t.Fatalf("group/artifact mismatch")
	}
}

func TestMavenSettingsGeneration(t *testing.T) {
	dir := t.TempDir()
	set := &packagePublicationSet{
		cfg: config{
			packageRegistryOwner:    "owner",
			packageRegistryUsername: "actor",
			packageRegistryToken:    "secret",
		},
		temporaryDir: dir,
	}
	path, err := mavenSettingsPath(set)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "flake-release") || !strings.Contains(content, "secret") {
		t.Fatalf("settings missing expected content: %s", content)
	}
}

func TestGradleParseSimple(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "build.gradle"), "group = 'com.example'\nversion = '1.2.3'\n")
	writeTestFile(t, filepath.Join(dir, "settings.gradle"), "rootProject.name = 'my-lib'\n")
	group, name, version, err := parseGradleCoordinates(dir, filepath.Join(dir, "build.gradle"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if group != "com.example" || name != "my-lib" || version != "1.2.3" {
		t.Fatalf("got %s %s %s", group, name, version)
	}
}

func TestGradleParseFromProperties(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "build.gradle"), "// no version here\n")
	writeTestFile(t, filepath.Join(dir, "gradle.properties"), "group=com.example\nversion=1.2.3\n")
	writeTestFile(t, filepath.Join(dir, "settings.gradle"), "rootProject.name = 'my-lib'\n")
	group, name, version, err := parseGradleCoordinates(dir, filepath.Join(dir, "build.gradle"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if group != "com.example" || version != "1.2.3" || name != "my-lib" {
		t.Fatalf("got %s %s %s", group, name, version)
	}
}

func TestGradleManifestDetection(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "build.gradle.kts"), `group = "com.example"
version = "1.2.3"
`)
	writeTestFile(t, filepath.Join(dir, "settings.gradle.kts"), `rootProject.name = "my-lib"`)
	// Check candidate detection
	candidates := packageManifestCandidates(packageGradle)
	found := false
	for _, c := range candidates {
		if isFile(filepath.Join(dir, c)) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("gradle manifest not found for .kts")
	}
}

func TestPrepareMavenPublications(t *testing.T) {
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "pom.xml"), `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>my-lib</artifactId>
  <version>1.2.3</version>
</project>`)
	cfg := config{
		dryRun:               true,
		publishPackages:      "maven",
		packageRegistryOwner: "owner",
		packageRegistryURL:   "https://git.example",
	}
	set, err := preparePackagePublicationsWith(cfg, releaseForgejo, "v1.2.3", []string{"pkg"}, func(string) (string, error) { return source, nil }, fakePackageCommandRunner{
		requireFunc: func(name string) error { return nil },
		runFunc: func(options commandOptions) error { return nil },
		captureFunc: func(options commandOptions) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if len(set.packages) != 1 || set.packages[0].kind != packageMaven {
		t.Fatalf("prepared packages %#v", set.packages)
	}
	if set.packages[0].name != "com.example:my-lib" || set.packages[0].version != "1.2.3" {
		t.Fatalf("maven identity %s@%s", set.packages[0].name, set.packages[0].version)
	}
}

func TestPrepareGradlePublications(t *testing.T) {
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "build.gradle"), "group = 'com.example'\nversion = '1.2.3'\n")
	writeTestFile(t, filepath.Join(source, "settings.gradle"), "rootProject.name = 'my-lib'\n")
	cfg := config{
		dryRun:               true,
		publishPackages:      "gradle",
		packageRegistryOwner: "owner",
		packageRegistryURL:   "https://git.example",
	}
	set, err := preparePackagePublicationsWith(cfg, releaseForgejo, "v1.2.3", []string{"pkg"}, func(string) (string, error) { return source, nil }, fakePackageCommandRunner{
		requireFunc: func(name string) error { return nil },
		runFunc: func(options commandOptions) error { return nil },
		captureFunc: func(options commandOptions) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if len(set.packages) != 1 || set.packages[0].kind != packageGradle {
		t.Fatalf("prepared packages %#v", set.packages)
	}
	if set.packages[0].name != "com.example:my-lib" || set.packages[0].version != "1.2.3" {
		t.Fatalf("gradle identity %s@%s", set.packages[0].name, set.packages[0].version)
	}
}

func TestRegistryURLMavenForgejo(t *testing.T) {
	set := &packagePublicationSet{
		cfg: config{
			packageRegistryOwner: "owner",
			packageRegistryURL:   "https://git.example",
		},
		provider: releaseForgejo,
	}
	url := set.registryURL(packageMaven)
	want := "https://git.example/api/packages/owner/maven/"
	if url != want {
		t.Fatalf("registryURL maven forgejo = %q want %q", url, want)
	}
	urlGradle := set.registryURL(packageGradle)
	if urlGradle != want {
		t.Fatalf("registryURL gradle forgejo = %q want %q", urlGradle, want)
	}
}

func TestRegistryURLMavenGitHub(t *testing.T) {
	set := &packagePublicationSet{
		cfg: config{
			packageRegistryOwner: "Owner",
			githubRepository:     "Owner/repo",
			packageRegistryURL:   "https://npm.pkg.github.com",
		},
		provider: releaseGitHub,
	}
	// For maven, should compute maven.pkg.github.com/Owner/repo
	url := set.registryURL(packageMaven)
	want := "https://maven.pkg.github.com/Owner/repo/"
	if url != want {
		t.Fatalf("registryURL maven github = %q want %q", url, want)
	}
	// npm should still be npm.pkg
	urlNPM := set.registryURL(packageNPM)
	wantNPM := "https://npm.pkg.github.com/"
	if urlNPM != wantNPM {
		t.Fatalf("registryURL npm github = %q want %q", urlNPM, wantNPM)
	}
	// With explicit override
	t.Setenv("PACKAGE_REGISTRY_URL", "https://custom.example/maven")
	set.cfg.packageRegistryURL = "https://custom.example/maven"
	urlCustom := set.registryURL(packageMaven)
	if urlCustom != "https://custom.example/maven/" {
		t.Fatalf("custom url = %q", urlCustom)
	}
}

func TestPublishMavenCommand(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(source, "pom.xml"), `<project><modelVersion>4.0.0</modelVersion><groupId>com.example</groupId><artifactId>my-lib</artifactId><version>1.2.3</version></project>`)
	cfg := config{
		packageRegistryOwner: "owner",
		packageRegistryURL:   "https://git.example",
		packageRegistryToken: "secret",
		githubRepository:     "owner/repo",
	}
	set := &packagePublicationSet{
		cfg:          cfg,
		provider:     releaseForgejo,
		tag:          "v1.2.3",
		version:      "1.2.3",
		temporaryDir: t.TempDir(),
		commands: fakePackageCommandRunner{
			requireFunc: func(string) error { return nil },
			runFunc: func(options commandOptions) error { return nil },
			captureFunc: func(options commandOptions) (string, error) { return "", nil },
		},
	}
	publication := &packagePublication{kind: packageMaven, dir: source, manifest: filepath.Join(source, "pom.xml")}
	// Preflight to set name/version
	if err := preflightMavenPackage(set, publication); err != nil {
		t.Fatal(err)
	}
	var captured commandOptions
	set.commands = fakePackageCommandRunner{
		requireFunc: func(string) error { return nil },
		runFunc: func(opts commandOptions) error {
			captured = opts
			return nil
		},
		captureFunc: func(commandOptions) (string, error) { return "", nil },
	}
	if err := publishMavenPackage(set, publication); err != nil {
		t.Fatal(err)
	}
	if captured.name != "mvn" {
		t.Fatalf("maven executable = %q want mvn", captured.name)
	}
	if !containsStr(captured.args, "deploy") || !containsStr(captured.args, "-s") {
		t.Fatalf("maven args missing deploy or -s: %q", captured.args)
	}
	found := false
	for _, arg := range captured.args {
		if arg == "secret" || containsStr([]string{arg}, "secret") {
			found = true
		}
		if stringContains(arg, "secret") {
			t.Fatalf("command args leaked secret: %q", arg)
		}
	}
	_ = found
	if !stringContains(captured.args[0], "-s") && !stringContains(strings.Join(captured.args, " "), "flake-release") {
		t.Fatalf("maven args = %q", captured.args)
	}
}

func TestPublishMavenWrapper(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(source, "pom.xml"), `<project><modelVersion>4.0.0</modelVersion><groupId>com.example</groupId><artifactId>my-lib</artifactId><version>1.2.3</version></project>`)
	writeTestFile(t, filepath.Join(source, "mvnw"), "#!/bin/sh\necho mvnw\n")
	if err := os.Chmod(filepath.Join(source, "mvnw"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config{
		packageRegistryOwner: "owner",
		packageRegistryURL:   "https://git.example",
		packageRegistryToken: "secret",
	}
	set := &packagePublicationSet{
		cfg:          cfg,
		provider:     releaseForgejo,
		temporaryDir: t.TempDir(),
	}
	publication := &packagePublication{kind: packageMaven, dir: source, manifest: filepath.Join(source, "pom.xml")}
	exe, err := mavenExecutable(source)
	if err != nil {
		t.Fatal(err)
	}
	if exe != filepath.Join(source, "mvnw") {
		t.Fatalf("expected wrapper, got %q", exe)
	}
	var captured commandOptions
	set.commands = fakePackageCommandRunner{
		requireFunc: func(string) error { t.Fatal("should not require mvn when wrapper present"); return nil },
		runFunc: func(opts commandOptions) error { captured = opts; return nil },
		captureFunc: func(commandOptions) (string, error) { return "", nil },
	}
	if err := requireMaven(set, publication); err != nil {
		t.Fatal(err)
	}
	// publish should use wrapper
	if err := publishMavenPackage(set, publication); err != nil {
		t.Fatal(err)
	}
	if captured.name != filepath.Join(source, "mvnw") {
		t.Fatalf("expected wrapper in publish, got %q", captured.name)
	}
}

func TestPublishGradleCommand(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(source, "build.gradle"), "group = 'com.example'\nversion = '1.2.3'\n")
	writeTestFile(t, filepath.Join(source, "settings.gradle"), "rootProject.name = 'my-lib'\n")
	cfg := config{
		packageRegistryOwner: "owner",
		packageRegistryURL:   "https://git.example",
		packageRegistryToken: "secret",
	}
	set := &packagePublicationSet{
		cfg:          cfg,
		provider:     releaseForgejo,
		tag:          "v1.2.3",
		version:      "1.2.3",
		temporaryDir: t.TempDir(),
		commands: fakePackageCommandRunner{
			requireFunc: func(string) error { return nil },
			runFunc: func(options commandOptions) error { return nil },
			captureFunc: func(options commandOptions) (string, error) { return "", nil },
		},
	}
	publication := &packagePublication{kind: packageGradle, dir: source, manifest: filepath.Join(source, "build.gradle")}
	if err := preflightGradlePackage(set, publication); err != nil {
		t.Fatal(err)
	}
	var captured commandOptions
	set.commands = fakePackageCommandRunner{
		requireFunc: func(string) error { return nil },
		runFunc: func(opts commandOptions) error { captured = opts; return nil },
		captureFunc: func(commandOptions) (string, error) { return "", nil },
	}
	if err := publishGradlePackage(set, publication); err != nil {
		t.Fatal(err)
	}
	if captured.name != "gradle" {
		t.Fatalf("gradle executable = %q want gradle", captured.name)
	}
	if !stringContains(strings.Join(captured.args, " "), "publishAllPublicationsToFlakeReleaseRepository") {
		t.Fatalf("gradle args missing publish task: %q", captured.args)
	}
	if !stringContains(strings.Join(captured.args, " "), "--init-script") {
		t.Fatalf("gradle args missing init script: %q", captured.args)
	}
}

func TestGradleInitScriptGeneration(t *testing.T) {
	dir := t.TempDir()
	set := &packagePublicationSet{
		cfg: config{
			packageRegistryOwner: "owner",
			packageRegistryURL:   "https://git.example",
			packageRegistryToken: "s3cret",
		},
		provider:     releaseForgejo,
		temporaryDir: dir,
	}
	path, err := gradleInitScriptPath(set)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "flakeRelease") || !strings.Contains(content, "s3cret") || !strings.Contains(content, "https://git.example") {
		t.Fatalf("init script missing content: %s", content)
	}
}

func TestValidateMavenGradleGitHub(t *testing.T) {
	cfg := config{
		packageRegistryOwner: "owner",
		packageRegistryURL:   "https://maven.pkg.github.com/owner/repo",
		packageRegistryToken: "token",
	}
	if err := validatePackageRegistryConfig(cfg, releaseGitHub, []packageKind{packageMaven}); err != nil {
		t.Fatalf("maven github validation failed: %v", err)
	}
	if err := validatePackageRegistryConfig(cfg, releaseGitHub, []packageKind{packageGradle}); err != nil {
		t.Fatalf("gradle github validation failed: %v", err)
	}
	if err := validatePackageRegistryConfig(cfg, releaseGitHub, []packageKind{packageGo}); err == nil {
		t.Fatal("go should be rejected on github")
	}
}

func containsStr(slice []string, substr string) bool {
	for _, s := range slice {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func stringContains(s, substr string) bool {
	return strings.Contains(s, substr)
}
