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
	"slices"
	"strings"
	"sync"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"go.podman.io/image/v5/copy"
	"go.podman.io/image/v5/docker"
	dockerarchive "go.podman.io/image/v5/docker/archive"
	containerimage "go.podman.io/image/v5/image"
	"go.podman.io/image/v5/manifest"
	"go.podman.io/image/v5/pkg/compression"
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
	if cfg.containerRegistry == "" {
		return fmt.Errorf("cannot upload image: CONTAINER_REGISTRY is not set")
	}
	if repository == "" {
		return fmt.Errorf("cannot upload image: GIT_REPOSITORY is not set")
	}
	if cfg.containerRegistryUsername == "" {
		return fmt.Errorf("cannot upload image: CONTAINER_REGISTRY_USERNAME is not set")
	}
	if cfg.containerRegistryPassword == "" {
		return fmt.Errorf("cannot upload image: CONTAINER_REGISTRY_PASSWORD is not set")
	}

	srcRef, err := dockerarchive.ParseReference(path)
	if err != nil {
		return err
	}
	destRef, err := dockerImageReference(cfg.containerRegistry, repository, tag+"-"+arch)
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
	destinationCtx.CompressionFormat = &compression.Zstd

	status("uploading to %s", transportsImageName(destRef))
	_, err = copy.Image(context.Background(), policyCtx, destRef, srcRef, &copy.Options{
		SourceCtx:              sourceCtx,
		DestinationCtx:         destinationCtx,
		ForceCompressionFormat: true,
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
	defer func() { _ = out.Close() }()

	cmd := exec.CommandContext(context.Background(), path)
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
	if cfg.containerRegistry == "" {
		itemWarn("CONTAINER_REGISTRY is not set; cannot inspect container registry")
		return false
	}
	if repository == "" {
		itemWarn("GIT_REPOSITORY is not set; cannot inspect container registry")
		return false
	}
	if cfg.containerRegistryUsername == "" {
		itemWarn("CONTAINER_REGISTRY_USERNAME is not set; cannot inspect container registry")
		return false
	}
	if cfg.containerRegistryPassword == "" {
		itemWarn("CONTAINER_REGISTRY_PASSWORD is not set; cannot inspect container registry")
		return false
	}

	_, err := inspectImage(cfg, repository, tag+"-"+arch)
	return err == nil
}

func imageTagMatchesVersion(tag string, version string) bool {
	return version != "" && (tag == version || strings.HasPrefix(tag, version+"-"))
}

func imageCleanupCandidates(tags []string, currentTag string, retainedTags []releaseTag) ([]string, bool) {
	currentFound := slices.ContainsFunc(tags, func(tag string) bool {
		return imageTagMatchesVersion(tag, currentTag)
	})
	if !currentFound {
		return nil, false
	}
	var candidates []string
	for _, tag := range tags {
		if tag == "latest" || imageTagMatchesVersion(tag, currentTag) || slices.ContainsFunc(retainedTags, func(retained releaseTag) bool {
			return imageTagMatchesVersion(tag, retained.version)
		}) {
			continue
		}
		candidates = append(candidates, tag)
	}
	return candidates, true
}

func imageCleanupOld(cfg config, repository string, currentTag string, retainedTags []releaseTag) error {
	if cfg.containerRegistry == "" {
		return fmt.Errorf("cannot delete old container images: CONTAINER_REGISTRY is not set")
	}
	if repository == "" {
		return fmt.Errorf("cannot delete old container images: GIT_REPOSITORY is not set")
	}
	if cfg.containerRegistryUsername == "" {
		return fmt.Errorf("cannot delete old container images: CONTAINER_REGISTRY_USERNAME is not set")
	}
	if cfg.containerRegistryPassword == "" {
		return fmt.Errorf("cannot delete old container images: CONTAINER_REGISTRY_PASSWORD is not set")
	}

	tags, err := listImageTags(cfg, repository)
	if err != nil {
		return fmt.Errorf("fetching image tags: %w", err)
	}

	cleanupTags, currentFound := imageCleanupCandidates(tags, currentTag, retainedTags)
	item("container images")
	if !currentFound {
		itemWarn("no remote images found for current tag %q; skipping cleanup", currentTag)
		return nil
	}
	sys, err := imageSystemContext(cfg)
	if err != nil {
		return err
	}

	status("deleting old tags at %s/%s", strings.ToLower(cfg.containerRegistry), strings.ToLower(repository))
	var cleanupErr error
	for _, remoteTag := range cleanupTags {
		status("deleting tag %s", remoteTag)
		ref, err := dockerImageReference(cfg.containerRegistry, repository, remoteTag)
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
	if cfg.containerRegistry == "" {
		return fmt.Errorf("cannot update image manifest: CONTAINER_REGISTRY is not set")
	}
	if repository == "" {
		return fmt.Errorf("cannot update image manifest: GIT_REPOSITORY is not set")
	}
	if cfg.containerRegistryUsername == "" {
		return fmt.Errorf("cannot update image manifest: CONTAINER_REGISTRY_USERNAME is not set")
	}
	if cfg.containerRegistryPassword == "" {
		return fmt.Errorf("cannot update image manifest: CONTAINER_REGISTRY_PASSWORD is not set")
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

	var manifests []ocispec.Descriptor
	platforms := map[string]string{}
	annotations := map[string]string{}
	for i, remoteTag := range matchingTags {
		inspect, err := inspectImage(cfg, repository, remoteTag)
		if err != nil {
			return err
		}
		if manifest.MIMETypeIsMultiImage(inspect.MediaType) {
			return fmt.Errorf("image tag %s is a manifest list; expected a single-platform image", remoteTag)
		}

		if inspect.OS != "" && inspect.Architecture != "" {
			platform := strings.Join([]string{inspect.OS, inspect.Architecture, inspect.Variant}, "/")
			if otherTag, ok := platforms[platform]; ok {
				return fmt.Errorf("image tags %s and %s both provide platform %s", otherTag, remoteTag, strings.TrimSuffix(platform, "/"))
			}
			platforms[platform] = remoteTag
			manifests = append(manifests, ocispec.Descriptor{
				MediaType: inspect.MediaType,
				Digest:    inspect.Digest,
				Size:      inspect.Size,
				Platform: &ocispec.Platform{
					OS:           inspect.OS,
					Architecture: inspect.Architecture,
					Variant:      inspect.Variant,
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

	index, err := manifest.OCI1IndexFromComponents(manifests, annotations).Serialize()
	if err != nil {
		return err
	}
	indexDigest, err := manifest.Digest(index)
	if err != nil {
		return err
	}
	for _, indexTag := range []string{tag, "latest"} {
		if err := pushImageManifest(cfg, repository, indexTag, index); err != nil {
			return fmt.Errorf("pushing image index to tag %s: %w", indexTag, err)
		}
	}

	detail("digest: %s", indexDigest)
	detail("size: %d bytes", len(index))
	return nil
}

type imageInspect struct {
	MediaType    string
	Digest       digest.Digest
	Size         int64
	OS           string
	Architecture string
	Variant      string
	Labels       map[string]string
}

func pushImageManifest(cfg config, repository string, tag string, rawManifest []byte) error {
	ref, err := dockerImageReference(cfg.containerRegistry, repository, tag)
	if err != nil {
		return err
	}
	sys, err := imageSystemContext(cfg)
	if err != nil {
		return err
	}
	dest, err := ref.NewImageDestination(context.Background(), sys)
	if err != nil {
		return err
	}
	defer func() { _ = dest.Close() }()

	if err := dest.PutManifest(context.Background(), rawManifest, nil); err != nil {
		return err
	}
	return dest.Commit(context.Background(), nil)
}

func listImageTags(cfg config, repository string) ([]string, error) {
	ref, err := dockerImageReference(cfg.containerRegistry, repository, "latest")
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
	ref, err := dockerImageReference(cfg.containerRegistry, repository, tag)
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
	defer func() { _ = src.Close() }()

	unparsed := containerimage.UnparsedInstance(src, nil)
	rawManifest, mediaType, err := unparsed.Manifest(context.Background())
	if err != nil {
		return imageInspect{}, err
	}
	manifestDigest, err := manifest.Digest(rawManifest)
	if err != nil {
		return imageInspect{}, err
	}
	img, err := containerimage.FromUnparsedImage(context.Background(), sys, unparsed)
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
		MediaType:    mediaType,
		Digest:       manifestDigest,
		Size:         int64(len(rawManifest)),
		OS:           inspect.Os,
		Architecture: inspect.Architecture,
		Variant:      inspect.Variant,
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
	if cfg.containerRegistryUsername != "" || cfg.containerRegistryPassword != "" {
		sys.DockerAuthConfig = &types.DockerAuthConfig{
			Username: cfg.containerRegistryUsername,
			Password: cfg.containerRegistryPassword,
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
	if cfg.containerRegistry == "" {
		return fmt.Errorf("CONTAINER_REGISTRY is not set")
	}
	if repository == "" {
		return fmt.Errorf("GIT_REPOSITORY is not set")
	}
	if tag == "" {
		return fmt.Errorf("container image tag is empty")
	}
	if _, err := dockerImageReference(cfg.containerRegistry, repository, tag); err != nil {
		return fmt.Errorf("invalid container image reference %q: %w", dockerImageName(cfg.containerRegistry, repository, tag), err)
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
