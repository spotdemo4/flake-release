#!/usr/bin/env bash

# creates a GitHub release if it does not exist
function github_release_create () {
    local version="$1"

    if [[ -n ${GITHUB_TOKEN-} && -n ${GITHUB_REPOSITORY-} ]]; then
        info "creating release v$version at $GITHUB_REPOSITORY"
        run gh release create --repo "$GITHUB_REPOSITORY" "v$version" --generate-notes || true
    fi
}

# uploads a file to a GitHub release
function github_upload_file () {
    local file="$1"
    local version="$2"

    if [[ -n ${GITHUB_TOKEN-} && -n ${GITHUB_REPOSITORY-} ]]; then
        github_release_create "$version"

        info "uploading to release v$version at $GITHUB_REPOSITORY"
        run gh release upload --repo "$GITHUB_REPOSITORY" "v$version" "$file" --clobber
    fi
}
