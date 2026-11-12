package flakerelease

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// releaseGroup is a release tag and the packages whose version it releases.
type releaseGroup struct {
	tag      releaseTag
	packages []releasePackagePlan
}

// releaseTagHint returns the tag named by TAG or a CI tag event, or "" when neither
// names one. Branch and other non-tag events give no hint, so every package is
// released to the tag for its own version.
func releaseTagHint() (string, error) {
	if tag := os.Getenv("TAG"); tag != "" {
		return tag, nil
	}

	refName := os.Getenv("GITHUB_REF_NAME")
	ref := os.Getenv("GITHUB_REF")
	if !strings.HasPrefix(ref, "refs/tags/") {
		return "", nil
	}
	if refName == "" || ref != "refs/tags/"+refName {
		return "", fmt.Errorf("release event must identify one exact tag; got GITHUB_REF_NAME=%q GITHUB_REF=%q", refName, ref)
	}
	return refName, nil
}

// packageTagVersions returns the versions a package can be released under: its
// version, and its image tag for container images.
func packageTagVersions(plan releasePackagePlan) []string {
	var versions []string
	for _, version := range []string{plan.version, plan.imageTag} {
		if version != "" && !slices.Contains(versions, version) {
			versions = append(versions, version)
		}
	}
	return versions
}

// resolvePackageTag finds the release tag for a package from its version. A non-empty
// skip reason means the package has no release tag and is not released; an error means
// its tag is ambiguous.
//
// With a hint, only packages whose version matches the hint are released to it.
// Otherwise the package is released to the tag in tags whose version matches its own;
// when several do, the tag whose namespace ends in the package's pname is chosen.
func resolvePackageTag(plan releasePackagePlan, hint *releaseTag, tags []releaseTag) (releaseTag, string, error) {
	versions := packageTagVersions(plan)
	if len(versions) == 0 {
		return releaseTag{}, "package has no version", nil
	}
	quoted := quoteVersions(versions)

	if hint != nil {
		if slices.Contains(versions, hint.version) {
			return *hint, "", nil
		}
		return releaseTag{}, fmt.Sprintf("version %s does not match git tag %q", quoted, hint.full), nil
	}

	var candidates []releaseTag
	for _, tag := range tags {
		if slices.Contains(versions, tag.version) {
			candidates = append(candidates, tag)
		}
	}
	switch len(candidates) {
	case 0:
		return releaseTag{}, fmt.Sprintf("no git tag found for version %s", quoted), nil
	case 1:
		return candidates[0], "", nil
	}

	if plan.pname != "" {
		var named []releaseTag
		for _, tag := range candidates {
			if namespaceName(tag.namespace) == plan.pname {
				named = append(named, tag)
			}
		}
		if len(named) == 1 {
			return named[0], "", nil
		}
	}

	names := make([]string, 0, len(candidates))
	for _, tag := range candidates {
		names = append(names, tag.full)
	}
	slices.Sort(names)
	return releaseTag{}, "", fmt.Errorf("%s version %s matches multiple git tags (%s); set TAG to choose one", plan.pkg, quoted, strings.Join(names, ", "))
}

// groupReleasePackages resolves each package's release tag and groups the packages by
// tag, in the order their tags are first resolved.
func groupReleasePackages(plans []releasePackagePlan, hint *releaseTag, tags []releaseTag) ([]releaseGroup, error) {
	section("Matching release tags")
	var groups []releaseGroup
	var errs error
	for _, plan := range plans {
		item("%s", plan.pkg)
		tag, skip, err := resolvePackageTag(plan, hint, tags)
		if err != nil {
			itemWarn("%v", err)
			errs = errors.Join(errs, err)
			continue
		}
		if skip != "" {
			itemWarn("%s; skipping", skip)
			continue
		}
		detail("tag: %s", tag.full)

		index := slices.IndexFunc(groups, func(group releaseGroup) bool { return group.tag.full == tag.full })
		if index < 0 {
			groups = append(groups, releaseGroup{tag: tag})
			index = len(groups) - 1
		}
		groups[index].packages = append(groups[index].packages, plan)
	}
	if errs != nil {
		return nil, errs
	}
	return groups, nil
}

// namespaceName returns the last path segment of a tag namespace.
func namespaceName(namespace string) string {
	return namespace[strings.LastIndexByte(namespace, '/')+1:]
}

func quoteVersions(versions []string) string {
	quoted := make([]string, 0, len(versions))
	for _, version := range versions {
		quoted = append(quoted, fmt.Sprintf("%q", version))
	}
	return strings.Join(quoted, " or ")
}
