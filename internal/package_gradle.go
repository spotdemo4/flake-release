package flakerelease

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func preflightGradlePackage(set *packagePublicationSet, publication *packagePublication) error {
	if err := requireGradle(set, publication); err != nil {
		return err
	}
	group, name, version, err := parseGradleCoordinates(publication.dir, publication.manifest, publication.source)
	if err != nil {
		return err
	}
	if group == "" || name == "" {
		return fmt.Errorf("gradle project requires group and name (checked gradle.properties, settings.gradle, and %s)", filepath.Base(publication.manifest))
	}
	if version == "" {
		return fmt.Errorf("gradle project requires version (checked gradle.properties and %s)", filepath.Base(publication.manifest))
	}
	if err := requireStrictPackageVersion(version); err != nil {
		return fmt.Errorf("gradle package: %w", err)
	}
	publication.name = group + ":" + name
	publication.version = version
	if _, err := gradleInitScriptPath(set); err != nil {
		return err
	}
	return nil
}

func publishGradlePackage(set *packagePublicationSet, publication *packagePublication) error {
	initScript, err := gradleInitScriptPath(set)
	if err != nil {
		return err
	}
	executable, err := gradleExecutable(publication.dir)
	if err != nil {
		return err
	}
	args := []string{
		"--init-script", initScript,
		"publishAllPublicationsToFlakeReleaseRepository",
		"--no-daemon",
	}
	return set.commands.run(commandOptions{
		name:    executable,
		args:    args,
		dir:     publication.dir,
		secrets: []string{set.cfg.packageRegistryToken},
	})
}

func requireGradle(set *packagePublicationSet, publication *packagePublication) error {
	if isFile(filepath.Join(publication.dir, "gradlew")) {
		return nil
	}
	return set.commands.require("gradle")
}

func gradleExecutable(dir string) (string, error) {
	wrapper := filepath.Join(dir, "gradlew")
	if isFile(wrapper) {
		return wrapper, nil
	}
	return "gradle", nil
}

func gradleInitScriptPath(set *packagePublicationSet) (string, error) {
	path := filepath.Join(set.temporaryDir, "gradle-init.gradle")
	if isFile(path) {
		return path, nil
	}
	registry := strings.TrimRight(set.registryURL(packageGradle), "/")
	token := set.cfg.packageRegistryToken
	content := generateGradleInitScript(registry, token)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func generateGradleInitScript(registry string, token string) string {
	escapedRegistry := gradleEscape(registry)
	escapedToken := gradleEscape(token)
	// Use Groovy init script that works for both Groovy and Kotlin DSL projects
	return fmt.Sprintf(`allprojects {
    afterEvaluate {
        def publishing = extensions.findByName("publishing")
        if (publishing != null) {
            publishing.repositories {
                maven {
                    name = "flakeRelease"
                    url = uri("%s")
                    credentials(org.gradle.api.credentials.HttpHeaderCredentials) {
                        name = "Authorization"
                        value = "token %s"
                    }
                    authentication {
                        header(org.gradle.authentication.http.HttpHeaderAuthentication)
                    }
                }
            }
        }
    }
}
`, escapedRegistry, escapedToken)
}

func gradleEscape(value string) string {
	// Escape for Groovy double-quoted string
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		`$`, `\$`,
	)
	return replacer.Replace(value)
}

func parseGradleCoordinates(dir string, manifest string, source string) (string, string, string, error) {
	_ = source
	props := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(dir, "gradle.properties")); err == nil {
		props = parsePropertiesContent(string(data))
	}
	buildContent := ""
	if data, err := os.ReadFile(manifest); err == nil {
		buildContent = string(data)
	}
	settingsContent := ""
	for _, candidate := range []string{"settings.gradle", "settings.gradle.kts"} {
		if data, err := os.ReadFile(filepath.Join(dir, candidate)); err == nil {
			settingsContent = string(data)
			break
		}
	}
	// group
	group := findGradleString(buildContent, []string{
		`(?:project\.)?group\s*(?:=\s*)?["']([^"']+)["']`,
		`group\s+["']([^"']+)["']`,
	})
	if group == "" {
		group = strings.TrimSpace(props["group"])
	}
	// version
	version := findGradleString(buildContent, []string{
		`(?:project\.)?version\s*(?:=\s*)?["']([^"']+)["']`,
		`version\s+["']([^"']+)["']`,
	})
	if version == "" {
		version = strings.TrimSpace(props["version"])
	}
	// name / artifactId
	name := ""
	// try settings first
	if settingsContent != "" {
		name = findGradleString(settingsContent, []string{
			`rootProject\.name\s*(?:=\s*)?["']([^"']+)["']`,
		})
	}
	if name == "" {
		name = findGradleString(buildContent, []string{
			`archivesBaseName\s*(?:=\s*)?["']([^"']+)["']`,
			`artifactId\s*(?:=\s*)?["']([^"']+)["']`,
			`archivesName\s*(?:=\s*)?["']([^"']+)["']`,
		})
	}
	if name == "" {
		name = strings.TrimSpace(props["archivesBaseName"])
	}
	if name == "" {
		name = strings.TrimSpace(props["name"])
	}
	// Trim possible property placeholders - require explicit
	if strings.Contains(group, "${") || strings.Contains(name, "${") || strings.Contains(version, "${") {
		return "", "", "", fmt.Errorf("gradle group/name/version contains unresolved property placeholder; set explicit values in gradle.properties or build file")
	}
	return strings.TrimSpace(group), strings.TrimSpace(name), strings.TrimSpace(version), nil
}

func parsePropertiesContent(content string) map[string]string {
	result := map[string]string{}
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		// Handle line continuation?
		var key, value string
		if before, after, ok := strings.Cut(line, "="); ok {
			key = strings.TrimSpace(before)
			value = strings.TrimSpace(after)
		} else if before, after, ok := strings.Cut(line, ":"); ok {
			key = strings.TrimSpace(before)
			value = strings.TrimSpace(after)
		} else if before, after, ok := strings.Cut(line, " "); ok {
			key = strings.TrimSpace(before)
			value = strings.TrimSpace(after)
		} else {
			continue
		}
		// Remove possible escaped characters? keep simple
		result[key] = value
	}
	return result
}

var gradleFindCache = map[string]*regexp.Regexp{}

func findGradleString(content string, patterns []string) string {
	for _, pattern := range patterns {
		re, ok := gradleFindCache[pattern]
		if !ok {
			re = regexp.MustCompile(pattern)
			gradleFindCache[pattern] = re
		}
		if match := re.FindStringSubmatch(content); len(match) > 1 {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}
