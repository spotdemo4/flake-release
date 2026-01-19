#!/usr/bin/env bash

# creates a GitHub release if it does not exist
function github_release () {
    local version="$1"
    local changelog="$2"

    if [[ -n ${GITHUB_TOKEN-} && -n "${GITHUB_REPOSITORY}" ]]; then
        info "creating release v$version at $GITHUB_REPOSITORY"
        run gh release create \
            --title "v$version" \
            --notes "$changelog" \
            --repo "$GITHUB_REPOSITORY" \
            "v$version"
    fi
}

# uploads a file to a GitHub release
function github_release_asset () {
    local version="$1"
    local asset="$2"

    if [[ -n ${GITHUB_TOKEN-} && -n "${GITHUB_REPOSITORY}" ]]; then
        info "uploading asset to release v$version at $GITHUB_REPOSITORY"
        run gh release upload --repo "$GITHUB_REPOSITORY" "v$version" "$asset" --clobber
    fi
}
