package flakerelease

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type releaseProvider string

const (
	releaseGitHub  releaseProvider = "github"
	releaseGitea   releaseProvider = "gitea"
	releaseForgejo releaseProvider = "forgejo"

	githubAuthScheme = "Bearer"
	tokenAuthScheme  = "token"
	githubAccept     = "application/vnd.github+json"
	jsonAccept       = "application/json"
)

type repository struct {
	owner string
	name  string
}

func (r repository) path() string {
	return url.PathEscape(r.owner) + "/" + url.PathEscape(r.name)
}

type releaseAsset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type githubReleaseResponse struct {
	ID        int64          `json:"id"`
	TagName   string         `json:"tag_name"`
	UploadURL string         `json:"upload_url"`
	Assets    []releaseAsset `json:"assets"`
}

type giteaReleaseResponse struct {
	ID         int64          `json:"id"`
	TagName    string         `json:"tag_name"`
	TagNameAlt string         `json:"tagName"`
	Tag        string         `json:"tag"`
	Assets     []releaseAsset `json:"assets"`
}

type createReleaseRequest struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name,omitempty"`
	Body    string `json:"body,omitempty"`
}

type httpRequestOptions struct {
	method        string
	url           string
	token         string
	authScheme    string
	accept        string
	contentType   string
	body          io.Reader
	contentLength int64
}

type releaseClient interface {
	createRelease(tag string, changelog string) error
	uploadAsset(tag string, asset string) error
	cleanupAssets(currentTag string) error
}

type noopReleaseClient struct{}

type githubReleaseClient struct {
	cfg config
}

type giteaReleaseClient struct {
	cfg  config
	name string
}

type forgejoReleaseClient struct {
	giteaReleaseClient
}

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
	case strings.Contains(origin, "trev.zip"):
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

func newReleaseClient(provider releaseProvider, cfg config) releaseClient {
	switch provider {
	case releaseGitHub:
		return githubReleaseClient{cfg: cfg}
	case releaseGitea:
		return newGiteaReleaseClient(cfg, releaseGitea)
	case releaseForgejo:
		return forgejoReleaseClient{
			giteaReleaseClient: newGiteaReleaseClient(cfg, releaseForgejo),
		}
	default:
		return noopReleaseClient{}
	}
}

func newGiteaReleaseClient(cfg config, provider releaseProvider) giteaReleaseClient {
	return giteaReleaseClient{
		cfg:  cfg,
		name: releaseProviderName(provider),
	}
}

func (noopReleaseClient) createRelease(_ string, _ string) error {
	return nil
}

func (noopReleaseClient) uploadAsset(_ string, _ string) error {
	return nil
}

func (noopReleaseClient) cleanupAssets(_ string) error {
	return nil
}

func (c githubReleaseClient) createRelease(tag string, changelog string) error {
	action := "create GitHub release"
	repo, err := c.repository(action)
	if err != nil {
		return err
	}
	if err := c.requireToken(action); err != nil {
		return err
	}

	body, err := os.ReadFile(changelog)
	if err != nil {
		return err
	}

	info("creating release %s at %s", tag, c.cfg.githubRepository)
	endpoint := fmt.Sprintf("%s/repos/%s/releases", c.apiBase(), repo.path())
	_, err = c.jsonRequest(http.MethodPost, endpoint, createReleaseRequest{
		TagName: tag,
		Name:    tag,
		Body:    string(body),
	})
	return err
}

func (c githubReleaseClient) uploadAsset(tag string, asset string) error {
	action := "upload asset to GitHub"
	repo, err := c.repository(action)
	if err != nil {
		return err
	}
	if err := c.requireToken(action); err != nil {
		return err
	}

	release, err := c.releaseByTag(repo, tag)
	if err != nil {
		return err
	}
	uploadURL, err := releaseAssetUploadURL(release.UploadURL, filepath.Base(asset))
	if err != nil {
		return err
	}

	file, err := os.Open(asset)
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}

	contentType := mime.TypeByExtension(filepath.Ext(asset))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	info("uploading asset to release %s at %s", tag, c.cfg.githubRepository)
	_, err = c.httpRequest(httpRequestOptions{
		method:        http.MethodPost,
		url:           uploadURL,
		contentType:   contentType,
		body:          file,
		contentLength: stat.Size(),
	})
	return err
}

func (c githubReleaseClient) cleanupAssets(currentTag string) error {
	action := "delete old GitHub release assets"
	repo, err := c.repository(action)
	if err != nil {
		return err
	}
	if err := c.requireToken(action); err != nil {
		return err
	}

	releases, err := c.listReleases(repo)
	if err != nil {
		warn("failed to fetch GitHub releases")
		return err
	}

	info("deleting old GitHub release assets at %s", c.cfg.githubRepository)
	failed := false
	for _, release := range releases {
		if release.TagName == "" || release.TagName == currentTag || release.ID == 0 {
			continue
		}

		assets, err := c.listReleaseAssets(repo, release.ID)
		if err != nil {
			warn("failed to fetch GitHub release assets for %s", release.TagName)
			failed = true
			continue
		}

		for _, asset := range assets {
			if asset.ID == 0 || asset.Name == "" {
				continue
			}
			info("deleting asset %s from release %s", asset.Name, release.TagName)
			endpoint := fmt.Sprintf("%s/repos/%s/releases/assets/%d", c.apiBase(), repo.path(), asset.ID)
			if _, err := c.httpRequest(httpRequestOptions{
				method: http.MethodDelete,
				url:    endpoint,
			}); err != nil {
				warn("failed to delete asset %s from release %s", asset.Name, release.TagName)
				failed = true
			}
		}
	}

	if failed {
		return fmt.Errorf("failed to delete some GitHub release assets")
	}
	return nil
}

func (c githubReleaseClient) releaseByTag(repo repository, tag string) (githubReleaseResponse, error) {
	var release githubReleaseResponse
	endpoint := fmt.Sprintf("%s/repos/%s/releases/tags/%s", c.apiBase(), repo.path(), url.PathEscape(tag))
	body, err := c.httpRequest(httpRequestOptions{
		method: http.MethodGet,
		url:    endpoint,
	})
	if err != nil {
		return release, err
	}
	return release, json.Unmarshal(body, &release)
}

func (c githubReleaseClient) listReleases(repo repository) ([]githubReleaseResponse, error) {
	const limit = 100
	var releases []githubReleaseResponse
	for page := 1; ; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/releases?per_page=%d&page=%d", c.apiBase(), repo.path(), limit, page)
		var pageReleases []githubReleaseResponse
		body, err := c.httpRequest(httpRequestOptions{
			method: http.MethodGet,
			url:    endpoint,
		})
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &pageReleases); err != nil {
			return nil, err
		}
		releases = append(releases, pageReleases...)
		if len(pageReleases) < limit {
			break
		}
	}
	return releases, nil
}

func (c githubReleaseClient) listReleaseAssets(repo repository, releaseID int64) ([]releaseAsset, error) {
	const limit = 100
	var assets []releaseAsset
	for page := 1; ; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/releases/%d/assets?per_page=%d&page=%d", c.apiBase(), repo.path(), releaseID, limit, page)
		var pageAssets []releaseAsset
		body, err := c.httpRequest(httpRequestOptions{
			method: http.MethodGet,
			url:    endpoint,
		})
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &pageAssets); err != nil {
			return nil, err
		}
		assets = append(assets, pageAssets...)
		if len(pageAssets) < limit {
			break
		}
	}
	return assets, nil
}

func (c githubReleaseClient) repository(action string) (repository, error) {
	return releaseRepository(c.cfg, action)
}

func (c githubReleaseClient) requireToken(action string) error {
	return requireToken(c.cfg, action)
}

func (c githubReleaseClient) apiBase() string {
	return githubAPIBase(c.cfg)
}

func (c githubReleaseClient) jsonRequest(method string, endpoint string, payload any) ([]byte, error) {
	return jsonRequest(method, githubAuthScheme, c.cfg.githubToken, githubAccept, endpoint, payload)
}

func (c githubReleaseClient) httpRequest(options httpRequestOptions) ([]byte, error) {
	options.token = c.cfg.githubToken
	options.authScheme = githubAuthScheme
	options.accept = githubAccept
	return httpRequest(options)
}

func (c giteaReleaseClient) createRelease(tag string, changelog string) error {
	repo, err := c.repository("create " + c.name + " release")
	if err != nil {
		return err
	}

	body, err := os.ReadFile(changelog)
	if err != nil {
		return err
	}

	info("creating release %s at %s", tag, c.cfg.githubRepository)
	endpoint := fmt.Sprintf("%s/repos/%s/releases", c.apiBase(), repo.path())
	_, err = c.jsonRequest(http.MethodPost, endpoint, createReleaseRequest{
		TagName: tag,
		Name:    tag,
		Body:    string(body),
	})
	return err
}

func (c giteaReleaseClient) uploadAsset(tag string, asset string) error {
	repo, err := c.repository("upload asset to " + c.name)
	if err != nil {
		return err
	}

	release, err := c.releaseByTag(repo, tag)
	if err != nil {
		return err
	}
	if release.ID == 0 {
		return fmt.Errorf("%s release %s has no id", c.name, tag)
	}

	body, contentType, err := multipartFileBody("attachment", asset)
	if err != nil {
		return err
	}
	defer body.Close()

	endpoint, err := releaseAssetUploadURL(
		fmt.Sprintf("%s/repos/%s/releases/%d/assets", c.apiBase(), repo.path(), release.ID),
		filepath.Base(asset),
	)
	if err != nil {
		return err
	}

	info("uploading asset to release %s at %s", tag, c.cfg.githubRepository)
	_, err = c.httpRequest(httpRequestOptions{
		method:      http.MethodPost,
		url:         endpoint,
		contentType: contentType,
		body:        body,
	})
	return err
}

func (r giteaReleaseResponse) tagName() string {
	return firstNonEmpty(r.TagName, r.TagNameAlt, r.Tag)
}

func (c giteaReleaseClient) cleanupAssets(currentTag string) error {
	repo, err := c.repository("delete old " + c.name + " release assets")
	if err != nil {
		return err
	}

	releases, err := c.listReleases(repo)
	if err != nil {
		warn("failed to fetch %s releases", c.name)
		return err
	}

	failed := false
	info("deleting old %s release assets at %s", c.name, c.cfg.githubRepository)
	for _, release := range releases {
		releaseTag := release.tagName()
		if releaseTag == currentTag || release.ID == 0 {
			continue
		}

		assets, err := c.listReleaseAssets(repo, release.ID)
		if err != nil {
			warn("failed to fetch %s release assets for %s", c.name, releaseTag)
			failed = true
			continue
		}

		for _, asset := range assets {
			if asset.ID == 0 {
				continue
			}
			info("deleting asset %s from release %s", asset.Name, releaseTag)
			endpoint := fmt.Sprintf("%s/repos/%s/releases/%d/assets/%d", c.apiBase(), repo.path(), release.ID, asset.ID)
			if _, err := c.httpRequest(httpRequestOptions{
				method: http.MethodDelete,
				url:    endpoint,
			}); err != nil {
				warn("failed to delete asset %s from release %s", asset.Name, releaseTag)
				failed = true
			}
		}
	}

	if failed {
		return fmt.Errorf("failed to delete some %s release assets", c.name)
	}
	return nil
}

func (c giteaReleaseClient) releaseByTag(repo repository, tag string) (giteaReleaseResponse, error) {
	var release giteaReleaseResponse
	endpoint := fmt.Sprintf("%s/repos/%s/releases/tags/%s", c.apiBase(), repo.path(), url.PathEscape(tag))
	body, err := c.httpRequest(httpRequestOptions{
		method: http.MethodGet,
		url:    endpoint,
	})
	if err != nil {
		return release, err
	}
	return release, json.Unmarshal(body, &release)
}

func (c giteaReleaseClient) listReleases(repo repository) ([]giteaReleaseResponse, error) {
	const limit = 100
	var releases []giteaReleaseResponse
	for page := 1; ; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/releases?page=%d&limit=%d", c.apiBase(), repo.path(), page, limit)
		var pageReleases []giteaReleaseResponse
		body, err := c.httpRequest(httpRequestOptions{
			method: http.MethodGet,
			url:    endpoint,
		})
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &pageReleases); err != nil {
			return nil, err
		}
		releases = append(releases, pageReleases...)
		if len(pageReleases) < limit {
			break
		}
	}
	return releases, nil
}

func (c giteaReleaseClient) listReleaseAssets(repo repository, releaseID int64) ([]releaseAsset, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/releases/%d/assets", c.apiBase(), repo.path(), releaseID)
	var assets []releaseAsset
	body, err := c.httpRequest(httpRequestOptions{
		method: http.MethodGet,
		url:    endpoint,
	})
	if err != nil {
		return nil, err
	}
	return assets, json.Unmarshal(body, &assets)
}

func (c giteaReleaseClient) repository(action string) (repository, error) {
	repo, err := releaseRepository(c.cfg, action)
	if err != nil {
		return repository{}, err
	}
	if err := requireServerURL(c.cfg, action); err != nil {
		return repository{}, err
	}
	if err := requireToken(c.cfg, action); err != nil {
		return repository{}, err
	}
	return repo, nil
}

func (c giteaReleaseClient) apiBase() string {
	return giteaAPIBase(c.cfg)
}

func (c giteaReleaseClient) jsonRequest(method string, endpoint string, payload any) ([]byte, error) {
	return jsonRequest(method, tokenAuthScheme, c.cfg.githubToken, jsonAccept, endpoint, payload)
}

func (c giteaReleaseClient) httpRequest(options httpRequestOptions) ([]byte, error) {
	options.token = c.cfg.githubToken
	options.authScheme = tokenAuthScheme
	options.accept = jsonAccept
	return httpRequest(options)
}

func releaseRepository(cfg config, action string) (repository, error) {
	if cfg.githubRepository == "" {
		warn("GITHUB_REPOSITORY is not set, cannot %s", action)
		return repository{}, fmt.Errorf("GITHUB_REPOSITORY is not set")
	}

	repo, err := parseRepository(cfg.githubRepository)
	if err != nil {
		warn("GITHUB_REPOSITORY must be owner/repo, cannot %s", action)
		return repository{}, err
	}
	return repo, nil
}

func requireServerURL(cfg config, action string) error {
	if cfg.githubServerURL == "" {
		warn("GITHUB_SERVER_URL is not set, cannot %s", action)
		return fmt.Errorf("GITHUB_SERVER_URL is not set")
	}
	return nil
}

func requireToken(cfg config, action string) error {
	if cfg.githubToken == "" {
		warn("GITHUB_TOKEN is not set, cannot %s", action)
		return fmt.Errorf("GITHUB_TOKEN is not set")
	}
	return nil
}

func parseRepository(value string) (repository, error) {
	owner, name, ok := strings.Cut(value, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return repository{}, fmt.Errorf("invalid repository %q", value)
	}
	return repository{owner: owner, name: name}, nil
}

func releaseProviderName(provider releaseProvider) string {
	switch provider {
	case releaseForgejo:
		return "Forgejo"
	case releaseGitea:
		return "Gitea"
	case releaseGitHub:
		return "GitHub"
	default:
		return string(provider)
	}
}

func githubAPIBase(cfg config) string {
	server := strings.TrimRight(cfg.githubServerURL, "/")
	if server == "" || server == "https://github.com" || server == "http://github.com" {
		return "https://api.github.com"
	}
	return server + "/api/v3"
}

func giteaAPIBase(cfg config) string {
	return strings.TrimRight(cfg.githubServerURL, "/") + "/api/v1"
}

func releaseAssetUploadURL(uploadURL string, name string) (string, error) {
	if uploadURL == "" {
		return "", fmt.Errorf("release upload URL is empty")
	}
	baseURL := strings.Split(uploadURL, "{")[0]
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("name", name)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func multipartFileBody(fieldName string, path string) (io.ReadCloser, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}

	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	go func() {
		defer file.Close()

		part, err := multipartWriter.CreateFormFile(fieldName, filepath.Base(path))
		if err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, file); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		if err := multipartWriter.Close(); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		_ = writer.Close()
	}()

	return reader, multipartWriter.FormDataContentType(), nil
}

func jsonRequest(method string, authScheme string, token string, accept string, endpoint string, payload any) ([]byte, error) {
	var body io.Reader
	contentType := ""
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
		contentType = jsonAccept
	}

	return httpRequest(httpRequestOptions{
		method:      method,
		url:         endpoint,
		token:       token,
		authScheme:  authScheme,
		accept:      accept,
		contentType: contentType,
		body:        body,
	})
}

func httpRequest(options httpRequestOptions) ([]byte, error) {
	req, err := http.NewRequest(options.method, options.url, options.body)
	if err != nil {
		return nil, err
	}
	if options.token != "" {
		authScheme := options.authScheme
		if authScheme == "" {
			authScheme = tokenAuthScheme
		}
		req.Header.Set("Authorization", authScheme+" "+options.token)
	}
	if options.accept == "" {
		options.accept = jsonAccept
	}
	req.Header.Set("Accept", options.accept)
	if options.contentType != "" {
		req.Header.Set("Content-Type", options.contentType)
	}
	if options.contentLength > 0 {
		req.ContentLength = options.contentLength
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if message != "" {
			return nil, fmt.Errorf("%s %s failed: %s: %s", options.method, options.url, resp.Status, message)
		}
		return nil, fmt.Errorf("%s %s failed: %s", options.method, options.url, resp.Status)
	}
	if options.method == http.MethodDelete {
		return nil, nil
	}
	return body, nil
}
