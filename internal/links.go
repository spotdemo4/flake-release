package flakerelease

import (
	"net/url"
	"slices"
	"strings"
)

const releaseLinksHeading = "## Packages"

// releaseLink is a published package or container image listed in the release description.
// url is empty when the registry has no known web page for it.
type releaseLink struct {
	text     string
	url      string
	registry string
}

func (link releaseLink) markdown() string {
	line := "`" + link.text + "`"
	if link.url != "" {
		line = "[" + line + "](" + link.url + ")"
	}
	if link.registry != "" {
		line += " on " + link.registry
	}
	return "- " + line
}

// releaseBodyWithLinks adds the links missing from body to its Packages section, appending
// the section when body has none, and reports whether body changed. Links already present
// are kept, so runners releasing different packages to the same release each add their own.
func releaseBodyWithLinks(body string, links []releaseLink) (string, bool) {
	body = strings.TrimRight(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	lines := strings.Split(body, "\n")
	var missing []string
	for _, link := range links {
		line := link.markdown()
		if !slices.Contains(lines, line) && !slices.Contains(missing, line) {
			missing = append(missing, line)
		}
	}
	if len(missing) == 0 {
		return body, false
	}

	start := slices.Index(lines, releaseLinksHeading)
	if start < 0 {
		section := releaseLinksHeading + "\n\n" + strings.Join(missing, "\n")
		if body == "" {
			return section, true
		}
		return body + "\n\n" + section, true
	}
	end := start + 1
	for end < len(lines) && !strings.HasPrefix(lines[end], "## ") {
		end++
	}
	insert := end
	for insert > start+1 && strings.TrimSpace(lines[insert-1]) == "" {
		insert--
	}
	if insert == start+1 {
		missing = slices.Insert(missing, 0, "")
	}
	if insert < len(lines) && strings.TrimSpace(lines[insert]) != "" {
		missing = append(missing, "")
	}
	return strings.Join(slices.Insert(lines, insert, missing...), "\n"), true
}

// containerReleaseLink links the combined image manifest, on the git host's web UI when
// the image is in its default container registry.
func containerReleaseLink(cfg config, provider releaseProvider, repository string, tag string) releaseLink {
	link := releaseLink{text: dockerImageName(cfg.containerRegistry, repository, tag)}
	owner, name, ok := strings.Cut(strings.ToLower(repository), "/")
	registry := strings.ToLower(cfg.containerRegistry)
	if !ok || registry == "" || registry != strings.ToLower(defaultContainerRegistry(provider, cfg.gitServerURL)) {
		return link
	}
	switch provider {
	case releaseGitHub:
		// Images pushed from a workflow are linked to its repository.
		repo, err := parseRepository(cfg.gitRepository)
		if err == nil && githubAPIBase(cfg) == "https://api.github.com" {
			link.url = "https://github.com/" + repo.path() + "/pkgs/container/" + url.PathEscape(name)
		}
	case releaseGitea, releaseForgejo:
		link.url = giteaPackageURL(cfg.gitServerURL, owner, "container", name, tag)
	}
	return link
}

// releaseLinks returns links to the published packages, or in a dry run to every package
// that would be published.
func (set *packagePublicationSet) releaseLinks() []releaseLink {
	if set == nil {
		return nil
	}
	var links []releaseLink
	for _, publication := range set.packages {
		if publication.published || set.cfg.dryRun {
			links = append(links, set.releaseLink(publication))
		}
	}
	return links
}

func (set *packagePublicationSet) releaseLink(publication packagePublication) releaseLink {
	name := publication.name
	version := publication.version
	group, artifact, _ := strings.Cut(name, ":")
	link := releaseLink{
		text:     name + "@" + version,
		registry: packageRegistryName(publication.registry, set.provider),
	}
	switch publication.kind {
	case packageMaven:
		link.text = name + ":" + version
	case packageGradle:
		// A Gradle build may publish only subprojects, so link every artifact in its group.
		link.text = group + ":*:" + version
	}

	switch publication.registry {
	case registryHost:
		link.url = set.hostPackageURL(publication)
	case registryCratesIO:
		link.url = "https://crates.io/crates/" + url.PathEscape(name) + "/" + url.PathEscape(version)
	case registryNPMJS:
		link.url = "https://www.npmjs.com/package/" + escapePathSegments(name) + "/v/" + url.PathEscape(version)
	case registryPyPI:
		link.url = "https://pypi.org/project/" + url.PathEscape(normalizePyPIName(name)) + "/" + url.PathEscape(version) + "/"
	case registryMavenCentral:
		if publication.kind == packageGradle {
			link.url = "https://central.sonatype.com/namespace/" + url.PathEscape(group)
		} else {
			link.url = "https://central.sonatype.com/artifact/" + url.PathEscape(group) + "/" + url.PathEscape(artifact) + "/" + url.PathEscape(version)
		}
	case registryPkgGoDev:
		link.url = "https://pkg.go.dev/" + escapePathSegments(name) + "@" + url.PathEscape(version)
	case registryHexPM:
		link.url = "https://hex.pm/packages/" + url.PathEscape(name) + "/" + url.PathEscape(version)
	}
	return link
}

// hostPackageURL returns the git host's web page for a package, or "" when it is unknown.
func (set *packagePublicationSet) hostPackageURL(publication packagePublication) string {
	name := publication.name
	group, artifact, _ := strings.Cut(name, ":")
	owner := set.cfg.packageRegistryOwner
	switch set.provider {
	case releaseGitea, releaseForgejo:
		switch publication.kind {
		case packageGradle:
			return giteaPackagesURL(set.cfg.packageRegistryURL, owner) + "?" + url.Values{"type": {"maven"}, "q": {group}}.Encode()
		case packagePyPI:
			name = normalizePyPIName(name)
		}
		return giteaPackageURL(set.cfg.packageRegistryURL, owner, string(publication.kind), name, publication.version)
	case releaseGitHub:
		// Packages are linked to the repository they are published from, which shares their owner.
		repo, err := parseRepository(set.cfg.gitRepository)
		if err != nil || !strings.EqualFold(repo.owner, owner) || githubAPIBase(set.cfg) != "https://api.github.com" {
			return ""
		}
		base := "https://github.com/" + repo.path() + "/pkgs/"
		switch publication.kind {
		case packageNPM:
			if set.registryURL(packageNPM) != "https://npm.pkg.github.com/" {
				return ""
			}
			_, unscoped, _ := strings.Cut(name, "/")
			return base + "npm/" + url.PathEscape(unscoped)
		case packageMaven:
			if !strings.EqualFold(set.registryURL(packageMaven), "https://maven.pkg.github.com/"+repo.path()+"/") {
				return ""
			}
			return base + "maven/" + url.PathEscape(group+"."+artifact)
		}
	}
	return ""
}

func giteaPackagesURL(server string, owner string) string {
	return strings.TrimRight(server, "/") + "/" + url.PathEscape(owner) + "/-/packages"
}

func giteaPackageURL(server string, owner string, kind string, name string, version string) string {
	return giteaPackagesURL(server, owner) + "/" + url.PathEscape(kind) + "/" + url.PathEscape(name) + "/" + url.PathEscape(version)
}

// escapePathSegments escapes each segment of a slash-separated name, such as a Go module path.
func escapePathSegments(name string) string {
	segments := strings.Split(name, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
