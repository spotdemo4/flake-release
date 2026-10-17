package flakerelease

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

func TestPublicRegistryTokensEnableTargetsWithoutHostToken(t *testing.T) {
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "package.json"), `{"name":"@scope/web","version":"1.2.3"}`)
	set, err := discoverPackagesForTest(t, config{npmjsToken: "npm-token"}, releaseForgejo, source)
	if err != nil {
		t.Fatal(err)
	}
	if set == nil || len(set.packages) != 1 {
		t.Fatalf("prepared packages = %#v; want npmjs only", set)
	}
	publication := set.packages[0]
	if publication.registry != registryNPMJS || publication.name != "@scope/web" {
		t.Fatalf("publication = %#v; want @scope/web for npmjs", publication)
	}
	if !slices.Equal(publication.publishArgs, []string{"--access", "public"}) {
		t.Fatalf("publish args = %q; want scoped package published publicly", publication.publishArgs)
	}
}

func TestPublicRegistriesPublishAlongsideHostRegistry(t *testing.T) {
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "package.json"), `{"name":"web","version":"1.2.3"}`)
	set, err := discoverPackagesForTest(t, config{packageRegistryToken: "host-token", npmjsToken: "npm-token"}, releaseGitHub, source)
	if err != nil {
		t.Fatal(err)
	}
	if set == nil || len(set.packages) != 2 {
		t.Fatalf("prepared packages = %#v; want host and npmjs", set)
	}
	host, public := set.packages[0], set.packages[1]
	if host.registry != registryHost || host.name != "@owner/web" {
		t.Fatalf("host publication = %s for %q; want GitHub-scoped @owner/web", host.name, host.registry)
	}
	if public.registry != registryNPMJS || public.name != "web" || len(public.publishArgs) != 0 {
		t.Fatalf("npmjs publication = %s %q; want unscoped web without access override", public.name, public.publishArgs)
	}
	if host.dir == public.dir {
		t.Fatal("host and npmjs publications share a staged source")
	}
}

func TestPublicRegistriesDoNotInheritDeprecatedExplicitKinds(t *testing.T) {
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "package.json"), `{"name":"web","version":"0.0.0","private":true}`)
	set, err := discoverPackagesForTest(t, config{npmjsToken: "npm-token", publishPackages: ","}, releaseForgejo, source)
	if err != nil {
		t.Fatalf("private package for npmjs was not skipped: %v", err)
	}
	if set != nil {
		t.Fatalf("private npm package was prepared: %#v", set.packages)
	}
}

func TestPackageIdentityIsPerRegistry(t *testing.T) {
	host := packagePublication{kind: packageNPM, name: "web", version: "1.2.3"}
	public := packagePublication{kind: packageNPM, registry: registryNPMJS, name: "web", version: "1.2.3"}
	if publicationIdentity(host) == publicationIdentity(public) {
		t.Fatal("the same package for different registries has the same identity")
	}
}

func TestCratesIOCargoCommand(t *testing.T) {
	set := &packagePublicationSet{cfg: config{packageRegistryToken: "host-token", cratesIOToken: "crates-token"}}
	publication := &packagePublication{kind: packageCargo, registry: registryCratesIO, dir: "/src", manifest: "/src/Cargo.toml"}
	command := cargoCommand(set, publication, false)
	want := []string{"publish", "--allow-dirty", "--registry", "crates-io", "--manifest-path", "/src/Cargo.toml"}
	if !slices.Equal(command.args, want) {
		t.Fatalf("cargo args = %q; want %q", command.args, want)
	}
	if !slices.Equal(command.env, []string{"CARGO_REGISTRY_TOKEN=crates-token"}) {
		t.Fatalf("cargo env = %q; want crates.io token only", command.env)
	}
	if !slices.Equal(command.secrets, []string{"crates-token"}) {
		t.Fatalf("cargo secrets = %q", command.secrets)
	}
}

func TestCargoPublishListWithoutCratesIOIsNotPublishableThere(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "Cargo.toml")
	metadata := func(publish string) fakePackageCommandRunner {
		return fakePackageCommandRunner{
			requireFunc: func(string) error { return nil },
			runFunc:     func(commandOptions) error { return nil },
			captureFunc: func(commandOptions) (string, error) {
				return `{"packages":[{"name":"app","version":"1.2.3","manifest_path":"` + manifest + `","publish":` + publish + `}]}`, nil
			},
		}
	}
	set := &packagePublicationSet{cfg: config{cratesIOToken: "token"}, commands: metadata(`["internal"]`)}
	publication := &packagePublication{kind: packageCargo, registry: registryCratesIO, dir: dir, manifest: manifest}
	if err := preflightCargoPackage(set, publication); !errors.Is(err, errPackageNotPublishable) {
		t.Fatalf("error = %v; want not publishable to crates.io", err)
	}
	set.commands = metadata(`["internal","crates-io"]`)
	if err := preflightCargoPackage(set, publication); err != nil {
		t.Fatalf("package listing crates-io was rejected: %v", err)
	}
}

func TestNPMJSConfigUsesNPMJSToken(t *testing.T) {
	set := &packagePublicationSet{
		cfg:          config{packageRegistryToken: "host-token", npmjsToken: "npm-token"},
		provider:     releaseForgejo,
		temporaryDir: t.TempDir(),
	}
	path, err := set.npmConfig(registryNPMJS)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "registry=https://registry.npmjs.org/\n//registry.npmjs.org/:_authToken=npm-token\nalways-auth=true\n"
	if string(data) != want {
		t.Fatalf("npmrc = %q; want %q", data, want)
	}
	command := npmCommand(set, &packagePublication{registry: registryNPMJS, publishArgs: []string{"--access", "public"}}, path, true)
	if !slices.Equal(command.args, []string{"publish", "--registry", "https://registry.npmjs.org/", "--access", "public", "--dry-run"}) {
		t.Fatalf("npm args = %q", command.args)
	}
	if !slices.Equal(command.secrets, []string{"npm-token"}) {
		t.Fatalf("npm secrets = %q", command.secrets)
	}
}

func TestValidatePublicRegistryConfig(t *testing.T) {
	cfg := config{mavenCentralToken: "token", mavenCentralSigningKey: "key"}
	if err := validatePublicRegistryConfig(cfg, registryMavenCentral); err == nil || !strings.Contains(err.Error(), "MAVEN_CENTRAL_USERNAME") {
		t.Fatalf("error = %v; want missing username", err)
	}
	cfg.dryRun = true
	if err := validatePublicRegistryConfig(cfg, registryMavenCentral); err != nil {
		t.Fatalf("dry run required a username: %v", err)
	}
	cfg.mavenCentralSigningKey = ""
	if err := validatePublicRegistryConfig(cfg, registryMavenCentral); err == nil || !strings.Contains(err.Error(), "MAVEN_CENTRAL_SIGNING_KEY") {
		t.Fatalf("error = %v; want missing signing key", err)
	}
	if err := validatePublicRegistryConfig(config{}, registryCratesIO); err != nil {
		t.Fatal(err)
	}
}

func testSigningKey(t *testing.T, passphrase string) (string, *openpgp.Entity) {
	t.Helper()
	entity, err := openpgp.NewEntity("Release", "", "release@example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	var key bytes.Buffer
	writer, err := armor.Encode(&key, openpgp.PrivateKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if passphrase == "" {
		err = entity.SerializePrivate(writer, nil)
	} else {
		// Sign the identities before encrypting, since encrypted keys can't sign.
		if err = entity.SerializePrivate(io.Discard, nil); err == nil {
			if err = entity.EncryptPrivateKeys([]byte(passphrase), nil); err == nil {
				err = entity.SerializePrivateWithoutSigning(writer, nil)
			}
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return key.String(), entity
}

func TestReadSigningKey(t *testing.T) {
	key, _ := testSigningKey(t, "")
	if _, err := readSigningKey(key, ""); err != nil {
		t.Fatal(err)
	}
	encrypted, _ := testSigningKey(t, "secret")
	if _, err := readSigningKey(encrypted, ""); err == nil || !strings.Contains(err.Error(), "MAVEN_CENTRAL_SIGNING_PASSWORD") {
		t.Fatalf("error = %v; want missing passphrase", err)
	}
	if _, err := readSigningKey(encrypted, "wrong"); err == nil {
		t.Fatal("wrong passphrase was accepted")
	}
	if _, err := readSigningKey(encrypted, "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := readSigningKey("not a key", ""); err == nil {
		t.Fatal("invalid key was accepted")
	}
}

const centralTestPom = `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>my-lib</artifactId>
  <version>1.2.3</version>
  <name>my-lib</name>
  <description>An example library</description>
  <url>https://example.com</url>
  <licenses><license><name>MIT</name></license></licenses>
  <developers><developer><name>Example</name></developer></developers>
  <scm><url>https://example.com/repo</url></scm>
</project>`

// writeCentralTestRepository writes the files a Maven deploy of centralTestPom
// produces, including metadata and checksums the bundle replaces.
func writeCentralTestRepository(t *testing.T, repository string, pom string, classifiers ...string) {
	t.Helper()
	dir := filepath.Join(repository, "com", "example", "my-lib", "1.2.3")
	writeTestFile(t, filepath.Join(dir, "my-lib-1.2.3.pom"), pom)
	writeTestFile(t, filepath.Join(dir, "my-lib-1.2.3.pom.md5"), "stale")
	writeTestFile(t, filepath.Join(dir, "my-lib-1.2.3.jar"), "jar")
	for _, classifier := range classifiers {
		writeTestFile(t, filepath.Join(dir, "my-lib-1.2.3-"+classifier+".jar"), classifier)
	}
	writeTestFile(t, filepath.Join(repository, "com", "example", "my-lib", "maven-metadata.xml"), "<metadata/>")
}

func newCentralTestSet(t *testing.T, key string, run func(commandOptions) error) *packagePublicationSet {
	t.Helper()
	return &packagePublicationSet{
		cfg: config{
			mavenCentralUsername:   "user",
			mavenCentralToken:      "token",
			mavenCentralSigningKey: key,
		},
		provider:     releaseForgejo,
		releaseTag:   parseReleaseTag("v1.2.3"),
		temporaryDir: t.TempDir(),
		commands: fakePackageCommandRunner{
			requireFunc: func(string) error { return nil },
			runFunc:     run,
			captureFunc: func(commandOptions) (string, error) { return "", nil },
		},
	}
}

// deployedRepository returns the file repository a Maven Central build command targets.
func deployedRepository(t *testing.T, options commandOptions) string {
	t.Helper()
	for _, arg := range options.args {
		if value, ok := strings.CutPrefix(arg, "-DaltDeploymentRepository=flake-release::default::file://"); ok {
			return value
		}
	}
	t.Fatalf("build command has no file deployment repository: %q", options.args)
	return ""
}

func TestPreflightMavenCentralBuildsSignedBundle(t *testing.T) {
	key, entity := testSigningKey(t, "")
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "pom.xml"), centralTestPom)
	var build commandOptions
	set := newCentralTestSet(t, key, func(options commandOptions) error {
		build = options
		writeCentralTestRepository(t, deployedRepository(t, options), centralTestPom, "sources", "javadoc")
		return nil
	})
	publication := &packagePublication{kind: packageMaven, registry: registryMavenCentral, dir: source, manifest: filepath.Join(source, "pom.xml")}
	if err := publication.preflight(set); err != nil {
		t.Fatal(err)
	}
	if build.name != "mvn" || build.args[0] != "deploy" || !slices.Contains(build.args, "-Dgpg.skip=true") {
		t.Fatalf("build command = %q %q", build.name, build.args)
	}
	if publication.name != "com.example:my-lib" || publication.version != "1.2.3" || len(publication.artifacts) != 1 {
		t.Fatalf("publication = %#v", publication)
	}

	archive, err := zip.OpenReader(publication.artifacts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := map[string]string{}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[file.Name] = string(data)
	}
	base := "com/example/my-lib/1.2.3/my-lib-1.2.3"
	var want []string
	for _, artifact := range []string{base + "-javadoc.jar", base + "-sources.jar", base + ".jar", base + ".pom"} {
		want = append(want, artifact, artifact+".md5", artifact+".sha1", artifact+".asc")
	}
	got := make([]string, 0, len(files))
	for name := range files {
		got = append(got, name)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("bundle files = %q; want %q", got, want)
	}
	sha1Sum, md5Sum := sha1.Sum([]byte("jar")), md5.Sum([]byte("jar"))
	if files[base+".jar.sha1"] != hex.EncodeToString(sha1Sum[:]) || files[base+".jar.md5"] != hex.EncodeToString(md5Sum[:]) {
		t.Fatalf("jar checksums = %q, %q", files[base+".jar.sha1"], files[base+".jar.md5"])
	}
	if files[base+".pom.md5"] == "stale" {
		t.Fatal("bundle kept the stale deployed checksum")
	}
	if _, err := openpgp.CheckArmoredDetachedSignature(openpgp.EntityList{entity}, strings.NewReader(files[base+".pom"]), strings.NewReader(files[base+".pom.asc"]), nil); err != nil {
		t.Fatalf("pom signature does not verify: %v", err)
	}
}

func TestPreflightMavenCentralGradleUsesFileRepository(t *testing.T) {
	key, _ := testSigningKey(t, "")
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "build.gradle"), "group = 'com.example'\nversion = '1.2.3'\n")
	writeTestFile(t, filepath.Join(source, "settings.gradle"), "rootProject.name = 'my-lib'\n")
	var initScript string
	set := newCentralTestSet(t, key, func(options commandOptions) error {
		if options.name != "gradle" || !slices.Contains(options.args, "publishAllPublicationsToFlakeReleaseRepository") {
			t.Fatalf("build command = %q %q", options.name, options.args)
		}
		data, err := os.ReadFile(options.args[1])
		if err != nil {
			t.Fatal(err)
		}
		initScript = string(data)
		repository := filepath.Join(filepath.Dir(options.args[1]), "repository")
		writeCentralTestRepository(t, repository, centralTestPom, "sources", "javadoc")
		return nil
	})
	publication := &packagePublication{kind: packageGradle, registry: registryMavenCentral, dir: source, manifest: filepath.Join(source, "build.gradle")}
	if err := publication.preflight(set); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(initScript, `url = uri("file:///`) || strings.Contains(initScript, "credentials") {
		t.Fatalf("init script does not target an unauthenticated file repository:\n%s", initScript)
	}
}

func TestPreflightMavenCentralReportsMissingRequirements(t *testing.T) {
	key, _ := testSigningKey(t, "")
	source := t.TempDir()
	pom := `<project><modelVersion>4.0.0</modelVersion><groupId>com.example</groupId><artifactId>my-lib</artifactId><version>1.2.3</version></project>`
	writeTestFile(t, filepath.Join(source, "pom.xml"), pom)
	set := newCentralTestSet(t, key, func(options commandOptions) error {
		writeCentralTestRepository(t, deployedRepository(t, options), pom, "sources")
		return nil
	})
	publication := &packagePublication{kind: packageMaven, registry: registryMavenCentral, dir: source, manifest: filepath.Join(source, "pom.xml")}
	err := publication.preflight(set)
	if err == nil {
		t.Fatal("bundle without required metadata was accepted")
	}
	for _, missing := range []string{"javadoc jar", "<name>", "<description>", "<url>", "<licenses>", "<developers>", "<scm>"} {
		if !strings.Contains(err.Error(), missing) {
			t.Errorf("error %q does not report missing %s", err, missing)
		}
	}
	if strings.Contains(err.Error(), "sources jar") {
		t.Errorf("error %q reports the sources jar that exists", err)
	}
}

func TestPreflightMavenCentralRejectsSnapshot(t *testing.T) {
	key, _ := testSigningKey(t, "")
	source := t.TempDir()
	writeTestFile(t, filepath.Join(source, "pom.xml"), strings.Replace(centralTestPom, "<version>1.2.3</version>", "<version>1.2.3-SNAPSHOT</version>", 1))
	set := newCentralTestSet(t, key, func(commandOptions) error {
		t.Fatal("SNAPSHOT version was built")
		return nil
	})
	publication := &packagePublication{kind: packageMaven, registry: registryMavenCentral, dir: source, manifest: filepath.Join(source, "pom.xml")}
	if err := publication.preflight(set); err == nil || !strings.Contains(err.Error(), "SNAPSHOT") {
		t.Fatalf("error = %v; want SNAPSHOT rejection", err)
	}
}

func useMavenCentralServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	previousURL, previousInterval := mavenCentralURL, mavenCentralPollInterval
	mavenCentralURL, mavenCentralPollInterval = server.URL, time.Millisecond
	t.Cleanup(func() { mavenCentralURL, mavenCentralPollInterval = previousURL, previousInterval })
}

func TestPublishMavenCentralUploadsAndWaitsForValidation(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "bundle.zip")
	writeTestFile(t, bundle, "bundle contents")
	wantAuthorization := "Bearer " + base64.StdEncoding.EncodeToString([]byte("user:token"))
	states := []string{"PENDING", "VALIDATING", "PUBLISHING"}
	statusRequests := 0
	useMavenCentralServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != wantAuthorization {
			t.Errorf("authorization = %q; want %q", r.Header.Get("Authorization"), wantAuthorization)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s; want POST", r.Method)
		}
		switch r.URL.Path {
		case "/api/v1/publisher/upload":
			if r.URL.Query().Get("publishingType") != "AUTOMATIC" || r.URL.Query().Get("name") != "com.example:my-lib:1.2.3" {
				t.Errorf("upload query = %q", r.URL.RawQuery)
			}
			file, header, err := r.FormFile("bundle")
			if err != nil {
				t.Errorf("upload has no bundle: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			data, _ := io.ReadAll(file)
			if string(data) != "bundle contents" || header.Filename != "bundle.zip" {
				t.Errorf("uploaded bundle %s = %q", header.Filename, data)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, "deployment-id\n")
		case "/api/v1/publisher/status":
			if r.URL.Query().Get("id") != "deployment-id" {
				t.Errorf("status id = %q", r.URL.Query().Get("id"))
			}
			_, _ = io.WriteString(w, `{"deploymentId":"deployment-id","deploymentState":"`+states[statusRequests]+`"}`)
			statusRequests++
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	})
	set := &packagePublicationSet{cfg: config{mavenCentralUsername: "user", mavenCentralToken: "token"}}
	publication := &packagePublication{kind: packageMaven, registry: registryMavenCentral, name: "com.example:my-lib", version: "1.2.3", artifacts: []string{bundle}}
	if err := publication.publish(set); err != nil {
		t.Fatal(err)
	}
	if statusRequests != len(states) {
		t.Fatalf("status requests = %d; want polling until %s", statusRequests, states[len(states)-1])
	}
}

func TestPublishMavenCentralReportsFailedDeployment(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "bundle.zip")
	writeTestFile(t, bundle, "bundle contents")
	useMavenCentralServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/publisher/upload" {
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, "deployment-id")
			return
		}
		_, _ = io.WriteString(w, `{"deploymentState":"FAILED","errors":{"pkg:maven/com.example/my-lib@1.2.3":["Component with package url already exists"]}}`)
	})
	set := &packagePublicationSet{cfg: config{mavenCentralUsername: "user", mavenCentralToken: "token"}}
	publication := &packagePublication{kind: packageMaven, registry: registryMavenCentral, name: "com.example:my-lib", version: "1.2.3", artifacts: []string{bundle}}
	if err := publication.publish(set); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v; want failed deployment with its errors", err)
	}
}
