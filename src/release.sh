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

    echo "$PACKAGE: building"
    nix_pkg_build "$PACKAGE"

    echo "$PACKAGE: probing"
    # `mkDerivation`` attributes
    NAME=$(nix_pkg_name "$PACKAGE")
    VERSION=$(nix_pkg_version "$PACKAGE")
    EXE=$(nix_pkg_exe "$PACKAGE")
    PLATFORM=$(detect_platform "$EXE")

    # `dockerTools.buildLayeredImage` attributes
    IMAGE_NAME=$(nix_pkg_image_name "$PACKAGE")
    IMAGE_TAG=$(nix_pkg_image_tag "$PACKAGE")

    if [[ -n $IMAGE_NAME && -n $IMAGE_TAG && -f "$STORE_PATH" ]]; then
        echo "$PACKAGE: detected as image '$IMAGE_NAME:$IMAGE_TAG'"

        echo "$PACKAGE: uploading"
        github_upload_image "$STORE_PATH" "$IMAGE_TAG"

    elif [[ -n $NAME && -n $VERSION && -d "$STORE_PATH" && -f "$EXE" && "$PLATFORM" != "unknown-unknown" ]]; then
        echo "$PACKAGE: detected as executable '$(basename "$EXE")' for '$PLATFORM'"

        echo "$PACKAGE: archiving"
        ARCHIVE=$(archive "$EXE" "$NAME-$PLATFORM" "$PLATFORM")

        echo "$PACKAGE: uploading"
        github_upload_file "$ARCHIVE" "$VERSION"

    elif [[ -n $NAME && -n $VERSION && -d "$STORE_PATH" ]]; then
        echo "$PACKAGE: detected as derivation '${NAME}'"

        echo "$PACKAGE: bundling"
        BUNDLE=$(nix_bundle "$PACKAGE")

        echo "$PACKAGE: archiving"
        ARCHIVE=$(archive "$BUNDLE" "$NAME" "$(host_platform)")

        echo "$PACKAGE: uploading"
        github_upload_file "$ARCHIVE" "$VERSION"

    else
        echo "$PACKAGE: unknown type"
    fi

    echo "$PACKAGE: done"
done
