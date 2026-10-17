package flakerelease

import (
	"archive/zip"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
)

var (
	mavenCentralURL          = "https://central.sonatype.com"
	mavenCentralPollInterval = 10 * time.Second
	// mavenCentralTimeout bounds how long a deployment may take to pass validation.
	mavenCentralTimeout = 30 * time.Minute
)

// preflightMavenCentralPackage builds a Maven or Gradle project into a local Maven
// repository, then validates, checksums, and signs it into a Central Portal bundle.
func preflightMavenCentralPackage(set *packagePublicationSet, publication *packagePublication) error {
	var err error
	switch publication.kind {
	case packageMaven:
		err = mavenPackageIdentity(set, publication)
	case packageGradle:
		err = gradlePackageIdentity(set, publication)
	default:
		err = fmt.Errorf("maven central does not support %s packages", publication.kind)
	}
	if err != nil {
		return err
	}
	if strings.HasSuffix(publication.version, "-SNAPSHOT") {
		return fmt.Errorf("maven central does not accept SNAPSHOT version %q", publication.version)
	}
	signer, err := set.mavenCentralSigner()
	if err != nil {
		return err
	}

	staging, err := os.MkdirTemp(set.temporaryDir, "maven-central-")
	if err != nil {
		return err
	}
	repository := filepath.Join(staging, "repository")
	if err := os.Mkdir(repository, 0o755); err != nil {
		return err
	}
	status("building local Maven repository")
	build, err := mavenCentralBuildCommand(publication, staging, repository)
	if err != nil {
		return err
	}
	if err := set.commands.run(build); err != nil {
		return err
	}
	groupID, artifactID, _ := strings.Cut(publication.name, ":")
	if err := validateMavenCentralRepository(repository, groupID, artifactID, publication.version); err != nil {
		return err
	}
	bundle := filepath.Join(staging, "bundle.zip")
	status("signing bundle")
	if err := writeMavenCentralBundle(repository, bundle, signer); err != nil {
		return err
	}
	publication.artifacts = []string{bundle}
	return nil
}

// mavenCentralBuildCommand returns the command that publishes the project to the
// file repository at repository, writing any Gradle init script it needs to staging.
func mavenCentralBuildCommand(publication *packagePublication, staging string, repository string) (commandOptions, error) {
	repositoryURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(repository)}).String()
	if publication.kind == packageGradle {
		initScript := filepath.Join(staging, "gradle-init.gradle")
		if err := os.WriteFile(initScript, []byte(generateGradleRepositoryInitScript(repositoryURL, "")), 0o600); err != nil {
			return commandOptions{}, err
		}
		executable, err := gradleExecutable(publication.dir)
		return commandOptions{
			name: executable,
			args: []string{"--init-script", initScript, "publishAllPublicationsToFlakeReleaseRepository", "--no-daemon"},
			dir:  publication.dir,
		}, err
	}
	executable, err := mavenExecutable(publication.dir)
	return commandOptions{
		name: executable,
		args: []string{
			"deploy",
			"--batch-mode",
			// flake-release signs the bundle itself, so the project needs no gpg.
			"-Dgpg.skip=true",
			"-DaltDeploymentRepository=flake-release::default::" + repositoryURL,
		},
		dir: publication.dir,
	}, err
}

func (set *packagePublicationSet) mavenCentralSigner() (*openpgp.Entity, error) {
	if set.signer != nil {
		return set.signer, nil
	}
	signer, err := readSigningKey(set.cfg.mavenCentralSigningKey, set.cfg.mavenCentralSigningPassword)
	if err != nil {
		return nil, err
	}
	set.signer = signer
	return signer, nil
}

// readSigningKey returns the first entity in an ASCII-armored private key that can
// sign, decrypting it with passphrase when it is protected.
func readSigningKey(key string, passphrase string) (*openpgp.Entity, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("MAVEN_CENTRAL_SIGNING_KEY is required to publish to Maven Central")
	}
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(key))
	if err != nil {
		return nil, fmt.Errorf("reading MAVEN_CENTRAL_SIGNING_KEY: %w", err)
	}
	now := time.Now()
	for _, entity := range entities {
		if entity.PrivateKey == nil {
			continue
		}
		if err := entity.DecryptPrivateKeys([]byte(passphrase)); err != nil {
			if passphrase == "" {
				return nil, errors.New("MAVEN_CENTRAL_SIGNING_KEY is encrypted; set MAVEN_CENTRAL_SIGNING_PASSWORD")
			}
			return nil, fmt.Errorf("decrypting MAVEN_CENTRAL_SIGNING_KEY: %w", err)
		}
		if signingKey, ok := entity.SigningKey(now); ok && signingKey.PrivateKey != nil && !signingKey.PrivateKey.Dummy() {
			return entity, nil
		}
	}
	return nil, errors.New("MAVEN_CENTRAL_SIGNING_KEY contains no usable private signing key")
}

type mavenCentralPom struct {
	Packaging   string    `xml:"packaging"`
	Name        string    `xml:"name"`
	Description string    `xml:"description"`
	URL         string    `xml:"url"`
	Parent      *struct{} `xml:"parent"`
	Licenses    []struct {
		Name string `xml:"name"`
	} `xml:"licenses>license"`
	Developers []struct {
		ID    string `xml:"id"`
		Name  string `xml:"name"`
		Email string `xml:"email"`
	} `xml:"developers>developer"`
	SCM struct {
		URL                 string `xml:"url"`
		Connection          string `xml:"connection"`
		DeveloperConnection string `xml:"developerConnection"`
	} `xml:"scm"`
}

// validateMavenCentralRepository checks the local repository against the Maven
// Central requirements, so a dry run reports what the Central Portal would reject.
func validateMavenCentralRepository(repository string, groupID string, artifactID string, version string) error {
	versionDir := filepath.Join(repository, filepath.FromSlash(strings.ReplaceAll(groupID, ".", "/")), artifactID, version)
	if !isFile(filepath.Join(versionDir, artifactID+"-"+version+".pom")) {
		return fmt.Errorf("build did not publish %s:%s:%s with a POM", groupID, artifactID, version)
	}

	var problems []string
	err := filepath.WalkDir(repository, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".pom") {
			return walkErr
		}
		relative, err := filepath.Rel(repository, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var pom mavenCentralPom
		if err := xml.Unmarshal(data, &pom); err != nil {
			return fmt.Errorf("parsing %s: %w", relative, err)
		}
		var missing []string
		if strings.TrimSpace(pom.Packaging) != "pom" {
			base := strings.TrimSuffix(path, ".pom")
			for _, classifier := range []string{"sources", "javadoc"} {
				if !isFile(base + "-" + classifier + ".jar") {
					missing = append(missing, classifier+" jar")
				}
			}
		}
		// Metadata may be inherited from a parent POM, which Central resolves itself.
		if pom.Parent == nil {
			for _, field := range []struct {
				name    string
				present bool
			}{
				{"name", strings.TrimSpace(pom.Name) != ""},
				{"description", strings.TrimSpace(pom.Description) != ""},
				{"url", strings.TrimSpace(pom.URL) != ""},
				{"licenses", len(pom.Licenses) > 0},
				{"developers", len(pom.Developers) > 0},
				{"scm", strings.TrimSpace(pom.SCM.URL+pom.SCM.Connection+pom.SCM.DeveloperConnection) != ""},
			} {
				if !field.present {
					missing = append(missing, "<"+field.name+">")
				}
			}
		}
		if len(missing) > 0 {
			problems = append(problems, fmt.Sprintf("%s is missing %s", relative, strings.Join(missing, ", ")))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("maven central requirements are not met: %s", strings.Join(problems, "; "))
	}
	return nil
}

// isMavenRepositoryMetadata reports whether path is a checksum, signature, or
// maven-metadata.xml file. Bundles get fresh checksums and signatures, and Central
// maintains maven-metadata.xml itself.
func isMavenRepositoryMetadata(path string) bool {
	if strings.HasPrefix(filepath.Base(path), "maven-metadata") {
		return true
	}
	for _, suffix := range []string{".md5", ".sha1", ".sha256", ".sha512", ".asc"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// writeMavenCentralBundle zips the artifacts in repository with MD5 and SHA-1
// checksums and a detached ASCII-armored signature for each.
func writeMavenCentralBundle(repository string, bundle string, signer *openpgp.Entity) error {
	output, err := os.OpenFile(bundle, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(output)
	walkErr := filepath.WalkDir(repository, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || isMavenRepositoryMetadata(path) {
			return walkErr
		}
		relative, err := filepath.Rel(repository, path)
		if err != nil {
			return err
		}
		return addMavenCentralBundleFile(archive, path, filepath.ToSlash(relative), signer)
	})
	closeErr := errors.Join(archive.Close(), output.Close())
	if walkErr != nil {
		return walkErr
	}
	return closeErr
}

func addMavenCentralBundleFile(archive *zip.Writer, path string, name string, signer *openpgp.Entity) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	entry, err := archive.Create(name)
	if err != nil {
		return err
	}
	checksums := []struct {
		suffix string
		hash   hash.Hash
	}{
		{".md5", md5.New()},
		{".sha1", sha1.New()},
	}
	writers := []io.Writer{entry}
	for _, checksum := range checksums {
		writers = append(writers, checksum.hash)
	}
	if _, err := io.Copy(io.MultiWriter(writers...), input); err != nil {
		return err
	}
	for _, checksum := range checksums {
		entry, err := archive.Create(name + checksum.suffix)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(entry, hex.EncodeToString(checksum.hash.Sum(nil))); err != nil {
			return err
		}
	}

	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return err
	}
	signature, err := archive.Create(name + ".asc")
	if err != nil {
		return err
	}
	if err := openpgp.ArmoredDetachSign(signature, signer, input, nil); err != nil {
		return fmt.Errorf("signing %s: %w", name, err)
	}
	return nil
}

type mavenCentralDeployment struct {
	DeploymentState string          `json:"deploymentState"`
	Errors          json.RawMessage `json:"errors"`
}

// publishMavenCentralPackage uploads the prepared bundle to the Central Portal for
// automatic publishing, then waits for it to pass validation.
func publishMavenCentralPackage(set *packagePublicationSet, publication *packagePublication) error {
	if len(publication.artifacts) != 1 {
		return fmt.Errorf("maven central bundle was not prepared")
	}
	body, contentType, err := multipartFileBody("bundle", publication.artifacts[0])
	if err != nil {
		return err
	}
	defer body.Close()

	token := base64.StdEncoding.EncodeToString([]byte(set.cfg.mavenCentralUsername + ":" + set.cfg.mavenCentralToken))
	query := url.Values{
		"name":           {publication.name + ":" + publication.version},
		"publishingType": {"AUTOMATIC"},
	}
	response, err := httpRequest(httpRequestOptions{
		method:      http.MethodPost,
		url:         strings.TrimRight(mavenCentralURL, "/") + "/api/v1/publisher/upload?" + query.Encode(),
		token:       token,
		authScheme:  "Bearer",
		accept:      "text/plain",
		contentType: contentType,
		body:        body,
	})
	if err != nil {
		return err
	}
	deploymentID := strings.TrimSpace(string(response))
	if deploymentID == "" {
		return fmt.Errorf("maven central upload returned no deployment ID")
	}
	detail("deployment: %s", deploymentID)
	return waitForMavenCentralDeployment(token, deploymentID)
}

func waitForMavenCentralDeployment(token string, deploymentID string) error {
	endpoint := strings.TrimRight(mavenCentralURL, "/") + "/api/v1/publisher/status?" + url.Values{"id": {deploymentID}}.Encode()
	deadline := time.Now().Add(mavenCentralTimeout)
	state := ""
	for {
		response, err := httpRequest(httpRequestOptions{
			method:     http.MethodPost,
			url:        endpoint,
			token:      token,
			authScheme: "Bearer",
		})
		if err != nil {
			return err
		}
		var deployment mavenCentralDeployment
		if err := json.Unmarshal(response, &deployment); err != nil {
			return fmt.Errorf("parsing maven central deployment status: %w", err)
		}
		if deployment.DeploymentState != state {
			state = deployment.DeploymentState
			status("deployment %s", strings.ToLower(state))
		}
		switch state {
		case "PUBLISHING", "PUBLISHED":
			return nil
		case "FAILED":
			if len(deployment.Errors) == 0 || string(deployment.Errors) == "null" {
				return fmt.Errorf("maven central deployment %s failed", deploymentID)
			}
			return fmt.Errorf("maven central deployment %s failed: %s", deploymentID, deployment.Errors)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for maven central deployment %s in state %s", deploymentID, state)
		}
		time.Sleep(mavenCentralPollInterval)
	}
}
