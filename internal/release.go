package flakerelease

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type releaseProvider string

const (
	releaseGitHub  releaseProvider = "github"
	releaseGitea   releaseProvider = "gitea"
	releaseForgejo releaseProvider = "forgejo"
)

func releaseType(origin string) (releaseProvider, error) {
	switch os.Getenv("GIT_TYPE") {
	case string(releaseForgejo):
		return releaseForgejo, nil
	case string(releaseGitea):
		return releaseGitea, nil
	case string(releaseGitHub):
		return releaseGitHub, nil
	}

	switch {
	case os.Getenv("FORGEJO_ACTIONS") != "":
		return releaseForgejo, nil
	case os.Getenv("GITEA_ACTIONS") != "":
		return releaseGitea, nil
	case os.Getenv("GITHUB_ACTIONS") != "":
		return releaseGitHub, nil
	}

	switch {
	case strings.Contains(origin, "forgejo"):
		return releaseForgejo, nil
	case strings.Contains(origin, "gitea"):
		return releaseGitea, nil
	case strings.Contains(origin, "github"):
		return releaseGitHub, nil
	default:
		warn("unknown release type")
		return "", fmt.Errorf("unknown release type")
	}
}

func login(run runner, provider releaseProvider, cfg config) error {
	switch provider {
	case releaseGitea:
		return giteaLogin(run, cfg)
	case releaseForgejo:
		return forgejoLogin(run, cfg)
	default:
		return nil
	}
}

func logout(run runner, provider releaseProvider, cfg config) {
	switch provider {
	case releaseGitea:
		if err := giteaLogout(run, cfg); err != nil {
			warn("%v", err)
		}
	case releaseForgejo:
		if err := forgejoLogout(run, cfg); err != nil {
			warn("%v", err)
		}
	}
}

func createRelease(run runner, provider releaseProvider, cfg config, tag string, changelog string) error {
	switch provider {
	case releaseForgejo:
		return forgejoRelease(run, cfg, tag, changelog)
	case releaseGitea:
		return giteaRelease(run, cfg, tag, changelog)
	case releaseGitHub:
		return githubRelease(run, cfg, tag, changelog)
	default:
		return nil
	}
}

func uploadReleaseAsset(run runner, provider releaseProvider, cfg config, tag string, asset string) error {
	switch provider {
	case releaseForgejo:
		return forgejoReleaseAsset(run, cfg, tag, asset)
	case releaseGitea:
		return giteaReleaseAsset(run, cfg, tag, asset)
	case releaseGitHub:
		return githubReleaseAsset(run, cfg, tag, asset)
	default:
		return nil
	}
}

func cleanupReleaseAssets(run runner, provider releaseProvider, cfg config, tag string) error {
	switch provider {
	case releaseForgejo, releaseGitea:
		return giteaAPIReleaseCleanupAssets(run, provider, cfg, tag)
	case releaseGitHub:
		return githubReleaseCleanupAssets(run, cfg, tag)
	default:
		return nil
	}
}

func githubRelease(run runner, cfg config, tag string, changelog string) error {
	if cfg.githubRepository == "" {
		warn("GITHUB_REPOSITORY is not set, cannot create GitHub release")
		return fmt.Errorf("GITHUB_REPOSITORY is not set")
	}
	if cfg.githubToken == "" {
		warn("GITHUB_TOKEN is not set, cannot create GitHub release")
		return fmt.Errorf("GITHUB_TOKEN is not set")
	}

	info("creating release %s at %s", tag, cfg.githubRepository)
	return run.run("gh", "release", "create", "--title", tag, "--notes-file", changelog, "--repo", cfg.githubRepository, tag)
}

func githubReleaseAsset(run runner, cfg config, tag string, asset string) error {
	if cfg.githubRepository == "" {
		warn("GITHUB_REPOSITORY is not set, cannot upload asset to GitHub")
		return fmt.Errorf("GITHUB_REPOSITORY is not set")
	}
	if cfg.githubToken == "" {
		warn("GITHUB_TOKEN is not set, cannot upload asset to GitHub")
		return fmt.Errorf("GITHUB_TOKEN is not set")
	}

	info("uploading asset to release %s at %s", tag, cfg.githubRepository)
	return run.run("gh", "release", "upload", "--repo", cfg.githubRepository, tag, asset)
}

func githubReleaseCleanupAssets(run runner, cfg config, currentTag string) error {
	if cfg.githubRepository == "" {
		warn("GITHUB_REPOSITORY is not set, cannot delete old GitHub release assets")
		return fmt.Errorf("GITHUB_REPOSITORY is not set")
	}
	if cfg.githubToken == "" {
		warn("GITHUB_TOKEN is not set, cannot delete old GitHub release assets")
		return fmt.Errorf("GITHUB_TOKEN is not set")
	}

	releases, err := run.capture("gh", "release", "list", "--repo", cfg.githubRepository, "--limit", "1000", "--json", "tagName", "--jq", ".[].tagName")
	if err != nil {
		warn("failed to fetch GitHub releases")
		return err
	}

	info("deleting old GitHub release assets at %s", cfg.githubRepository)
	if releases == "" {
		return nil
	}

	failed := false
	for _, releaseTag := range splitLines(releases) {
		if releaseTag == "" || releaseTag == currentTag {
			continue
		}

		assets, err := run.capture("gh", "release", "view", releaseTag, "--repo", cfg.githubRepository, "--json", "assets", "--jq", ".assets[].name")
		if err != nil {
			warn("failed to fetch GitHub release assets for %s", releaseTag)
			failed = true
			continue
		}
		if assets == "" {
			continue
		}

		for _, asset := range splitLines(assets) {
			if asset == "" {
				continue
			}
			info("deleting asset %s from release %s", asset, releaseTag)
			if err := run.run("gh", "release", "delete-asset", "--repo", cfg.githubRepository, releaseTag, asset, "--yes"); err != nil {
				warn("failed to delete asset %s from release %s", asset, releaseTag)
				failed = true
			}
		}
	}

	if failed {
		return fmt.Errorf("failed to delete some GitHub release assets")
	}
	return nil
}

func giteaLogin(run runner, cfg config) error {
	if cfg.githubServerURL == "" {
		warn("GITHUB_SERVER_URL is not set, cannot login to Gitea")
		return fmt.Errorf("GITHUB_SERVER_URL is not set")
	}
	if cfg.githubToken == "" {
		warn("GITHUB_TOKEN is not set, cannot login to Gitea")
		return fmt.Errorf("GITHUB_TOKEN is not set")
	}
	if cfg.githubActor == "" {
		warn("GITHUB_ACTOR is not set, cannot login to Gitea")
		return fmt.Errorf("GITHUB_ACTOR is not set")
	}

	info("logging in to %s", cfg.githubServerURL)
	_ = run.run("tea", "login", "add", "--name", cfg.githubActor, "--url", cfg.githubServerURL, "--token", cfg.githubToken)
	return run.run("tea", "login", "default", cfg.githubActor)
}

func giteaLogout(run runner, cfg config) error {
	if cfg.githubActor == "" {
		return fmt.Errorf("GITHUB_ACTOR is not set, cannot logout of Gitea")
	}

	info("logging out of Gitea")
	return run.run("tea", "login", "delete", cfg.githubActor)
}

func giteaRelease(run runner, cfg config, tag string, changelog string) error {
	if cfg.githubRepository == "" {
		warn("GITHUB_REPOSITORY is not set, cannot create Gitea release")
		return fmt.Errorf("GITHUB_REPOSITORY is not set")
	}

	info("creating release %s at %s", tag, cfg.githubRepository)
	return run.run("tea", "release", "create", "--title", tag, "--note-file", changelog, "--repo", cfg.githubRepository, tag)
}

func giteaReleaseAsset(run runner, cfg config, tag string, asset string) error {
	if cfg.githubRepository == "" {
		warn("GITHUB_REPOSITORY is not set, cannot upload asset to Gitea")
		return fmt.Errorf("GITHUB_REPOSITORY is not set")
	}

	info("uploading asset to release %s at %s", tag, cfg.githubRepository)
	return run.run("tea", "release", "assets", "create", "--repo", cfg.githubRepository, tag, asset)
}

func forgejoLogin(run runner, cfg config) error {
	if cfg.githubServerURL == "" {
		warn("GITHUB_SERVER_URL is not set, cannot login to Forgejo")
		return fmt.Errorf("GITHUB_SERVER_URL is not set")
	}
	if cfg.githubActor == "" {
		warn("GITHUB_ACTOR is not set, cannot login to Forgejo")
		return fmt.Errorf("GITHUB_ACTOR is not set")
	}
	if cfg.githubToken == "" {
		warn("GITHUB_TOKEN is not set, cannot login to Forgejo")
		return fmt.Errorf("GITHUB_TOKEN is not set")
	}

	info("logging in to %s", cfg.githubServerURL)
	return run.run("fj", "--host", cfg.githubServerURL, "auth", "add-key", cfg.githubActor, cfg.githubToken)
}

func forgejoLogout(run runner, cfg config) error {
	if cfg.githubServerURL == "" {
		return fmt.Errorf("GITHUB_SERVER_URL is not set, cannot logout of Forgejo")
	}

	domain := strings.TrimPrefix(strings.TrimPrefix(cfg.githubServerURL, "https://"), "http://")
	info("logging out of Forgejo")
	return run.run("fj", "--host", cfg.githubServerURL, "auth", "logout", domain)
}

func forgejoRelease(run runner, cfg config, tag string, changelog string) error {
	if cfg.githubServerURL == "" {
		warn("GITHUB_SERVER_URL is not set, cannot create Forgejo release")
		return fmt.Errorf("GITHUB_SERVER_URL is not set")
	}
	if cfg.githubRepository == "" {
		warn("GITHUB_REPOSITORY is not set, cannot create Forgejo release")
		return fmt.Errorf("GITHUB_REPOSITORY is not set")
	}

	body, err := os.ReadFile(changelog)
	if err != nil {
		return err
	}

	info("creating release %s at %s", tag, cfg.githubRepository)
	return run.run("fj", "--host", cfg.githubServerURL, "release", "create", "--tag", tag, "--body", string(body), "--repo", cfg.githubRepository, tag)
}

func forgejoReleaseAsset(run runner, cfg config, tag string, asset string) error {
	if cfg.githubServerURL == "" {
		warn("GITHUB_SERVER_URL is not set, cannot upload asset to Forgejo")
		return fmt.Errorf("GITHUB_SERVER_URL is not set")
	}
	if cfg.githubRepository == "" {
		warn("GITHUB_REPOSITORY is not set, cannot upload asset to Forgejo")
		return fmt.Errorf("GITHUB_REPOSITORY is not set")
	}

	info("uploading asset to release %s at %s", tag, cfg.githubRepository)
	return run.run("fj", "--host", cfg.githubServerURL, "release", "asset", "create", "--repo", cfg.githubRepository, tag, asset)
}

type giteaReleaseResponse struct {
	ID         int64  `json:"id"`
	TagName    string `json:"tag_name"`
	TagNameAlt string `json:"tagName"`
	Tag        string `json:"tag"`
	Assets     []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"assets"`
}

func (r giteaReleaseResponse) tagName() string {
	return firstNonEmpty(r.TagName, r.TagNameAlt, r.Tag)
}

func giteaAPIReleaseCleanupAssets(run runner, provider releaseProvider, cfg config, currentTag string) error {
	name := strings.ToUpper(string(provider[:1])) + string(provider[1:])
	if cfg.githubServerURL == "" {
		warn("GITHUB_SERVER_URL is not set, cannot delete old %s release assets", name)
		return fmt.Errorf("GITHUB_SERVER_URL is not set")
	}
	if cfg.githubRepository == "" {
		warn("GITHUB_REPOSITORY is not set, cannot delete old %s release assets", name)
		return fmt.Errorf("GITHUB_REPOSITORY is not set")
	}
	if cfg.githubToken == "" {
		warn("GITHUB_TOKEN is not set, cannot delete old %s release assets", name)
		return fmt.Errorf("GITHUB_TOKEN is not set")
	}

	server := strings.TrimRight(cfg.githubServerURL, "/")
	page := 1
	limit := 100
	failed := false

	info("deleting old %s release assets at %s", name, cfg.githubRepository)
	for {
		url := fmt.Sprintf("%s/api/v1/repos/%s/releases?page=%d&limit=%d", server, cfg.githubRepository, page, limit)
		body, err := curl(run, "GET", cfg.githubToken, url)
		if err != nil {
			warn("failed to fetch %s releases", name)
			return err
		}

		var releases []giteaReleaseResponse
		if err := json.Unmarshal(body, &releases); err != nil {
			warn("failed to parse %s releases", name)
			return err
		}
		if len(releases) == 0 {
			break
		}

		for _, release := range releases {
			releaseTag := release.tagName()
			if releaseTag == currentTag {
				continue
			}

			for _, asset := range release.Assets {
				if asset.ID == 0 {
					continue
				}
				info("deleting asset %s from release %s", asset.Name, releaseTag)
				url := fmt.Sprintf("%s/api/v1/repos/%s/releases/%d/assets/%d", server, cfg.githubRepository, release.ID, asset.ID)
				if _, err := curl(run, "DELETE", cfg.githubToken, url); err != nil {
					warn("failed to delete asset %s from release %s", asset.Name, releaseTag)
					failed = true
				}
			}
		}

		if len(releases) < limit {
			break
		}
		page++
	}

	if failed {
		return fmt.Errorf("failed to delete some %s release assets", name)
	}
	return nil
}

func curl(_ runner, method string, token string, url string) ([]byte, error) {
	args := []string{"--fail", "--silent", "--show-error"}
	if method == "DELETE" {
		args = append(args, "--request", "DELETE", "--output", "/dev/null")
	}
	args = append(args, "--header", "Authorization: token "+token, "--header", "Accept: application/json", url)

	cmd := exec.Command("curl", args...)
	return cmd.Output()
}
