package flakerelease

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/module"
)

type goModuleDownload struct {
	Zip string `json:"Zip"`
}

func preflightGoPackage(set *packagePublicationSet, publication *packagePublication) error {
	if err := set.commands.require("go"); err != nil {
		return err
	}
	if set.releaseTag.versionTag != "v"+set.releaseTag.version {
		return fmt.Errorf("go packages require a v-prefixed semantic version tag, got %q", set.releaseTag.full)
	}
	if err := requireStrictPackageVersion(set.releaseTag.version); err != nil {
		return err
	}
	if strings.Contains(set.releaseTag.version, "+") && !strings.HasSuffix(set.releaseTag.version, "+incompatible") {
		return fmt.Errorf("go module version %q has unsupported build metadata", set.releaseTag.versionTag)
	}

	module, err := readGoModule(publication.manifest)
	if err != nil {
		return err
	}
	if err := validateGoModuleTagNamespace(set.cfg, module, set.releaseTag); err != nil {
		return err
	}
	publication.name = module
	publication.version = set.releaseTag.versionTag

	listed, err := set.commands.capture(commandOptions{
		name: "go",
		args: []string{"list", "-m", "-mod=readonly"},
		dir:  publication.dir,
		env:  []string{"GOWORK=off"},
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(listed) != module {
		return fmt.Errorf("go list returned module %q, expected %q", strings.TrimSpace(listed), module)
	}

	moduleCache, err := os.MkdirTemp(set.temporaryDir, "go-mod-cache-")
	if err != nil {
		return err
	}
	output, err := set.commands.capture(commandOptions{
		name: "go",
		args: []string{"mod", "download", "-json", module + "@" + set.releaseTag.versionTag},
		dir:  publication.dir,
		env: []string{
			"GOMODCACHE=" + moduleCache,
			"GONOSUMDB=" + module,
			"GOPRIVATE=" + module,
			"GOPROXY=direct",
			"GOWORK=off",
		},
	})
	if err != nil {
		return err
	}
	var download goModuleDownload
	if err := json.Unmarshal([]byte(output), &download); err != nil {
		return fmt.Errorf("parsing go module download metadata: %w", err)
	}
	if download.Zip == "" || !isFile(download.Zip) {
		return fmt.Errorf("go mod download returned no module zip")
	}
	publication.artifacts = []string{filepath.Clean(download.Zip)}
	return nil
}

func validateGoModuleTagNamespace(cfg config, module string, tag releaseTag) error {
	if tag.namespace == "" {
		return nil
	}

	serverURL, err := url.Parse(cfg.githubServerURL)
	if err != nil || serverURL.Hostname() == "" {
		return fmt.Errorf("cannot verify Go module provenance: GITHUB_SERVER_URL must contain a host")
	}
	repository, err := parseRepository(cfg.githubRepository)
	if err != nil {
		return fmt.Errorf("cannot verify Go module provenance: invalid GITHUB_REPOSITORY: %w", err)
	}
	prefix := serverURL.Hostname()
	if serverPath := strings.Trim(serverURL.Path, "/"); serverPath != "" {
		prefix += "/" + serverPath
	}
	prefix += "/" + repository.owner + "/" + repository.name
	codePath := goModuleCodePath(module)
	suffix, ok := goModuleRepositorySuffix(codePath, prefix)
	if !ok {
		return fmt.Errorf("go module %q does not belong to repository %q", module, prefix)
	}
	if suffix == "" {
		return fmt.Errorf("root Go module %q cannot be published from scoped tag %q; use an unscoped tag", module, tag.full)
	}
	if suffix != tag.namespace {
		return fmt.Errorf("go submodule %q requires tag namespace %q, got %q", module, suffix, tag.namespace)
	}
	return nil
}

func goModuleRepositorySuffix(module string, prefix string) (string, bool) {
	for _, repositoryPrefix := range []string{prefix, prefix + ".git"} {
		if len(module) < len(repositoryPrefix) || !strings.EqualFold(module[:len(repositoryPrefix)], repositoryPrefix) {
			continue
		}
		if len(module) > len(repositoryPrefix) && module[len(repositoryPrefix)] != '/' {
			continue
		}
		return strings.TrimPrefix(module[len(repositoryPrefix):], "/"), true
	}
	return "", false
}

func goModuleCodePath(modulePath string) string {
	prefix, _, ok := module.SplitPathVersion(modulePath)
	if !ok {
		return modulePath
	}
	return prefix
}

func publishGoPackage(set *packagePublicationSet, publication *packagePublication) error {
	if len(publication.artifacts) != 1 {
		return fmt.Errorf("go package archive was not prepared")
	}
	file, err := os.Open(publication.artifacts[0])
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}

	endpoint := set.registryURL(packageGo) + "upload"
	_, err = httpRequest(httpRequestOptions{
		method:        http.MethodPut,
		url:           endpoint,
		token:         set.cfg.packageRegistryToken,
		authScheme:    tokenAuthScheme,
		accept:        jsonAccept,
		contentType:   "application/zip",
		body:          file,
		contentLength: stat.Size(),
	})
	return err
}

func readGoModule(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		module := strings.TrimSpace(strings.TrimPrefix(line, "module "))
		module = strings.Trim(module, `"`)
		if module == "" || strings.ContainsAny(module, " \t") {
			return "", fmt.Errorf("invalid module directive in %s", path)
		}
		return module, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s has no module directive", path)
}
