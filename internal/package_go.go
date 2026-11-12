package flakerelease

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
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

	module, requires, err := readGoModule(publication.manifest)
	if err != nil {
		return err
	}
	if err := validateGoModuleTagNamespace(set.cfg, module, set.releaseTag); err != nil {
		return err
	}
	publication.name = module
	publication.version = set.releaseTag.versionTag
	publication.dependencies = requires

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

	serverURL, err := url.Parse(cfg.gitServerURL)
	if err != nil || serverURL.Hostname() == "" {
		return fmt.Errorf("cannot verify Go module provenance: GIT_SERVER_URL must contain a host")
	}
	repository, err := parseRepository(cfg.gitRepository)
	if err != nil {
		return fmt.Errorf("cannot verify Go module provenance: invalid GIT_REPOSITORY: %w", err)
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
	defer func() { _ = file.Close() }()
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

// readGoModule returns the module path in a go.mod file and the modules it requires.
func readGoModule(path string) (string, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	file, err := modfile.ParseLax(path, data, nil)
	if err != nil {
		return "", nil, err
	}
	if file.Module == nil || file.Module.Mod.Path == "" {
		return "", nil, fmt.Errorf("%s has no module directive", path)
	}
	requires := make([]string, 0, len(file.Require))
	for _, require := range file.Require {
		requires = append(requires, require.Mod.Path)
	}
	return file.Module.Mod.Path, requires, nil
}
