#!/usr/bin/env bash

# creates a GitHub release if it does not exist
function github_release () {
    local tag="$1"
    local changelog="$2"

    if [[ -n ${GITHUB_TOKEN-} && -n "${GITHUB_REPOSITORY-}" ]]; then
    info "creating release ${tag} at ${GITHUB_REPOSITORY}"
        run gh release create \
            --title "${tag}" \
            --notes-file "${changelog}" \
            --repo "${GITHUB_REPOSITORY}" \
            "${tag}"
    fi
}

# uploads a file to a GitHub release
function github_release_asset () {
    local tag="$1"
    local asset="$2"

    if [[ -n ${GITHUB_TOKEN-} && -n "${GITHUB_REPOSITORY-}" ]]; then
        info "uploading asset to release ${tag} at $GITHUB_REPOSITORY"
        run gh release upload --repo "${GITHUB_REPOSITORY}" --clobber "${tag}" "$asset"
    fi
}
