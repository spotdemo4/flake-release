#!/usr/bin/env bash
# export PATH="${PATH}" placeholder, will be replaced by nix

set -e

# make source imports work
DIR="${BASH_SOURCE%/*}"
if [[ ! -d "$DIR" ]]; then DIR="$PWD"; fi

source "$DIR/github.sh"
source "$DIR/nix.sh"
source "$DIR/platform.sh"
source "$DIR/util.sh"

github_release_create

BUILD_DIR=$(mktemp -d)
NIX_SYSTEM=$(nix_system)
readarray -t PACKAGES < <(nix_packages "$NIX_SYSTEM")

STORE_PATHS=()
IMAGE_URLS=()
for PACKAGE in "${PACKAGES[@]}"; do
    echo "$PACKAGE: evaluating" 

    STORE_PATH=$(nix_pkg_path "${PACKAGE}")
    if [[ ${STORE_PATHS[*]} =~ $STORE_PATH ]]; then
        echo "$PACKAGE: already built, skipping"
        continue
    else
        STORE_PATHS+=("$STORE_PATH")
    fi

    NAME=$(nix_pkg_name "${PACKAGE}")
    echo "$PACKAGE: building '$NAME'"
    nix_pkg_build "${PACKAGE}"

    echo "$PACKAGE: probing '$NAME'"
    IMAGE_NAME=$(nix_pkg_image_name "${PACKAGE}")
    IMAGE_TAG=$(nix_pkg_image_tag "${PACKAGE}")
    EXE=$(nix_pkg_exe "${PACKAGE}")
    PLATFORM=$(detect_platform "$EXE")

    if [[ -f "$STORE_PATH" && -n $IMAGE_NAME && -n $IMAGE_TAG ]]; then
        echo "$PACKAGE: detected as docker image"

        echo "$PACKAGE: loading to '$IMAGE_NAME:$IMAGE_TAG'"
        docker load -i "$STORE_PATH" &> /dev/null

        echo "$PACKAGE: uploading"
        IMAGE_URL=$(github_upload_image "${IMAGE_NAME}" "${IMAGE_TAG}")
        IMAGE_URLS+=("$IMAGE_URL")

    elif [[ -d "$STORE_PATH" && -f "$EXE" && "$PLATFORM" != "unknown-unknown" ]]; then
        echo "$PACKAGE: detected as executable '$(basename "$EXE")' for '$PLATFORM'"

        echo "$PACKAGE: archiving"
        ARCHIVE=$(archive "$EXE" "$BUILD_DIR/${NAME}-${PLATFORM}" "$PLATFORM")

        echo "$PACKAGE: uploading"
        github_upload_file "$ARCHIVE"
    else
        echo "$PACKAGE: unknown package type"
    fi

    echo "$PACKAGE: done"
done

github_upload_manifest "${IMAGE_URLS[@]}"
