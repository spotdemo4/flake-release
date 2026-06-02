package flakerelease

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	git "github.com/go-git/go-git/v6"
	gitconfig "github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/plumbing/storer"
)

func gitLatestTag() (string, error) {
	repo, err := openGitRepository()
	if err != nil {
		return "", err
	}
	defer closeGitRepository(repo)

	head, err := repo.Head()
	if err != nil {
		return "", err
	}

	taggedCommits, err := tagNamesByCommit(repo)
	if err != nil {
		return "", err
	}

	commits, err := repo.Log(&git.LogOptions{
		From:  head.Hash(),
		Order: git.LogOrderCommitterTime,
	})
	if err != nil {
		return "", err
	}
	defer commits.Close()

	for {
		commit, err := commits.Next()
		if errors.Is(err, storer.ErrStop) {
			break
		}
		if err != nil {
			return "", err
		}

		names := taggedCommits[commit.Hash]
		if len(names) == 0 {
			continue
		}
		sortVersionTags(names)
		return names[len(names)-1], nil
	}

	return "", errors.New("no tags found")
}

func gitUser() (string, error) {
	repo, err := openGitRepository()
	if err != nil {
		return "", err
	}
	defer closeGitRepository(repo)

	cfg, err := repo.Config()
	if err != nil {
		return "", err
	}
	if cfg.User.Name != "" {
		return cfg.User.Name, nil
	}

	for _, path := range gitGlobalConfigPaths() {
		name, err := gitConfigUserName(path)
		if err != nil {
			warn("could not read global git config %q: %v", path, err)
			continue
		}
		if name != "" {
			return name, nil
		}
	}

	for _, path := range gitSystemConfigPaths() {
		name, err := gitConfigUserName(path)
		if err != nil {
			warn("could not read system git config %q: %v", path, err)
			continue
		}
		if name != "" {
			return name, nil
		}
	}

	warn("git user.name is not set in local, global, or system git config")
	return "", nil
}

func gitConfigUserName(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer file.Close()

	cfg, err := gitconfig.ReadConfig(file)
	if err != nil {
		return "", err
	}
	return cfg.User.Name, nil
}

func gitGlobalConfigPaths() []string {
	if path, ok := os.LookupEnv("GIT_CONFIG_GLOBAL"); ok {
		if path == "" {
			return nil
		}
		return []string{path}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	paths := []string{filepath.Join(home, ".gitconfig")}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "git", "config"))
	} else {
		paths = append(paths, filepath.Join(home, ".config", "git", "config"))
	}
	return paths
}

func gitSystemConfigPaths() []string {
	if gitConfigNoSystem() {
		return nil
	}
	if path, ok := os.LookupEnv("GIT_CONFIG_SYSTEM"); ok {
		if path == "" {
			return nil
		}
		return []string{path}
	}

	if runtime.GOOS == "windows" {
		if path := os.Getenv("PROGRAMFILES"); path != "" {
			return []string{filepath.Join(path, "Git", "etc", "gitconfig")}
		}
		return nil
	}
	return []string{"/etc/gitconfig"}
}

func gitConfigNoSystem() bool {
	switch strings.ToLower(os.Getenv("GIT_CONFIG_NOSYSTEM")) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func gitOrigin() (string, error) {
	repo, err := openGitRepository()
	if err != nil {
		return "", err
	}
	defer closeGitRepository(repo)

	remote, err := repo.Remote("origin")
	if err != nil {
		return "", err
	}

	urls := remote.Config().URLs
	if len(urls) == 0 {
		return "", errors.New("origin remote has no URLs")
	}
	return urls[0], nil
}

func gitRepositoryFromOrigin(origin string) string {
	path := gitOriginPath(origin)
	path = strings.Trim(strings.TrimSpace(path), "/")
	path = strings.TrimSuffix(path, ".git")
	if path == "" {
		return ""
	}

	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ""
	}

	repository := parts[len(parts)-2] + "/" + parts[len(parts)-1]
	if _, err := parseRepository(repository); err != nil {
		return ""
	}
	return repository
}

func gitServerURLFromOrigin(origin string) string {
	if parsed, err := url.Parse(origin); err == nil && parsed.Scheme != "" {
		if parsed.Scheme == "file" || parsed.Host == "" {
			return ""
		}
		switch parsed.Scheme {
		case "http", "https":
			return parsed.Scheme + "://" + parsed.Host
		default:
			if parsed.Hostname() == "" {
				return ""
			}
			return "https://" + parsed.Hostname()
		}
	}

	host := gitOriginSCPHost(origin)
	if host == "" {
		return ""
	}
	return "https://" + host
}

func gitOriginPath(origin string) string {
	if parsed, err := url.Parse(origin); err == nil && parsed.Scheme != "" {
		if parsed.Scheme == "file" || parsed.Host == "" {
			return ""
		}
		return parsed.Path
	}

	_, path, ok := strings.Cut(origin, ":")
	if ok && gitOriginSCPHost(origin) != "" {
		return path
	}
	return ""
}

func gitOriginSCPHost(origin string) string {
	prefix, _, ok := strings.Cut(origin, ":")
	if !ok || strings.Contains(prefix, "/") {
		return ""
	}

	host := prefix
	if _, value, ok := strings.Cut(host, "@"); ok {
		host = value
	}
	return host
}

func gitChangelog(tag string) (string, error) {
	repo, err := openGitRepository()
	if err != nil {
		return "", err
	}
	defer closeGitRepository(repo)

	file, err := os.CreateTemp("", "flake-release-changelog-*")
	if err != nil {
		return "", err
	}
	_ = file.Close()

	lastTag, err := previousTag(repo, tag)
	if err != nil {
		return "", err
	}

	log, err := changelog(repo, lastTag, tag)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(file.Name(), []byte(sortChangelog(log)), 0o600); err != nil {
		return "", err
	}

	return file.Name(), nil
}

func openGitRepository() (*git.Repository, error) {
	return git.PlainOpenWithOptions(".", &git.PlainOpenOptions{
		DetectDotGit: true,
	})
}

func closeGitRepository(repo *git.Repository) {
	_ = repo.Close()
}

func previousTag(repo *git.Repository, tag string) (string, error) {
	tags, err := tagNames(repo)
	if err != nil {
		return "", err
	}
	sortVersionTags(tags)

	for i, candidate := range tags {
		if candidate != tag {
			continue
		}
		if i > 0 {
			return tags[i-1], nil
		}
		return candidate, nil
	}

	root, err := rootCommit(repo)
	if err != nil {
		return "", err
	}
	return root.String(), nil
}

func changelog(repo *git.Repository, from string, to string) (string, error) {
	fromHash, err := revisionCommitHash(repo, from)
	if err != nil {
		return "", err
	}
	toHash, err := revisionCommitHash(repo, to)
	if err != nil {
		return "", err
	}

	if fromHash == toHash {
		return "", nil
	}

	excluded, err := reachableCommits(repo, fromHash)
	if err != nil {
		return "", err
	}

	commits, err := commitHistory(repo, toHash)
	if err != nil {
		return "", err
	}

	var lines []string
	for _, commit := range commits {
		if excluded[commit.Hash] {
			continue
		}

		subject := strings.SplitN(commit.Message, "\n", 2)[0]
		lines = append(lines, "* "+subject+" ("+commit.Hash.String()+")")
	}

	return strings.Join(lines, "\n"), nil
}

func reachableCommits(repo *git.Repository, from plumbing.Hash) (map[plumbing.Hash]bool, error) {
	commits, err := commitHistory(repo, from)
	if err != nil {
		return nil, err
	}

	hashes := map[plumbing.Hash]bool{}
	for _, commit := range commits {
		hashes[commit.Hash] = true
	}
	return hashes, nil
}

func commitHistory(repo *git.Repository, from plumbing.Hash) ([]*object.Commit, error) {
	pending := []plumbing.Hash{from}
	seen := map[plumbing.Hash]bool{}
	var commits []*object.Commit

	for len(pending) > 0 {
		hash := pending[0]
		pending = pending[1:]
		if seen[hash] {
			continue
		}
		seen[hash] = true

		commit, err := repo.CommitObject(hash)
		if err != nil {
			return nil, err
		}
		commits = append(commits, commit)
		pending = append(pending, commit.ParentHashes...)
	}

	sort.SliceStable(commits, func(i, j int) bool {
		left := commits[i]
		right := commits[j]
		if left.Committer.When.Equal(right.Committer.When) {
			return left.Hash.String() < right.Hash.String()
		}
		return left.Committer.When.After(right.Committer.When)
	})
	return commits, nil
}

func rootCommit(repo *git.Repository) (plumbing.Hash, error) {
	head, err := repo.Head()
	if err != nil {
		return plumbing.ZeroHash, err
	}

	commits, err := repo.Log(&git.LogOptions{
		From:  head.Hash(),
		Order: git.LogOrderCommitterTime,
	})
	if err != nil {
		return plumbing.ZeroHash, err
	}
	defer commits.Close()

	var root plumbing.Hash
	for {
		commit, err := commits.Next()
		if errors.Is(err, storer.ErrStop) {
			break
		}
		if err != nil {
			return plumbing.ZeroHash, err
		}
		if commit.NumParents() == 0 {
			root = commit.Hash
		}
	}
	if root == plumbing.ZeroHash {
		return plumbing.ZeroHash, errors.New("no root commit found")
	}
	return root, nil
}

func tagNames(repo *git.Repository) ([]string, error) {
	tags, err := repo.Tags()
	if err != nil {
		return nil, err
	}
	defer tags.Close()

	var names []string
	if err := tags.ForEach(func(ref *plumbing.Reference) error {
		names = append(names, ref.Name().Short())
		return nil
	}); err != nil {
		return nil, err
	}
	return names, nil
}

func tagNamesByCommit(repo *git.Repository) (map[plumbing.Hash][]string, error) {
	tags, err := repo.Tags()
	if err != nil {
		return nil, err
	}
	defer tags.Close()

	tagged := map[plumbing.Hash][]string{}
	if err := tags.ForEach(func(ref *plumbing.Reference) error {
		hash, err := tagCommitHash(repo, ref)
		if err != nil {
			return err
		}
		tagged[hash] = append(tagged[hash], ref.Name().Short())
		return nil
	}); err != nil {
		return nil, err
	}
	return tagged, nil
}

func revisionCommitHash(repo *git.Repository, revision string) (plumbing.Hash, error) {
	if hash := plumbing.NewHash(revision); !hash.IsZero() {
		if _, err := repo.CommitObject(hash); err == nil {
			return hash, nil
		}
	}

	hash, err := repo.ResolveRevision(plumbing.Revision(revision))
	if err == nil {
		return objectCommitHash(repo, *hash)
	}

	ref, refErr := repo.Tag(revision)
	if refErr != nil {
		return plumbing.ZeroHash, err
	}
	return tagCommitHash(repo, ref)
}

func tagCommitHash(repo *git.Repository, ref *plumbing.Reference) (plumbing.Hash, error) {
	return objectCommitHash(repo, ref.Hash())
}

func objectCommitHash(repo *git.Repository, hash plumbing.Hash) (plumbing.Hash, error) {
	obj, err := repo.Object(plumbing.AnyObject, hash)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	switch obj := obj.(type) {
	case *object.Commit:
		return obj.Hash, nil
	case *object.Tag:
		commit, err := obj.Commit()
		if err != nil {
			return plumbing.ZeroHash, err
		}
		return commit.Hash, nil
	default:
		return plumbing.ZeroHash, errors.New("revision does not point to a commit")
	}
}

func sortVersionTags(tags []string) {
	sort.Slice(tags, func(i, j int) bool {
		return versionLess(tags[i], tags[j])
	})
}

func versionLess(left string, right string) bool {
	leftParts := versionParts(left)
	rightParts := versionParts(right)
	limit := max(len(rightParts), len(leftParts))

	for i := range limit {
		if i >= len(leftParts) {
			return true
		}
		if i >= len(rightParts) {
			return false
		}

		leftPart := leftParts[i]
		rightPart := rightParts[i]
		leftNum, leftErr := strconv.Atoi(leftPart)
		rightNum, rightErr := strconv.Atoi(rightPart)
		if leftErr == nil && rightErr == nil {
			if leftNum != rightNum {
				return leftNum < rightNum
			}
			continue
		}
		if leftPart != rightPart {
			return leftPart < rightPart
		}
	}

	return false
}

func versionParts(value string) []string {
	value = strings.TrimPrefix(value, "refs/tags/")
	value = strings.TrimPrefix(value, "v")

	var parts []string
	var current strings.Builder
	var currentDigit bool
	for i, r := range value {
		digit := r >= '0' && r <= '9'
		if i > 0 && (digit != currentDigit || (!digit && (r == '.' || r == '-' || r == '_' || r == '/'))) {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
			currentDigit = digit
			if !digit && (r == '.' || r == '-' || r == '_' || r == '/') {
				continue
			}
		}
		currentDigit = digit
		if !digit && (r == '.' || r == '-' || r == '_' || r == '/') {
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func sortChangelog(log string) string {
	featRE := regexp.MustCompile(`^\* feat(\(.*\))?!?:`)
	fixRE := regexp.MustCompile(`^\* fix(\(.*\))?!?:`)

	var feat, fix, other []string
	for _, line := range splitLines(log) {
		switch {
		case featRE.MatchString(line):
			feat = append(feat, line)
		case fixRE.MatchString(line):
			fix = append(fix, line)
		default:
			other = append(other, line)
		}
	}

	ordered := append(feat, fix...)
	ordered = append(ordered, other...)
	if len(ordered) == 0 {
		return ""
	}
	return strings.Join(ordered, "\n") + "\n"
}

func splitLines(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(value, "\n"), "\n")
}
