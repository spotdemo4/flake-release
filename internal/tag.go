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

// tagHint is the tag named by TAG or a CI tag event.
type tagHint struct {
	tag releaseTag
	// filter is set for an unscoped CI tag, which only selects the packages whose version
	// matches it, or that have their own scoped tag on HEAD. Each of them is released to
	// its own tag among those on HEAD, with the CI tag as the fallback for packages whose
	// version matches it, so one base tag can trigger a monorepo's scoped releases, even
	// for packages versioned apart from it.
	filter bool
}

// releaseTagHint returns the tag named by TAG or a CI tag event, or "" when neither
// names one, and whether it came from the CI event. Branch and other non-tag events
// give no hint, so every package is released to the tag for its own version.
func releaseTagHint() (string, bool, error) {
	if tag := os.Getenv("TAG"); tag != "" {
		return tag, false, nil
	}

	refName := os.Getenv("GITHUB_REF_NAME")
	ref := os.Getenv("GITHUB_REF")
	if !strings.HasPrefix(ref, "refs/tags/") {
		return "", false, nil
	}
	if refName == "" || ref != "refs/tags/"+refName {
		return "", false, fmt.Errorf("release event must identify one exact tag; got GITHUB_REF_NAME=%q GITHUB_REF=%q", refName, ref)
	}
	return refName, true, nil
}

// newTagHint parses the tag hint. Only an unscoped CI tag filters by version; TAG and
// scoped CI tags release every package whose version matches to that tag.
func newTagHint(name string, fromCI bool) (*tagHint, error) {
	if name == "" {
		return nil, nil
	}
	tag, err := parseSelectedReleaseTag(name)
	if err != nil {
		return nil, err
	}
	return &tagHint{tag: tag, filter: fromCI && tag.namespace == ""}, nil
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

// packageSourcePath returns a package's source directory relative to the root of its
// src, or "" when it is unknown or the root itself.
type packageSourcePath func(releasePackagePlan) string

// resolvePackageTag finds the release tag for a package from its version. A non-empty
// skip reason means the package has no release tag and is not released; an error means
// its tag is ambiguous.
//
// With a hint from TAG or a scoped CI tag, only packages whose version matches the hint
// are released to it. Otherwise the package is released to the tag in tags whose version
// matches its own. An unscoped CI tag is the fallback when no scoped tag matches, and a
// package whose version differs from it is only released to a scoped tag in tags for
// its source directory, so it is released only when its own tag was created with the CI
// tag. When several tags match, the one whose namespace contains the package's source
// directory is chosen, then the one whose namespace ends in the package's pname.
func resolvePackageTag(plan releasePackagePlan, hint *tagHint, tags []releaseTag, sourcePath packageSourcePath) (releaseTag, string, error) {
	versions := packageTagVersions(plan)
	if len(versions) == 0 {
		return releaseTag{}, "package has no version", nil
	}
	quoted := quoteVersions(versions)

	var fallback *releaseTag
	if hint != nil {
		if !slices.Contains(versions, hint.tag.version) {
			if hint.filter {
				if tag, ok := sourcePathTag(plan, versions, tags, sourcePath); ok {
					return tag, "", nil
				}
				return releaseTag{}, fmt.Sprintf("version %s does not match git tag %q or a tag on HEAD for its source directory", quoted, hint.tag.full), nil
			}
			return releaseTag{}, fmt.Sprintf("version %s does not match git tag %q", quoted, hint.tag.full), nil
		}
		if !hint.filter {
			return hint.tag, "", nil
		}
		versions = []string{hint.tag.version}
		quoted = quoteVersions(versions)
		fallback = &hint.tag
	}

	var candidates []releaseTag
	for _, tag := range tags {
		if slices.Contains(versions, tag.version) {
			candidates = append(candidates, tag)
		}
	}
	if fallback != nil && !slices.ContainsFunc(candidates, func(tag releaseTag) bool { return tag.full == fallback.full }) {
		candidates = append(candidates, *fallback)
	}
	switch len(candidates) {
	case 0:
		return releaseTag{}, fmt.Sprintf("no git tag found for version %s", quoted), nil
	case 1:
		return candidates[0], "", nil
	}

	if sourcePath != nil {
		if tag, ok := tagContainingPath(candidates, sourcePath(plan)); ok {
			return tag, "", nil
		}
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
	if fallback != nil {
		return *fallback, "", nil
	}

	names := make([]string, 0, len(candidates))
	for _, tag := range candidates {
		names = append(names, tag.full)
	}
	slices.Sort(names)
	return releaseTag{}, "", fmt.Errorf("%s version %s matches multiple git tags (%s); set TAG to choose one", plan.pkg, quoted, strings.Join(names, ", "))
}

// sourcePathTag returns the tag in tags with one of versions whose namespace is the
// package's source directory or the nearest directory above it. The pname is not
// considered, so an unrelated tag that happens to share a version can't select it.
func sourcePathTag(plan releasePackagePlan, versions []string, tags []releaseTag, sourcePath packageSourcePath) (releaseTag, bool) {
	if sourcePath == nil {
		return releaseTag{}, false
	}
	var candidates []releaseTag
	for _, tag := range tags {
		if tag.namespace != "" && slices.Contains(versions, tag.version) {
			candidates = append(candidates, tag)
		}
	}
	if len(candidates) == 0 {
		return releaseTag{}, false
	}
	return tagContainingPath(candidates, sourcePath(plan))
}

// releasedTagForPath returns the tag in tags with version for the directory at path, a
// repository path that is "" for the root: the tag whose namespace is path or the
// nearest directory above it, as in tag resolution, or an unscoped tag for the root.
func releasedTagForPath(tags []releaseTag, version string, path string) (releaseTag, bool) {
	var candidates []releaseTag
	for _, tag := range tags {
		if tag.version == version {
			candidates = append(candidates, tag)
		}
	}
	if path == "" {
		for _, tag := range candidates {
			if tag.namespace == "" {
				return tag, true
			}
		}
		return releaseTag{}, false
	}
	return tagContainingPath(candidates, path)
}

// tagContainingPath returns the tag whose namespace is the longest one that equals path
// or is a directory above it. Unscoped tags never match, since their namespace says
// nothing about where a package lives.
func tagContainingPath(tags []releaseTag, path string) (releaseTag, bool) {
	path = strings.Trim(path, "/")
	if path == "" {
		return releaseTag{}, false
	}
	var best []releaseTag
	for _, tag := range tags {
		if tag.namespace == "" || (path != tag.namespace && !strings.HasPrefix(path, tag.namespace+"/")) {
			continue
		}
		switch {
		case len(best) == 0 || len(tag.namespace) > len(best[0].namespace):
			best = []releaseTag{tag}
		case len(tag.namespace) == len(best[0].namespace):
			best = append(best, tag)
		}
	}
	if len(best) != 1 {
		return releaseTag{}, false
	}
	return best[0], true
}

// groupReleasePackages resolves each package's release tag and groups the packages by
// tag, in the order their tags are first resolved.
func groupReleasePackages(plans []releasePackagePlan, hint *tagHint, tags []releaseTag, sourcePath packageSourcePath) ([]releaseGroup, error) {
	section("Matching release tags")
	var groups []releaseGroup
	var errs error
	for _, plan := range plans {
		item("%s", plan.pkg)
		tag, skip, err := resolvePackageTag(plan, hint, tags, sourcePath)
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
