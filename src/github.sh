#!/usr/bin/env bash

if [[ -n $GITHUB_TOKEN && -n $GITHUB_REPOSITORY ]]; then
    gh auth login --with-token <<< "$GITHUB_TOKEN" &> /dev/null
fi

function github_release_create () {
    local version="$1"

    if [[ -n $GITHUB_TOKEN && -n $GITHUB_REPOSITORY ]]; then
        gh release create --repo "$GITHUB_REPOSITORY" "v$version" --generate-notes &> /dev/null || true
    fi
}

# uploads a file to GitHub Releases
function github_upload_file () {
    local file="$1"
    local version="$2"

    if [[ -n $GITHUB_TOKEN && -n $GITHUB_REPOSITORY ]]; then
        github_release_create "$version"
        gh release upload --repo "$GITHUB_REPOSITORY" "v$version" "$file" --clobber
    fi
}

# uploads a image to the GitHub Container Registry
function github_upload_image () {
    local path="$1"
    local tag="$2"

    if [[ -n $GITHUB_TOKEN && -n $GITHUB_ACTOR && -n $GITHUB_REPOSITORY ]]; then
        skopeo --insecure-policy copy \
            --dest-creds "${GITHUB_ACTOR}:${GITHUB_TOKEN}" \
            "docker-archive:${path}" "docker://ghcr.io/${GITHUB_REPOSITORY}:${tag}"
    fi
}