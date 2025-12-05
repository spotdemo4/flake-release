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

NIX_SYSTEM=$(nix_system)
readarray -t PACKAGES < <(nix_packages "$NIX_SYSTEM")

STORE_PATHS=()
for PACKAGE in "${PACKAGES[@]}"; do
    echo "$PACKAGE: evaluating" 

    STORE_PATH=$(nix_pkg_path "$PACKAGE")
    if [[ ${STORE_PATHS[*]} =~ $STORE_PATH ]]; then
        echo "$PACKAGE: already built, skipping"
        continue
    else
        STORE_PATHS+=("$STORE_PATH")
    fi

    NAME=$(nix_pkg_name "$PACKAGE")
    VERSION=$(nix_pkg_version "$PACKAGE")
    echo "$PACKAGE: building '$NAME' '$VERSION'"
    nix_pkg_build "$PACKAGE"

    echo "$PACKAGE: probing '$NAME'"
    IMAGE_NAME=$(nix_pkg_image_name "$PACKAGE")
    IMAGE_TAG=$(nix_pkg_image_tag "$PACKAGE")
    EXE=$(nix_pkg_exe "$PACKAGE")
    PLATFORM=$(detect_platform "$EXE")

    if [[ -f "$STORE_PATH" && -n $IMAGE_NAME && -n $IMAGE_TAG ]]; then
        echo "$PACKAGE: detected as image"

        echo "$PACKAGE: uploading"
        github_upload_image "$STORE_PATH" "$IMAGE_TAG"

    elif [[ -d "$STORE_PATH" && -f "$EXE" && "$PLATFORM" != "unknown-unknown" ]]; then
        echo "$PACKAGE: detected as executable '$(basename "$EXE")' for '$PLATFORM'"

        echo "$PACKAGE: archiving"
        ARCHIVE=$(archive "$EXE" "$NAME-$PLATFORM" "$PLATFORM")

        echo "$PACKAGE: uploading"
        github_upload_file "$ARCHIVE" "$VERSION"

    elif [[ -d "$STORE_PATH" && -f "$EXE" ]]; then
        echo "$PACKAGE: detected as script '$(basename "$EXE")'"

        echo "$PACKAGE: bundling"
        BUNDLE=$(nix_bundle "$PACKAGE")

        echo "$PACKAGE: archiving"
        ARCHIVE=$(archive "$BUNDLE" "$NAME" "$(host_platform)")

        echo "$PACKAGE: uploading"
        github_upload_file "$ARCHIVE" "$VERSION"

    else
        echo "$PACKAGE: unknown package type"
    fi

    echo "$PACKAGE: done"
done
