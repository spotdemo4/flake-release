package flakerelease

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func preflightMavenPackage(set *packagePublicationSet, publication *packagePublication) error {
	if err := requireMaven(set, publication); err != nil {
		return err
	}
	groupID, artifactID, version, err := parseMavenPom(publication.manifest)
	if err != nil {
		return err
	}
	if groupID == "" || artifactID == "" {
		return fmt.Errorf("pom.xml requires groupId and artifactId")
	}
	if version == "" {
		return fmt.Errorf("pom.xml requires version")
	}
	if err := requireStrictPackageVersion(version); err != nil {
		return fmt.Errorf("maven package: %w", err)
	}
	publication.name = groupID + ":" + artifactID
	publication.version = version
	if _, err := mavenSettingsPath(set); err != nil {
		return err
	}
	return nil
}

func publishMavenPackage(set *packagePublicationSet, publication *packagePublication) error {
	settings, err := mavenSettingsPath(set)
	if err != nil {
		return err
	}
	executable, err := mavenExecutable(publication.dir)
	if err != nil {
		return err
	}
	registry := strings.TrimRight(set.registryURL(packageMaven), "/")
	args := []string{
		"-s", settings,
		"deploy",
		"--batch-mode",
		"-DaltDeploymentRepository=flake-release::default::" + registry,
		"-DaltSnapshotDeploymentRepository=flake-release::default::" + registry,
	}
	return set.commands.run(commandOptions{
		name:    executable,
		args:    args,
		dir:     publication.dir,
		secrets: []string{set.cfg.packageRegistryToken},
	})
}

func requireMaven(set *packagePublicationSet, publication *packagePublication) error {
	if isFile(filepath.Join(publication.dir, "mvnw")) {
		return nil
	}
	return set.commands.require("mvn")
}

func mavenExecutable(dir string) (string, error) {
	wrapper := filepath.Join(dir, "mvnw")
	if isFile(wrapper) {
		return wrapper, nil
	}
	return "mvn", nil
}

func mavenSettingsPath(set *packagePublicationSet) (string, error) {
	path := filepath.Join(set.temporaryDir, "maven-settings.xml")
	if isFile(path) {
		return path, nil
	}
	username := set.cfg.packageRegistryUsername
	if username == "" {
		username = set.cfg.packageRegistryOwner
	}
	token := set.cfg.packageRegistryToken
	content := generateMavenSettings(username, token)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func generateMavenSettings(username string, token string) string {
	escapedUsername := xmlEscape(username)
	escapedToken := xmlEscape(token)
	headerValue := xmlEscape("token " + token)
	// Include both password and header for compatibility with Gitea/Forgejo/GitHub
	template := `<settings>
  <servers>
    <server>
      <id>flake-release</id>
      <username>%s</username>
      <password>%s</password>
      <configuration>
        <httpHeaders>
          <property>
            <name>Authorization</name>
            <value>%s</value>
          </property>
        </httpHeaders>
      </configuration>
    </server>
    <server>
      <id>forgejo</id>
      <username>%s</username>
      <password>%s</password>
      <configuration>
        <httpHeaders>
          <property>
            <name>Authorization</name>
            <value>%s</value>
          </property>
        </httpHeaders>
      </configuration>
    </server>
    <server>
      <id>gitea</id>
      <username>%s</username>
      <password>%s</password>
      <configuration>
        <httpHeaders>
          <property>
            <name>Authorization</name>
            <value>%s</value>
          </property>
        </httpHeaders>
      </configuration>
    </server>
    <server>
      <id>github</id>
      <username>%s</username>
      <password>%s</password>
      <configuration>
        <httpHeaders>
          <property>
            <name>Authorization</name>
            <value>%s</value>
          </property>
        </httpHeaders>
      </configuration>
    </server>
  </servers>
</settings>
`
	return fmt.Sprintf(template,
		escapedUsername, escapedToken, headerValue,
		escapedUsername, escapedToken, headerValue,
		escapedUsername, escapedToken, headerValue,
		escapedUsername, escapedToken, headerValue,
	)
}

func xmlEscape(value string) string {
	escaped := strings.ReplaceAll(value, "&", "&amp;")
	escaped = strings.ReplaceAll(escaped, "<", "&lt;")
	escaped = strings.ReplaceAll(escaped, ">", "&gt;")
	escaped = strings.ReplaceAll(escaped, "\"", "&quot;")
	escaped = strings.ReplaceAll(escaped, "'", "&apos;")
	return escaped
}

type mavenPom struct {
	XMLName    xml.Name `xml:"project"`
	GroupID    string   `xml:"groupId"`
	ArtifactID string   `xml:"artifactId"`
	Version    string   `xml:"version"`
	Parent     struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
		Version    string `xml:"version"`
	} `xml:"parent"`
	Properties mavenProperties `xml:"properties"`
}

type mavenProperties map[string]string

func (m *mavenProperties) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	*m = make(map[string]string)
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch element := token.(type) {
		case xml.StartElement:
			var value string
			if err := decoder.DecodeElement(&value, &element); err != nil {
				return err
			}
			(*m)[element.Name.Local] = strings.TrimSpace(value)
		case xml.EndElement:
			if element.Name == start.Name {
				return nil
			}
		}
	}
}

var propertyPlaceholder = regexp.MustCompile(`\$\{([^}]+)\}`)

func parseMavenPom(path string) (string, string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", err
	}
	var pom mavenPom
	if err := xml.Unmarshal(data, &pom); err != nil {
		return "", "", "", fmt.Errorf("parsing pom.xml: %w", err)
	}
	groupID := strings.TrimSpace(pom.GroupID)
	if groupID == "" {
		groupID = strings.TrimSpace(pom.Parent.GroupID)
	}
	artifactID := strings.TrimSpace(pom.ArtifactID)
	if artifactID == "" {
		artifactID = strings.TrimSpace(pom.Parent.ArtifactID)
	}
	version := strings.TrimSpace(pom.Version)
	if version == "" {
		version = strings.TrimSpace(pom.Parent.Version)
	}
	// Resolve property placeholders like ${revision}
	if strings.Contains(version, "${") {
		version = resolveMavenProperties(version, pom.Properties)
	}
	if strings.Contains(groupID, "${") {
		groupID = resolveMavenProperties(groupID, pom.Properties)
	}
	if strings.Contains(artifactID, "${") {
		artifactID = resolveMavenProperties(artifactID, pom.Properties)
	}
	// If still contains placeholder after resolution, fail with clear message
	if strings.Contains(version, "${") {
		return "", "", "", fmt.Errorf("pom.xml version %q contains unresolved property; set an explicit version matching git tag", version)
	}
	if strings.Contains(groupID, "${") || strings.Contains(artifactID, "${") {
		return "", "", "", fmt.Errorf("pom.xml groupId/artifactId contains unresolved property")
	}
	return groupID, artifactID, version, nil
}

func resolveMavenProperties(value string, properties mavenProperties) string {
	return propertyPlaceholder.ReplaceAllStringFunc(value, func(match string) string {
		key := propertyPlaceholder.FindStringSubmatch(match)[1]
		// Handle default value syntax like ${revision:-1.0.0} or ${env.VAR}
		// For simplicity, strip default handling: take before :- or :
		if idx := strings.Index(key, ":-"); idx != -1 {
			key = key[:idx]
		}
		if idx := strings.Index(key, ":"); idx != -1 {
			key = key[:idx]
		}
		if replacement, ok := properties[key]; ok {
			return replacement
		}
		// Also check for project.version etc not in properties
		return match
	})
}
