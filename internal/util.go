package flakerelease

import (
	"fmt"
	"os"
	"strings"

	"github.com/go-git/go-git/v6/plumbing"
)

func splitPackages(value string) []string {
	if value == "" {
		return nil
	}

	var fields []string
	if strings.Contains(value, "\n") {
		for line := range strings.SplitSeq(value, "\n") {
			fields = append(fields, strings.TrimRight(line, "\r"))
		}
	} else {
		fields = strings.Split(value, " ")
	}

	packages := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			packages = append(packages, field)
		}
	}
	return packages
}

func truthy(value string) bool {
	switch strings.ToLower(value) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func deletePath(path string) {
	_ = os.RemoveAll(path)
}

type releaseTag struct {
	full       string
	namespace  string
	versionTag string
	version    string
}

func parseReleaseTag(tag string) releaseTag {
	namespace := ""
	versionTag := tag
	if separator := strings.LastIndexByte(tag, '/'); separator >= 0 {
		namespace = tag[:separator]
		versionTag = tag[separator+1:]
	}

	return releaseTag{
		full:       tag,
		namespace:  namespace,
		versionTag: versionTag,
		version:    strings.TrimPrefix(versionTag, "v"),
	}
}

func parseSelectedReleaseTag(value string) (releaseTag, error) {
	tag := parseReleaseTag(value)
	if tag.full == "" {
		return releaseTag{}, fmt.Errorf("release tag must not be empty; set TAG to a complete release tag")
	}
	if strings.HasPrefix(tag.full, "refs/") {
		return releaseTag{}, fmt.Errorf("release tag %q must be a short tag name, not a full Git ref", tag.full)
	}
	if err := plumbing.NewTagReferenceName(tag.full).Validate(); err != nil {
		return releaseTag{}, fmt.Errorf("invalid release tag %q: %w", tag.full, err)
	}
	if tag.versionTag == "" || tag.version == "" {
		return releaseTag{}, fmt.Errorf("release tag %q has an empty version component", tag.full)
	}
	return tag, nil
}

func tagVersion(tag string) string {
	return parseReleaseTag(tag).version
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
