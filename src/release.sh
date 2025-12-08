#!/usr/bin/env bash
# export PATH="${PATH}" placeholder, will be replaced in release

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
if [[ ${#PACKAGES[@]} -eq 0 ]]; then
    print "no packages found in the nix flake for system '$NIX_SYSTEM'"
    exit 1
fi

STORE_PATHS=()
for PACKAGE in "${PACKAGES[@]}"; do
    GLOBAL_PACKAGE="$PACKAGE"

    print "evaluating" 
    STORE_PATH=$(nix_pkg_path "$PACKAGE")
    if [[ ${STORE_PATHS[*]} =~ $STORE_PATH ]]; then
        print "$PACKAGE: already built, skipping"
        continue
    else
        STORE_PATHS+=("$STORE_PATH")
    fi

    nix_build "$PACKAGE"

    print "probing"
    # `mkDerivation`` attributes
    NAME=$(nix_pkg_name "$PACKAGE")
    VERSION=$(nix_pkg_version "$PACKAGE")
    EXE=$(nix_pkg_exe "$PACKAGE")
    PLATFORM=$(detect_platform "$EXE")
    # `dockerTools.buildLayeredImage` attributes
    IMAGE_NAME=$(nix_pkg_image_name "$PACKAGE")
    IMAGE_TAG=$(nix_pkg_image_tag "$PACKAGE")

    if [[ -n $IMAGE_NAME && -n $IMAGE_TAG && -f "$STORE_PATH" ]]; then
        print "detected as image '$IMAGE_NAME:$IMAGE_TAG'"

        github_upload_image "$STORE_PATH" "$IMAGE_TAG"

    elif [[ -n $NAME && -n $VERSION && -d "$STORE_PATH" && -f "$EXE" && "$PLATFORM" != "unknown-unknown" ]]; then
        print "detected as executable '$(basename "$EXE")' for '$PLATFORM'"

        ARCHIVE=$(archive "$EXE" "$NAME-$PLATFORM" "$PLATFORM")
        print "$(file_info "$ARCHIVE")"

        github_upload_file "$ARCHIVE" "$VERSION"

    elif [[ -n $NAME && -n $VERSION && -d "$STORE_PATH" ]]; then
        print "detected as derivation '${NAME}'"

        BUNDLE=$(nix_bundle "$PACKAGE")
        ARCHIVE=$(archive "$BUNDLE" "$NAME" "$(host_platform)")
        print "$(file_info "$ARCHIVE")"

        github_upload_file "$ARCHIVE" "$VERSION"

    else
        print "unknown type"
    fi

    print "done"
done
