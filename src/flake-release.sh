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
source "$DIR/forgejo.sh"
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

# git type
if ! TYPE=$(release_type); then
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
if ! release "${TYPE}" "${TAG}" "${CHANGELOG}"; then
    warn "could not create release ${TAG}"
fi

# get nix packages
NIX_SYSTEM=$(nix_system)
readarray -t PACKAGES < <(nix_packages "${NIX_SYSTEM}")
if [[ ${#PACKAGES[@]} -eq 0 ]]; then
    warn "no packages found in the nix flake for system '${NIX_SYSTEM}'"
fi

# build and upload assets
STORE_PATHS=()
for PACKAGE in "${PACKAGES[@]}"; do
    info ""

    if [[ "${#ARGS[@]}" -ne 0 && ! ${ARGS[*]} =~ ${PACKAGE} ]]; then
        info "skipping package '${PACKAGE}'"
        continue
    fi

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

    # `dockerTools` attributes
    IMAGE_NAME=$(nix_pkg_image_name "${PACKAGE}")
    IMAGE_TAG=$(nix_pkg_image_tag "${PACKAGE}")

    if [[ "${VERSION}" != "${TAG#v}" && "${IMAGE_TAG}" != "${TAG#v}" ]]; then
        warn "package version '${VERSION:-"${IMAGE_TAG}"}' does not match git tag '${TAG#v}'"
        continue
    fi

    # `dockerTools.buildLayeredImage`
    if
        [[ -n "${IMAGE_NAME}" ]] &&
        [[ -n "${IMAGE_TAG}" ]] &&
        [[ -f "${STORE_PATH}" ]] &&
        [[ "${STORE_PATH}" == *".tar.gz" ]];
    then
        info "detected as image $(bold "${IMAGE_NAME}:${IMAGE_TAG}")"

        IMAGES="true"
        IMAGE_ARCH=$(image_arch "${STORE_PATH}")
        info "arch: ${IMAGE_ARCH}"

        if image_exists "${IMAGE_TAG}" "${IMAGE_ARCH}"; then
            warn "image already exists, skipping upload"
            continue
        fi

        if ! image_upload "${STORE_PATH}" "${IMAGE_TAG}" "${IMAGE_ARCH}"; then
            warn "upload failed"
            continue
        fi

    # `dockerTools.streamLayeredImage`
    elif
        [[ -n "${IMAGE_NAME}" ]] &&
        [[ -n "${IMAGE_TAG}" ]] &&
        [[ -f "${STORE_PATH}" ]] &&
        [[ -x "${STORE_PATH}" ]];
    then
        info "detected as image stream $(bold "${IMAGE_NAME}:${IMAGE_TAG}")"

        IMAGES="true"
        IMAGE_ZIPPED=$(image_gzip "${STORE_PATH}")
        IMAGE_ARCH=$(image_arch "${IMAGE_ZIPPED}")
        info "arch: ${IMAGE_ARCH}"

        if image_exists "${IMAGE_TAG}" "${IMAGE_ARCH}"; then
            info "image already exists, skipping upload"
            continue
        fi

        if ! image_upload "${STORE_PATH}" "${IMAGE_TAG}" "${IMAGE_ARCH}"; then
            warn "upload failed"
            continue
        fi

    # `mkDerivation` executable(s)
    elif
        [[ -n "${PNAME}" ]] &&
        [[ -n "${VERSION}" ]] &&
        [[ -d "${STORE_PATH}" ]] &&
        [[ -n "$(only_bins "${STORE_PATH}")" ]];
    then
        info "detected as executable $(bold "${PNAME}")"

        OS=$(detect_os "${STORE_PATH}")
        info "os: ${OS}"

        ARCH=$(detect_arch "${STORE_PATH}")
        info "arch: ${ARCH}"

        if ! ARCHIVE=$(archive "${STORE_PATH}" "${OS}"); then
            warn "archiving failed"
            continue
        fi

        ASSET=$(rename "${ARCHIVE}" "${PNAME}" "${VERSION}" "${OS}" "${ARCH}")

        if ! release_asset "${TYPE}" "${TAG}" "${ASSET}"; then
            warn "uploading failed"
            continue
        fi

    # `mkDerivation` bundle
    elif
        [[ -n "${PNAME}" ]] &&
        [[ -n "${VERSION}" ]] &&
        [[ -d "${STORE_PATH}" ]];
    then
        info "detected as bundle $(bold "${PNAME}")"

        if [[ -z "${BUNDLE-}" ]]; then
            warn "BUNDLE is not set, no bundle type specified"
            continue
        fi

        if ! ARCHIVE=$(nix_bundle "${PACKAGE}" "${BUNDLE}"); then
            warn "bundling failed"
            continue
        fi

        ASSET=$(rename "${ARCHIVE}" "${PNAME}" "${VERSION}" "$(host_os)" "$(host_arch)")

        if ! release_asset "${TYPE}" "${TAG}" "${ASSET}"; then
            warn "uploading failed"
            continue
        fi

    else
        warn "unknown package type"
    fi
done

info ""

# create and push manifest
if [[ "${IMAGES-}" == "true" ]]; then
    info "updating image manifest for tag $(bold "${TAG#v}")"
    manifest_update "${TAG#v}"
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
