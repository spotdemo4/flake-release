#!/usr/bin/env bash
# export PATH="${PATH}" placeholder

set -o errexit
set -o nounset
set -o pipefail

# make source imports work
DIR="${BASH_SOURCE%/*}"
if [[ ! -d "$DIR" ]]; then DIR="$PWD"; fi

source "$DIR/util.sh"
source "$DIR/git.sh"
source "$DIR/github.sh"
source "$DIR/gitea.sh"
source "$DIR/release.sh"
source "$DIR/image.sh"
source "$DIR/nix.sh"
source "$DIR/platform.sh"

# get args
ARGS=()
if [[ "$#" -gt 0 ]]; then
    ARGS+=( "${@}" )
fi
if [[ -n "${ENV_ARGS-}" ]]; then
    readarray -t ENV_ARGS < <(array "${ENV_ARGS-}")
    ARGS+=( "${ENV_ARGS[@]}" )
fi

# login to gitea
if [[ -n "${GITEA_ACTIONS-}" ]]; then
    gitea_login
fi

# get tag
if [[ -z "${TAG-}" ]]; then
    TAG=$(git_latest_tag)
fi

# get changelog
CHANGELOG=$(git_changelog "${TAG}")

# release
if ! release "${TAG}" "${CHANGELOG}"; then
    warn "could not create release ${TAG}"
fi

# get nix packages
NIX_SYSTEM=$(nix_system)
readarray -t PACKAGES < <(nix_packages "$NIX_SYSTEM")
if [[ ${#PACKAGES[@]} -eq 0 ]]; then
    warn "no packages found in the nix flake for system '$NIX_SYSTEM'"
fi

# build and upload assets
STORE_PATHS=()
IMAGE_PLATFORMS=()
for PACKAGE in "${PACKAGES[@]}"; do
    info ""

    if [[ "${#ARGS[@]}" -ne 0 && ! ${ARGS[*]} =~ $PACKAGE ]]; then
        info "skipping package '$PACKAGE'"
        continue
    fi

    info "evaluating $(bold "$PACKAGE")"
    STORE_PATH=$(nix_pkg_path "$PACKAGE")
    if [[ ${STORE_PATHS[*]} =~ $STORE_PATH ]]; then
        info "$PACKAGE: already built, skipping"
        continue
    fi
    STORE_PATHS+=( "${STORE_PATH}" )

    if ! nix_build "$PACKAGE"; then
        warn "build failed"
        continue
    fi

    # `mkDerivation` attributes
    PNAME=$(nix_pkg_pname "${PACKAGE}")
    VERSION=$(nix_pkg_version "${PACKAGE}")
    HOMEPAGE=$(nix_pkg_homepage "${PACKAGE}")
    DESCRIPTION=$(nix_pkg_description "${PACKAGE}")
    LICENSE=$(nix_pkg_license "${PACKAGE}")

    # `dockerTools` attributes
    IMAGE_NAME=$(nix_pkg_image_name "${PACKAGE}")
    IMAGE_TAG=$(nix_pkg_image_tag "${PACKAGE}")

    if [[ "${VERSION}" != "${TAG#v}" && "${IMAGE_TAG}" != "${TAG#v}" ]]; then
        warn "package version '${VERSION}' does not match git tag '${TAG#v}'"
        continue
    fi

    # package info
    OS=$(detect_os "${STORE_PATH}")
    ARCH=$(detect_arch "${STORE_PATH}")
    ONLY_BINS=$(only_bins "${STORE_PATH}")

    # `dockerTools.buildLayeredImage`
    if [[ -n "${IMAGE_NAME}" && -n "${IMAGE_TAG}" && -f "${STORE_PATH}" && "${STORE_PATH}" == *".tar.gz" ]]; then

        info "detected as image $(bold "${IMAGE_NAME}:${IMAGE_TAG}")"

        IMAGE_OS=$(image_os "${STORE_PATH}")
        IMAGE_ARCH=$(image_arch "${STORE_PATH}")

        if ! upload_image "${STORE_PATH}" "${IMAGE_TAG}" "${IMAGE_ARCH}"; then
            warn "uploading failed"
            continue
        fi

        IMAGE_PLATFORMS+=( "${IMAGE_OS}/${IMAGE_ARCH}" )

    # `dockerTools.streamLayeredImage`
    elif [[ -n "${IMAGE_NAME}" && -n "${IMAGE_TAG}" && -f "${STORE_PATH}" && -x "${STORE_PATH}" ]]; then

        info "detected as image $(bold "${IMAGE_NAME}:${IMAGE_TAG}")"

        IMAGE_ZIPPED=$(image_gzip "${STORE_PATH}")
        IMAGE_OS=$(image_os "${IMAGE_ZIPPED}")
        IMAGE_ARCH=$(image_arch "${IMAGE_ZIPPED}")

        if ! upload_image "${STORE_PATH}" "${IMAGE_TAG}" "${IMAGE_ARCH}"; then
            warn "upload failed"
            continue
        fi

        IMAGE_PLATFORMS+=( "${IMAGE_OS}/${IMAGE_ARCH}" )

    # `mkDerivation` executable(s)
    elif [[ -n "${PNAME}" && -n "${VERSION}" && -d "${STORE_PATH}" && -n "${ONLY_BINS-}" ]]; then

        info "compressing $(bold "${PNAME}")"

        if ! ARCHIVE=$(archive "${STORE_PATH}" "${OS}"); then
            warn "archiving failed"
            continue
        fi

        ASSET=$(rename "${ARCHIVE}" "${PNAME}" "${VERSION}" "${OS}" "${ARCH}")

        if ! release_asset "${TAG}" "${ASSET}"; then
            warn "uploading failed"
            continue
        fi

    # `mkDerivation` bundle
    elif [[ -n "${PNAME}" && -n "${VERSION}" && -d "${STORE_PATH}" && -n "${BUNDLE-}" ]]; then

        info "bundling $(bold "${PNAME}")"

        if ! ARCHIVE=$(nix_bundle "${PACKAGE}" "${BUNDLE}"); then
            warn "bundling failed"
            continue
        fi

        ASSET=$(rename "${ARCHIVE}" "${PNAME}" "${VERSION}" "${OS}" "${ARCH}")

        if ! release_asset "${TAG}" "${ASSET}"; then
            warn "uploading failed"
            continue
        fi

    else
        warn "unknown package type"
    fi
done

# create and push manifest
manifest_push "${TAG#v}" "$( IFS=','; echo "${IMAGE_PLATFORMS[*]}" )" "${HOMEPAGE-}" "${DESCRIPTION-}" "${LICENSE-}"

# cleanup
rm -rf ~/.config/tea # gitea tea
rm -f "${CHANGELOG}" # changelog
