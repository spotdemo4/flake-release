package flakerelease

import (
	"os"
	"strings"
)

func splitPackages(value string) []string {
	if value == "" {
		return nil
	}

	var fields []string
	if strings.Contains(value, "\n") {
		for _, line := range strings.Split(value, "\n") {
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

func tagVersion(tag string) string {
	return strings.TrimPrefix(tag, "v")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
