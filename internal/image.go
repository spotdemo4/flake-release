package flakerelease

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"go.podman.io/image/v5/copy"
	"go.podman.io/image/v5/docker"
	dockerarchive "go.podman.io/image/v5/docker/archive"
	containerimage "go.podman.io/image/v5/image"
	"go.podman.io/image/v5/signature"
	"go.podman.io/image/v5/types"
	skopeoversion "go.podman.io/skopeo/version"
)

func imageUpload(cfg config, path string, tag string, arch string) error {
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

	srcRef, err := dockerarchive.ParseReference(path)
	if err != nil {
		return err
	}
	destRef, err := dockerImageReference(cfg.registry, cfg.githubRepository, tag+"-"+arch)
	if err != nil {
		return err
	}
	policyCtx, err := insecureImagePolicyContext()
	if err != nil {
		return err
	}
	defer func() {
		if err := policyCtx.Destroy(); err != nil {
			warn("failed to destroy image policy context")
		}
	}()

	info("uploading to %s", transportsImageName(destRef))
	_, err = copy.Image(context.Background(), policyCtx, destRef, srcRef, &copy.Options{
		SourceCtx:       imageSystemContext(config{}),
		DestinationCtx:  imageSystemContext(cfg),
		PreserveDigests: true,
	})
	return err
}

func imageArch(path string) (string, error) {
	ref, err := dockerarchive.ParseReference(path)
	if err != nil {
		return "", err
	}
	inspect, err := inspectImageReference(ref, imageSystemContext(config{}))
	if err != nil {
		return "", err
	}
	return inspect.Architecture, nil
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

func imageExists(cfg config, tag string, arch string) bool {
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

	_, err := inspectImage(cfg, tag+"-"+arch)
	return err == nil
}

func imageCleanupOld(cfg config, currentTag string) error {
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

	tags, err := listImageTags(cfg)
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
		ref, err := dockerImageReference(cfg.registry, cfg.githubRepository, remoteTag)
		if err != nil {
			warn("failed to parse image tag %s", remoteTag)
			failed = true
			continue
		}
		if err := ref.DeleteImage(context.Background(), imageSystemContext(cfg)); err != nil {
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

	remoteTags, err := listImageTags(cfg)
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
		inspect, err := inspectImage(cfg, remoteTag)
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

type imageInspect struct {
	OS           string
	Architecture string
	Labels       map[string]string
}

func listImageTags(cfg config) ([]string, error) {
	ref, err := dockerImageReference(cfg.registry, cfg.githubRepository, "latest")
	if err != nil {
		return nil, err
	}
	return docker.GetRepositoryTags(context.Background(), imageSystemContext(cfg), ref)
}

func inspectImage(cfg config, tag string) (imageInspect, error) {
	ref, err := dockerImageReference(cfg.registry, cfg.githubRepository, tag)
	if err != nil {
		return imageInspect{}, err
	}
	return inspectImageReference(ref, imageSystemContext(cfg))
}

func inspectImageReference(ref types.ImageReference, sys *types.SystemContext) (imageInspect, error) {
	src, err := ref.NewImageSource(context.Background(), sys)
	if err != nil {
		return imageInspect{}, err
	}
	defer src.Close()

	img, err := containerimage.FromUnparsedImage(context.Background(), sys, containerimage.UnparsedInstance(src, nil))
	if err != nil {
		return imageInspect{}, err
	}
	inspect, err := img.Inspect(context.Background())
	if err != nil {
		return imageInspect{}, err
	}

	if inspect.Labels == nil {
		inspect.Labels = map[string]string{}
	}
	return imageInspect{
		OS:           inspect.Os,
		Architecture: inspect.Architecture,
		Labels:       inspect.Labels,
	}, nil
}

func insecureImagePolicyContext() (*signature.PolicyContext, error) {
	return signature.NewPolicyContext(&signature.Policy{
		Default: signature.PolicyRequirements{
			signature.NewPRInsecureAcceptAnything(),
		},
	})
}

func imageSystemContext(cfg config) *types.SystemContext {
	sys := &types.SystemContext{
		DockerRegistryUserAgent: "skopeo/" + skopeoversion.Version + " flake-release",
	}
	if cfg.registryUsername != "" || cfg.registryPassword != "" {
		sys.DockerAuthConfig = &types.DockerAuthConfig{
			Username: cfg.registryUsername,
			Password: cfg.registryPassword,
		}
	}
	return sys
}

func dockerImageReference(registry string, repository string, tag string) (types.ImageReference, error) {
	return docker.ParseReference("//" + strings.ToLower(registry) + "/" + strings.ToLower(repository) + ":" + tag)
}

func transportsImageName(ref types.ImageReference) string {
	return ref.Transport().Name() + ":" + ref.StringWithinTransport()
}

func executable(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.Mode().IsRegular() && stat.Mode()&0o111 != 0
}
