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

# login to gitea if needed
if [[ -n "${GITEA_ACTIONS-}" ]]; then
    gitea_login
fi

# get nix packages
NIX_SYSTEM=$(nix_system)
readarray -t PACKAGES < <(nix_packages "$NIX_SYSTEM")
if [[ ${#PACKAGES[@]} -eq 0 ]]; then
    warn "no packages found in the nix flake for system '$NIX_SYSTEM'"
    exit 1
fi

STORE_PATHS=()
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
    else
        STORE_PATHS+=("$STORE_PATH")
    fi

    if ! nix_build "$PACKAGE"; then
        warn "build failed"
        continue
    fi

    # `mkDerivation`` attributes
    NAME=$(nix_pkg_name "$PACKAGE")
    VERSION=$(nix_pkg_version "$PACKAGE")
    EXE=$(nix_pkg_exe "$PACKAGE")
    PLATFORM=$(detect_platform "$EXE")

    # `dockerTools` attributes
    IMAGE_NAME=$(nix_pkg_image_name "$PACKAGE")
    IMAGE_TAG=$(nix_pkg_image_tag "$PACKAGE")

    if ! release "${VERSION}" "$(git_changelog "${VERSION}")"; then
        warn "release failed"
    fi

    # `dockerTools.buildLayeredImage`
    if [[ -n $IMAGE_NAME && -n $IMAGE_TAG && -f "$STORE_PATH" && "$STORE_PATH" == *".tar.gz" ]]; then

        info "detected as image $(bold "$IMAGE_NAME:$IMAGE_TAG")"

        if ! upload_image "$STORE_PATH" "$IMAGE_TAG"; then
            warn "uploading failed"
            continue
        fi

    # `dockerTools.streamLayeredImage`
    elif [[ -n $IMAGE_NAME && -n $IMAGE_TAG && -f "$STORE_PATH" && -x "$STORE_PATH" ]]; then

        info "detected as image $(bold "$IMAGE_NAME:$IMAGE_TAG")"

        if ! stream_image "$STORE_PATH" "$IMAGE_TAG"; then
            warn "streaming failed"
            continue
        fi

    # `mkDerivation`` executable
    elif [[ -n $NAME && -n $VERSION && -d "$STORE_PATH" && -f "$EXE" && "$PLATFORM" != "unknown-unknown" ]]; then

        info "detected as executable $(bold "$(basename "$EXE")") for $PLATFORM"

        if ! ARCHIVE=$(archive "$EXE" "$NAME-$PLATFORM" "$PLATFORM"); then
            warn "archiving failed"
            continue
        fi

        release_asset "$VERSION" "$ARCHIVE"

    # `mkDerivation`` non-executable
    elif [[ -n $NAME && -n $VERSION && -d "$STORE_PATH" ]]; then

        info "detected as generic derivation $(bold "${NAME}")"

        if [[ "${BUNDLE-}" == "false" ]]; then
            info "skipping bundling as BUNDLE is set to false"
            BUNDLE="$STORE_PATH"
        else
            if ! BUNDLE=$(nix_bundle "$PACKAGE"); then
                warn "bundling failed"
                continue
            fi
        fi

        if ! ARCHIVE=$(archive "$BUNDLE" "$NAME" "$(host_platform)"); then
            warn "archiving failed"
            continue
        fi

        release_asset "$VERSION" "$ARCHIVE"

    else
        warn "unknown type"
    fi
done
