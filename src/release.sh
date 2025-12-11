#!/usr/bin/env bash
# export PATH="${PATH}" placeholder, will be replaced in release

set -o errexit
set -o nounset
set -o pipefail

# make source imports work
DIR="${BASH_SOURCE%/*}"
if [[ ! -d "$DIR" ]]; then DIR="$PWD"; fi

source "$DIR/github.sh"
source "$DIR/image.sh"
source "$DIR/nix.sh"
source "$DIR/platform.sh"
source "$DIR/util.sh"

ARGS=( "$@" )

NIX_SYSTEM=$(nix_system)
readarray -t PACKAGES < <(nix_packages "$NIX_SYSTEM")
if [[ ${#PACKAGES[@]} -eq 0 ]]; then
    print "no packages found in the nix flake for system '$NIX_SYSTEM'"
    exit 1
fi

echo "" >&2

STORE_PATHS=()
for PACKAGE in "${PACKAGES[@]}"; do
    if [[ "${#ARGS[@]}" -ne 0 && ! ${ARGS[*]} =~ $PACKAGE ]]; then
        echo "skipping package '$PACKAGE'" >&2
        continue
    fi

    print "evaluating '$PACKAGE'"
    STORE_PATH=$(nix_pkg_path "$PACKAGE")
    if [[ ${STORE_PATHS[*]} =~ $STORE_PATH ]]; then
        echo "$PACKAGE: already built, skipping" >&2
        continue
    else
        STORE_PATHS+=("$STORE_PATH")
    fi

    print "building"
    nix_build "$PACKAGE"

    print "probing"
    # `mkDerivation`` attributes
    NAME=$(nix_pkg_name "$PACKAGE")
    VERSION=$(nix_pkg_version "$PACKAGE")
    EXE=$(nix_pkg_exe "$PACKAGE")
    PLATFORM=$(detect_platform "$EXE")
    # `dockerTools` attributes
    IMAGE_NAME=$(nix_pkg_image_name "$PACKAGE")
    IMAGE_TAG=$(nix_pkg_image_tag "$PACKAGE")

    # `dockerTools.buildLayeredImage`
    if [[ -n $IMAGE_NAME && -n $IMAGE_TAG && -f "$STORE_PATH" && "$STORE_PATH" == *".tar.gz" ]]; then
        echo "detected as image '$IMAGE_NAME:$IMAGE_TAG'" >&2

        print "uploading"
        upload_image "$STORE_PATH" "$IMAGE_TAG"

    # `dockerTools.streamLayeredImage`
    elif [[ -n $IMAGE_NAME && -n $IMAGE_TAG && -f "$STORE_PATH" && -x "$STORE_PATH" ]]; then
        echo "detected as image '$IMAGE_NAME:$IMAGE_TAG'" >&2

        print "streaming"
        stream_image "$STORE_PATH" "$IMAGE_TAG"

    # `mkDerivation`` executable
    elif [[ -n $NAME && -n $VERSION && -d "$STORE_PATH" && -f "$EXE" && "$PLATFORM" != "unknown-unknown" ]]; then
        echo "detected as executable '$(basename "$EXE")' for '$PLATFORM'" >&2

        print "archiving"
        ARCHIVE=$(archive "$EXE" "$NAME-$PLATFORM" "$PLATFORM")

        print "uploading"
        github_upload_file "$ARCHIVE" "$VERSION"

    # `mkDerivation`` non-executable
    elif [[ -n $NAME && -n $VERSION && -d "$STORE_PATH" ]]; then
        echo "detected as derivation '${NAME}'" >&2

        print "bundling"
        BUNDLE=$(nix_bundle "$PACKAGE")

        print "archiving"
        ARCHIVE=$(archive "$BUNDLE" "$NAME" "$(host_platform)")

        print "uploading"
        github_upload_file "$ARCHIVE" "$VERSION"

    else
        echo "unknown type" >&2
    fi

    if [[ "${CI-}" == "true" ]]; then
        printf "::endgroup::\n" >&2
    else
        echo "" >&2
    fi
done
