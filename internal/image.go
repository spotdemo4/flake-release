package flakerelease

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"strings"
	"sync"

	manifestregistry "github.com/estesp/manifest-tool/v2/pkg/registry"
	manifesttypes "github.com/estesp/manifest-tool/v2/pkg/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	"go.podman.io/image/v5/copy"
	"go.podman.io/image/v5/docker"
	dockerarchive "go.podman.io/image/v5/docker/archive"
	containerimage "go.podman.io/image/v5/image"
	"go.podman.io/image/v5/signature"
	"go.podman.io/image/v5/types"
	skopeoversion "go.podman.io/skopeo/version"
)

var (
	imageSystemContextOnce          sync.Once
	imageSystemContextRegistriesDir string
	imageSystemContextRegistriesErr error
)

func imageUpload(cfg config, repository string, path string, tag string, arch string) error {
	if cfg.registry == "" {
		return fmt.Errorf("cannot upload image: REGISTRY is not set")
	}
	if repository == "" {
		return fmt.Errorf("cannot upload image: GITHUB_REPOSITORY is not set")
	}
	if cfg.registryUsername == "" {
		return fmt.Errorf("cannot upload image: REGISTRY_USERNAME is not set")
	}
	if cfg.registryPassword == "" {
		return fmt.Errorf("cannot upload image: REGISTRY_PASSWORD is not set")
	}

	srcRef, err := dockerarchive.ParseReference(path)
	if err != nil {
		return err
	}
	destRef, err := dockerImageReference(cfg.registry, repository, tag+"-"+arch)
	if err != nil {
		return err
	}
	policyCtx, err := insecureImagePolicyContext()
	if err != nil {
		return err
	}
	defer func() {
		if err := policyCtx.Destroy(); err != nil {
			itemWarn("failed to destroy image policy context: %v", err)
		}
	}()
	sourceCtx, err := imageSystemContext(config{})
	if err != nil {
		return err
	}
	destinationCtx, err := imageSystemContext(cfg)
	if err != nil {
		return err
	}

	status("uploading to %s", transportsImageName(destRef))
	_, err = copy.Image(context.Background(), policyCtx, destRef, srcRef, &copy.Options{
		SourceCtx:       sourceCtx,
		DestinationCtx:  destinationCtx,
		PreserveDigests: true,
	})
	return err
}

func imageArch(path string) (string, error) {
	ref, err := dockerarchive.ParseReference(path)
	if err != nil {
		return "", err
	}
	sys, err := imageSystemContext(config{})
	if err != nil {
		return "", err
	}
	inspect, err := inspectImageReference(ref, sys)
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

func imageExists(cfg config, repository string, tag string, arch string) bool {
	if cfg.registry == "" {
		itemWarn("REGISTRY is not set; cannot inspect container registry")
		return false
	}
	if repository == "" {
		itemWarn("GITHUB_REPOSITORY is not set; cannot inspect container registry")
		return false
	}
	if cfg.registryUsername == "" {
		itemWarn("REGISTRY_USERNAME is not set; cannot inspect container registry")
		return false
	}
	if cfg.registryPassword == "" {
		itemWarn("REGISTRY_PASSWORD is not set; cannot inspect container registry")
		return false
	}

	_, err := inspectImage(cfg, repository, tag+"-"+arch)
	return err == nil
}

func imageCleanupOld(cfg config, repository string, currentTag string) error {
	if cfg.registry == "" {
		return fmt.Errorf("cannot delete old container images: REGISTRY is not set")
	}
	if repository == "" {
		return fmt.Errorf("cannot delete old container images: GITHUB_REPOSITORY is not set")
	}
	if cfg.registryUsername == "" {
		return fmt.Errorf("cannot delete old container images: REGISTRY_USERNAME is not set")
	}
	if cfg.registryPassword == "" {
		return fmt.Errorf("cannot delete old container images: REGISTRY_PASSWORD is not set")
	}

	tags, err := listImageTags(cfg, repository)
	if err != nil {
		return fmt.Errorf("fetching image tags: %w", err)
	}

	currentFound := false
	for _, remoteTag := range tags {
		if remoteTag == currentTag || strings.HasPrefix(remoteTag, currentTag+"-") {
			currentFound = true
			break
		}
	}
	item("container images")
	if !currentFound {
		itemWarn("no remote images found for current tag %q; skipping cleanup", currentTag)
		return nil
	}
	sys, err := imageSystemContext(cfg)
	if err != nil {
		return err
	}

	status("deleting old tags at %s/%s", strings.ToLower(cfg.registry), strings.ToLower(repository))
	var cleanupErr error
	for _, remoteTag := range tags {
		if remoteTag == "latest" || remoteTag == currentTag || strings.HasPrefix(remoteTag, currentTag+"-") {
			continue
		}

		status("deleting tag %s", remoteTag)
		ref, err := dockerImageReference(cfg.registry, repository, remoteTag)
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("parsing image tag %s: %w", remoteTag, err))
			continue
		}
		if err := ref.DeleteImage(context.Background(), sys); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("deleting image tag %s: %w", remoteTag, err))
		}
	}

	return cleanupErr
}

func manifestUpdate(cfg config, repository string, tag string) error {
	if cfg.registry == "" {
		return fmt.Errorf("cannot update image manifest: REGISTRY is not set")
	}
	if repository == "" {
		return fmt.Errorf("cannot update image manifest: GITHUB_REPOSITORY is not set")
	}
	if cfg.registryUsername == "" {
		return fmt.Errorf("cannot update image manifest: REGISTRY_USERNAME is not set")
	}
	if cfg.registryPassword == "" {
		return fmt.Errorf("cannot update image manifest: REGISTRY_PASSWORD is not set")
	}

	remoteTags, err := listImageTags(cfg, repository)
	if err != nil {
		itemWarn("failed to fetch image tags: %v", err)
		return nil
	}

	var matchingTags []string
	for _, remoteTag := range remoteTags {
		if strings.HasPrefix(remoteTag, tag+"-") {
			matchingTags = append(matchingTags, remoteTag)
		}
	}
	if len(matchingTags) == 0 {
		itemWarn("no remote images found for tag %q", tag)
		return nil
	}

	var manifests []manifesttypes.ManifestEntry
	annotations := map[string]string{}
	for i, remoteTag := range matchingTags {
		inspect, err := inspectImage(cfg, repository, remoteTag)
		if err != nil {
			return err
		}

		if inspect.OS != "" && inspect.Architecture != "" {
			manifests = append(manifests, manifesttypes.ManifestEntry{
				Image: dockerImageName(cfg.registry, repository, remoteTag),
				Platform: ocispec.Platform{
					OS:           inspect.OS,
					Architecture: inspect.Architecture,
				},
			})
		}

		if i == 0 {
			maps.Copy(annotations, inspect.Labels)
		}
	}
	if len(manifests) == 0 {
		return fmt.Errorf("no platform metadata found for tag '%s'", tag)
	}

	previousLevel := logrus.GetLevel()
	logrus.SetLevel(logrus.WarnLevel)
	defer logrus.SetLevel(previousLevel)

	digest, length, err := manifestregistry.PushManifestList(cfg.registryUsername, cfg.registryPassword, manifesttypes.YAMLInput{
		Image:       dockerImageName(cfg.registry, repository, tag),
		Tags:        []string{"latest"},
		Manifests:   manifests,
		Annotations: annotations,
	}, false, false, false, manifesttypes.OCI, "")
	if err != nil {
		return err
	}

	detail("digest: %s", digest)
	detail("size: %d bytes", length)
	return nil
}

type imageInspect struct {
	OS           string
	Architecture string
	Labels       map[string]string
}

func listImageTags(cfg config, repository string) ([]string, error) {
	ref, err := dockerImageReference(cfg.registry, repository, "latest")
	if err != nil {
		return nil, err
	}
	sys, err := imageSystemContext(cfg)
	if err != nil {
		return nil, err
	}
	return docker.GetRepositoryTags(context.Background(), sys, ref)
}

func inspectImage(cfg config, repository string, tag string) (imageInspect, error) {
	ref, err := dockerImageReference(cfg.registry, repository, tag)
	if err != nil {
		return imageInspect{}, err
	}
	sys, err := imageSystemContext(cfg)
	if err != nil {
		return imageInspect{}, err
	}
	return inspectImageReference(ref, sys)
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

func imageSystemContext(cfg config) (*types.SystemContext, error) {
	registriesConfDir, err := imageSystemContextRegistriesConfDir()
	if err != nil {
		return nil, err
	}
	sys := &types.SystemContext{
		DockerRegistryUserAgent:     "skopeo/" + skopeoversion.Version + " flake-release",
		SystemRegistriesConfPath:    os.DevNull,
		SystemRegistriesConfDirPath: registriesConfDir,
	}
	if cfg.registryUsername != "" || cfg.registryPassword != "" {
		sys.DockerAuthConfig = &types.DockerAuthConfig{
			Username: cfg.registryUsername,
			Password: cfg.registryPassword,
		}
	}
	return sys, nil
}

func imageSystemContextRegistriesConfDir() (string, error) {
	imageSystemContextOnce.Do(func() {
		imageSystemContextRegistriesDir, imageSystemContextRegistriesErr = os.MkdirTemp("", "flake-release-registries.conf.d-")
	})
	return imageSystemContextRegistriesDir, imageSystemContextRegistriesErr
}

func validateImageDestination(cfg config, repository string, tag string) error {
	if cfg.registry == "" {
		return fmt.Errorf("REGISTRY is not set")
	}
	if repository == "" {
		return fmt.Errorf("GITHUB_REPOSITORY is not set")
	}
	if tag == "" {
		return fmt.Errorf("container image tag is empty")
	}
	if _, err := dockerImageReference(cfg.registry, repository, tag); err != nil {
		return fmt.Errorf("invalid container image reference %q: %w", dockerImageName(cfg.registry, repository, tag), err)
	}
	return nil
}

func dockerImageReference(registry string, repository string, tag string) (types.ImageReference, error) {
	return docker.ParseReference("//" + dockerImageName(registry, repository, tag))
}

func dockerImageName(registry string, repository string, tag string) string {
	return strings.ToLower(registry) + "/" + strings.ToLower(repository) + ":" + tag
}

func transportsImageName(ref types.ImageReference) string {
	return ref.Transport().Name() + ":" + ref.StringWithinTransport()
}

func executable(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.Mode().IsRegular() && stat.Mode()&0o111 != 0
}
