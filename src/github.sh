#!/usr/bin/env bash

if [[ -n $GITHUB_TOKEN && -n $GITHUB_ACTOR ]]; then
    echo "logging into ghcr.io"
    echo "${GITHUB_TOKEN}" | docker login ghcr.io -u "$GITHUB_ACTOR" --password-stdin
fi

function github_release_create () {
    if [[ -n $GITHUB_TOKEN && -n $GITHUB_REPOSITORY && -n $GITHUB_REF_NAME && $GITHUB_REF_TYPE == "tag" ]]; then
        gh release create --repo "$GITHUB_REPOSITORY" "$GITHUB_REF_NAME" --generate-notes &> /dev/null || true
    fi
}

# uploads a file to GitHub Releases
function github_upload_file () {
    local file="$1"

    if [[ -n $GITHUB_TOKEN && -n $GITHUB_REF_NAME && $GITHUB_REF_TYPE == "tag" ]]; then
        gh release upload "$GITHUB_REF_NAME" "$file" --clobber &> /dev/null
    fi
}

# uploads a docker image to the GitHub Container Registry
function github_upload_image () {
    local name="$1"
    local tag="$2"

    if [[ -n $GITHUB_TOKEN && -n $GITHUB_ACTOR && -n $GITHUB_REPOSITORY ]]; then
        local IMAGE_URL="ghcr.io/${GITHUB_REPOSITORY}:$tag"
        docker tag "$name:$tag" "$IMAGE_URL" &> /dev/null
        docker push "$IMAGE_URL" &> /dev/null

        echo "${IMAGE_URL}"
    fi
}

# creates and pushes a multi-arch manifest to the GitHub Container Registry
function github_upload_manifest () {
    local images=("$@")

    if [[ -n $GITHUB_TOKEN && -n $GITHUB_ACTOR && -n $GITHUB_REPOSITORY && -n $GITHUB_REF_NAME && $GITHUB_REF_TYPE == "tag" ]]; then
        LATEST="ghcr.io/${GITHUB_REPOSITORY}:latest"

        # if there are multiple images, create a manifest
        if [[ ${#images[@]} -gt 1 ]]; then
            MANIFEST="ghcr.io/${GITHUB_REPOSITORY}:${GITHUB_REF_NAME#v}"

            for IMAGE in "${images[@]}"; do
                docker manifest create --amend "${LATEST}" "${IMAGE}" &> /dev/null
                docker manifest create --amend "${MANIFEST}" "${IMAGE}" &> /dev/null
            done

            docker manifest push "${LATEST}" &> /dev/null
            docker manifest push "${MANIFEST}" &> /dev/null

        # else just tag and push the single image as latest
        elif [[ ${#images[@]} -eq 1 ]]; then
            docker tag "${images[0]}" "${LATEST}" &> /dev/null
            docker push "${LATEST}" &> /dev/null
        fi
    fi

    if [[ ${#images[@]} -gt 1 && -n $GITHUB_TOKEN && -n $GITHUB_ACTOR && -n $GITHUB_REPOSITORY && -n $GITHUB_REF_NAME && $GITHUB_REF_TYPE == "tag" ]]; then
        NEXT="ghcr.io/${GITHUB_REPOSITORY}:${GITHUB_REF_NAME#v}"
        LATEST="ghcr.io/${GITHUB_REPOSITORY}:latest"

        for IMAGE in "${images[@]}"; do
            docker manifest create --amend "${NEXT}" "${IMAGE}" &> /dev/null
            docker manifest create --amend "${LATEST}" "${IMAGE}" &> /dev/null
        done

        docker manifest push "${NEXT}" &> /dev/null
        docker manifest push "${LATEST}" &> /dev/null
    fi
}