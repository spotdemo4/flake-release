package flakerelease

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func imageUpload(run runner, cfg config, path string, tag string, arch string) error {
	if cfg.registry == "" {
		warn("REGISTRY is not set, cannot upload image to container registry")
		return fmt.Errorf("REGISTRY is not set")
	}
	if cfg.githubRepository == "" {
		warn("GITHUB_REPOSITORY is not set, cannot upload image to container registry")
		return fmt.Errorf("GITHUB_REPOSITORY is not set")
	}
	if cfg.registryUsername == "" {
		warn("REGISTRY_USERNAME is not set, cannot upload image to container registry")
		return fmt.Errorf("REGISTRY_USERNAME is not set")
	}
	if cfg.registryPassword == "" {
		warn("REGISTRY_PASSWORD is not set, cannot upload image to container registry")
		return fmt.Errorf("REGISTRY_PASSWORD is not set")
	}

	image := fmt.Sprintf("docker://%s/%s:%s-%s", strings.ToLower(cfg.registry), strings.ToLower(cfg.githubRepository), tag, arch)
	info("uploading to %s", image)
	return run.run("skopeo", "--insecure-policy", "copy", "--dest-creds", cfg.registryUsername+":"+cfg.registryPassword, "--preserve-digests", "docker-archive:"+path, image)
}

func imageArch(run runner, path string) (string, error) {
	return run.capture("skopeo", "--insecure-policy", "inspect", "--format", "{{.Architecture}}", "docker-archive:"+path)
}

func imageGzip(path string) (string, error) {
	out, err := os.CreateTemp("", "flake-release-image-*")
	if err != nil {
		return "", err
	}
	defer out.Close()

	cmd := exec.Command(path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return "", err
	}

	writer, err := gzip.NewWriterLevel(out, gzip.BestSpeed)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(writer, stdout); err != nil {
		_ = writer.Close()
		_ = cmd.Wait()
		return "", err
	}
	if err := writer.Close(); err != nil {
		_ = cmd.Wait()
		return "", err
	}
	if err := cmd.Wait(); err != nil {
		return "", err
	}

	deletePath(path)
	return out.Name(), nil
}

func imageExists(run runner, cfg config, tag string, arch string) bool {
	if cfg.registry == "" {
		warn("REGISTRY is not set, cannot inspect container registry")
		return false
	}
	if cfg.githubRepository == "" {
		warn("GITHUB_REPOSITORY is not set, cannot inspect container registry")
		return false
	}
	if cfg.registryUsername == "" {
		warn("REGISTRY_USERNAME is not set, cannot inspect container registry")
		return false
	}
	if cfg.registryPassword == "" {
		warn("REGISTRY_PASSWORD is not set, cannot inspect container registry")
		return false
	}

	image := fmt.Sprintf("docker://%s/%s:%s-%s", strings.ToLower(cfg.registry), strings.ToLower(cfg.githubRepository), tag, arch)
	err := runCommand("", false, "skopeo", "--insecure-policy", "inspect", "--creds", cfg.registryUsername+":"+cfg.registryPassword, image)
	return err == nil
}

func imageCleanupOld(run runner, cfg config, currentTag string) error {
	if cfg.registry == "" {
		warn("REGISTRY is not set, cannot delete old container images")
		return fmt.Errorf("REGISTRY is not set")
	}
	if cfg.githubRepository == "" {
		warn("GITHUB_REPOSITORY is not set, cannot delete old container images")
		return fmt.Errorf("GITHUB_REPOSITORY is not set")
	}
	if cfg.registryUsername == "" {
		warn("REGISTRY_USERNAME is not set, cannot delete old container images")
		return fmt.Errorf("REGISTRY_USERNAME is not set")
	}
	if cfg.registryPassword == "" {
		warn("REGISTRY_PASSWORD is not set, cannot delete old container images")
		return fmt.Errorf("REGISTRY_PASSWORD is not set")
	}

	tags, err := listImageTags(run, cfg)
	if err != nil {
		warn("failed to fetch image tags")
		return err
	}

	currentFound := false
	for _, remoteTag := range tags {
		if remoteTag == currentTag || strings.HasPrefix(remoteTag, currentTag+"-") {
			currentFound = true
			break
		}
	}
	if !currentFound {
		warn("no remote images found for current tag '%s', skipping old image cleanup", currentTag)
		return nil
	}

	failed := false
	info("deleting old container image tags at %s/%s", strings.ToLower(cfg.registry), strings.ToLower(cfg.githubRepository))
	for _, remoteTag := range tags {
		if remoteTag == "latest" || remoteTag == currentTag || strings.HasPrefix(remoteTag, currentTag+"-") {
			continue
		}

		info("deleting image tag %s", remoteTag)
		image := fmt.Sprintf("docker://%s/%s:%s", strings.ToLower(cfg.registry), strings.ToLower(cfg.githubRepository), remoteTag)
		if err := run.run("skopeo", "--insecure-policy", "delete", "--creds", cfg.registryUsername+":"+cfg.registryPassword, image); err != nil {
			warn("failed to delete image tag %s", remoteTag)
			failed = true
		}
	}

	if failed {
		return fmt.Errorf("failed to delete some image tags")
	}
	return nil
}

func manifestUpdate(run runner, cfg config, tag string) error {
	if cfg.registry == "" {
		warn("REGISTRY is not set, cannot list container registry tags")
		return fmt.Errorf("REGISTRY is not set")
	}
	if cfg.githubRepository == "" {
		warn("GITHUB_REPOSITORY is not set, cannot list container registry tags")
		return fmt.Errorf("GITHUB_REPOSITORY is not set")
	}
	if cfg.registryUsername == "" {
		warn("REGISTRY_USERNAME is not set, cannot list container registry tags")
		return fmt.Errorf("REGISTRY_USERNAME is not set")
	}
	if cfg.registryPassword == "" {
		warn("REGISTRY_PASSWORD is not set, cannot list container registry tags")
		return fmt.Errorf("REGISTRY_PASSWORD is not set")
	}

	remoteTags, err := listImageTags(run, cfg)
	if err != nil {
		warn("failed to fetch image tags")
		return nil
	}

	var matchingTags []string
	for _, remoteTag := range remoteTags {
		if strings.HasPrefix(remoteTag, tag+"-") {
			matchingTags = append(matchingTags, remoteTag)
		}
	}
	if len(matchingTags) == 0 {
		warn("no remote images found for tag '%s'", tag)
		return nil
	}

	var platforms []string
	var annotations []string
	for i, remoteTag := range matchingTags {
		inspect, err := inspectImage(run, cfg, remoteTag)
		if err != nil {
			return err
		}

		if inspect.OS != "" && inspect.Architecture != "" {
			platforms = append(platforms, inspect.OS+"/"+inspect.Architecture)
		}

		if i == 0 {
			for key, value := range inspect.Labels {
				annotations = append(annotations, "--annotations", key+"="+value)
			}
		}
	}

	template := fmt.Sprintf("%s/%s:%s-ARCH", strings.ToLower(cfg.registry), strings.ToLower(cfg.githubRepository), tag)
	target := fmt.Sprintf("%s/%s:%s", strings.ToLower(cfg.registry), strings.ToLower(cfg.githubRepository), tag)
	args := []string{
		"--username", cfg.registryUsername,
		"--password", cfg.registryPassword,
		"push",
		"--type", "oci",
		"from-args",
		"--platforms", strings.Join(platforms, ","),
		"--template", template,
		"--target", target,
		"--tags", "latest",
	}
	args = append(args, annotations...)

	return run.run("manifest-tool", args...)
}

type tagList struct {
	Tags []string `json:"Tags"`
}

type skopeoInspect struct {
	OS           string            `json:"Os"`
	Architecture string            `json:"Architecture"`
	Labels       map[string]string `json:"Labels"`
}

func listImageTags(run runner, cfg config) ([]string, error) {
	out, err := run.capture("skopeo", "--insecure-policy", "list-tags", "--creds", cfg.registryUsername+":"+cfg.registryPassword, "docker://"+strings.ToLower(cfg.registry)+"/"+strings.ToLower(cfg.githubRepository))
	if err != nil {
		return nil, err
	}

	var tags tagList
	if err := json.Unmarshal([]byte(out), &tags); err != nil {
		return nil, err
	}
	return tags.Tags, nil
}

func inspectImage(run runner, cfg config, tag string) (skopeoInspect, error) {
	out, err := run.capture("skopeo", "--insecure-policy", "inspect", "--creds", cfg.registryUsername+":"+cfg.registryPassword, "docker://"+strings.ToLower(cfg.registry)+"/"+strings.ToLower(cfg.githubRepository)+":"+tag)
	if err != nil {
		return skopeoInspect{}, err
	}

	var inspect skopeoInspect
	if err := json.Unmarshal([]byte(out), &inspect); err != nil {
		return skopeoInspect{}, err
	}
	if inspect.Labels == nil {
		inspect.Labels = map[string]string{}
	}
	return inspect, nil
}

func executable(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.Mode().IsRegular() && stat.Mode()&0o111 != 0
}
