#!/usr/bin/env bash

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
source "$DIR/forgejo.sh"
source "$DIR/release.sh"
source "$DIR/image.sh"
source "$DIR/nix.sh"

# settings
DRY_RUN="${DRY_RUN:-false}"

PKGS=()
# get packages from args
for arg in "${@}"; do
    if [[ "${arg}" == "--help" ]]; then
        info "Usage: flake-release [packages...] [--dry-run]"
        info ""
        info "If no packages are provided as arguments, the script will attempt to get packages from the nix flake for the current system."
        exit 0
    elif [[ "${arg}" == "--dry-run" ]]; then
        DRY_RUN="true"
    else
        PKGS+=( "${arg}" )
    fi
done

# get packages from env
if [[ -n "${PACKAGES-}" ]]; then
    readarray -t ENV_PACKAGES < <(array "${PACKAGES-}")
    PKGS+=( "${ENV_PACKAGES[@]}" )
fi

# git type
if ! TYPE=$(release_type "$(git_origin)"); then
    exit 1
fi
info "git type: ${TYPE}"

# git tag
if [[ -z "${TAG-}" ]]; then
    TAG=$(git_latest_tag)
fi
info "git tag: ${TAG}"

# git user
if [[ -z "${GITHUB_ACTOR-}" ]]; then
    GITHUB_ACTOR=$(git_user)
fi
info "git user: ${GITHUB_ACTOR}"

# registry user
if [[ -z "${REGISTRY_USERNAME-}" ]]; then
    REGISTRY_USERNAME=$(git_user)
fi
info "registry user: ${REGISTRY_USERNAME}"

# get changelog
CHANGELOG=$(git_changelog "${TAG}")

# login
if [[ "${TYPE}" == "gitea" ]]; then
    gitea_login
elif [[ "${TYPE}" == "forgejo" ]]; then
    forgejo_login
fi

# release
if [[ "${DRY_RUN}" == "true" ]]; then
    info "dry run: skipping release creation"
else
    if ! release "${TYPE}" "${TAG}" "${CHANGELOG}"; then
        warn "could not create release ${TAG}"
    fi
fi

# get nix packages from .#packages.${system} if not provided
if [[ ${#PKGS[@]} -eq 0 ]]; then
    NIX_SYSTEM=$(nix_system)
    readarray -t PKGS < <(nix_packages "${NIX_SYSTEM}")
    if [[ ${#PKGS[@]} -eq 0 ]]; then
        warn "no packages found in the nix flake for system '${NIX_SYSTEM}'"
    fi
fi

# build and upload assets
STORE_PATHS=()
for PACKAGE in "${PKGS[@]}"; do
    info ""
    info "evaluating $(bold "${PACKAGE}")"
    STORE_PATH=$(nix_pkg_path "${PACKAGE}")
    if [[ ${STORE_PATHS[*]} =~ ${STORE_PATH} ]]; then
        info "${PACKAGE}: already built, skipping"
        continue
    fi
    STORE_PATHS+=( "${STORE_PATH}" )

    if ! nix_build "${PACKAGE}"; then
        warn "build failed"
        continue
    fi

    # `mkDerivation` attributes
    PNAME=$(nix_pkg_pname "${PACKAGE}")
    VERSION=$(nix_pkg_version "${PACKAGE}")
    PLATFORM=$(nix_pkg_platform "${PACKAGE}")
    OS=$(echo "${PLATFORM}" | jq -r '.GOOS // empty')
    ARCH=$(echo "${PLATFORM}" | jq -r '.GOARCH // empty')

    # `dockerTools` attributes
    IMAGE_NAME=$(nix_image_name "${PACKAGE}")
    IMAGE_TAG=$(nix_image_tag "${PACKAGE}")

    if [[ "${VERSION}" != "${TAG#v}" && "${IMAGE_TAG}" != "${TAG#v}" ]]; then
        warn "package version '${VERSION:-"${IMAGE_TAG}"}' does not match git tag '${TAG#v}'"
        continue
    fi

    # `dockerTools` image
    if
        [[ -n "${IMAGE_NAME}" ]] &&
        [[ -n "${IMAGE_TAG}" ]] &&
        [[ -f "${STORE_PATH}" ]] &&
        [[ "${OS}" == "linux" ]];
    then
        info "detected as image $(bold "${IMAGE_NAME}:${IMAGE_TAG}")"
        IMAGES="true"

        # `dockerTools.buildLayeredImage`
        if [[ "${STORE_PATH}" == *".tar.gz" ]]; then
            info "image type: buildLayeredImage"
        
        # `dockerTools.streamLayeredImage`
        elif [[ -x "${STORE_PATH}" ]]; then
            info "image type: streamLayeredImage, zipping"
            STORE_PATH=$(image_gzip "${STORE_PATH}")

        else
            warn "could not determine image type"
            continue
        fi

        IMAGE_ARCH=$(image_arch "${STORE_PATH}")
        info "image arch: ${IMAGE_ARCH}"

        if image_exists "${IMAGE_TAG}" "${IMAGE_ARCH}"; then
            warn "image already exists, skipping upload"
            continue
        fi

        if [[ "${DRY_RUN}" == "true" ]]; then
            info "dry run: skipping image upload"
        else
            if ! image_upload "${STORE_PATH}" "${IMAGE_TAG}" "${IMAGE_ARCH}"; then
                warn "upload failed"
                continue
            fi
        fi

    # `mkDerivation` static executable(s)
    elif
        [[ -n "${PNAME}" ]] &&
        [[ -n "${VERSION}" ]] &&
        all_static "${STORE_PATH}";
    then
        info "detected as static executable(s)"

        if ! ARCHIVE=$(archive "${STORE_PATH}" "${OS}"); then
            warn "archiving failed"
            continue
        fi

        ASSET=$(rename "${ARCHIVE}" "${PNAME}" "${VERSION}" "${OS}" "${ARCH}")

        if [[ "${DRY_RUN}" == "true" ]]; then
            info "dry run: skipping asset upload"
        else
            if ! release_asset "${TYPE}" "${TAG}" "${ASSET}"; then
                warn "uploading failed"
            fi
        fi

        delete "${ASSET}"

    # `mkDerivation` AppImage bundle
    elif
        [[ -n "${PNAME}" ]] &&
        [[ -n "${VERSION}" ]] &&
        [[ "${OS}" == "linux" ]];
    then
        info "bundling as AppImage"

        if ! ARCHIVE=$(nix_bundle_appimage "${PACKAGE}"); then
            warn "bundling failed"
            continue
        fi

        ASSET=$(rename "${ARCHIVE}" "${PNAME}" "${VERSION}" "${OS}" "${ARCH}")

        if [[ "${DRY_RUN}" == "true" ]]; then
            info "dry run: skipping asset upload"
        else
            if ! release_asset "${TYPE}" "${TAG}" "${ASSET}"; then
                warn "uploading failed"
            fi
        fi

        delete "${ASSET}"
    else
        warn "unknown package type"
    fi
done

info ""

# create and push manifest
if [[ "${IMAGES-}" == "true" ]]; then
    if [[ "${DRY_RUN}" == "true" ]]; then
        info "dry run: skipping manifest update"
    else
        info "updating image manifest for tag $(bold "${TAG#v}")"
        manifest_update "${TAG#v}"
    fi
fi

# logout
if [[ "${TYPE}" == "gitea" ]]; then
    gitea_logout
elif [[ "${TYPE}" == "forgejo" ]]; then
    forgejo_logout
fi

# cleanup
delete ~/.config/tea  # gitea tea
delete "${CHANGELOG}" # changelog
