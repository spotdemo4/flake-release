#!/usr/bin/env bash

# logs in to Gitea using the GITHUB_TOKEN
function gitea_login () {
    if [[ -n ${GITHUB_TOKEN-} && -n ${GITHUB_SERVER_URL-} ]]; then
        info "logging in to ${GITHUB_SERVER_URL}"
        run tea login add --name gitea --url "${GITHUB_SERVER_URL}" --token "${GITHUB_TOKEN}" || true
        run tea login default gitea
    fi
}

# creates a Gitea release if it does not exist
function gitea_release () {
    local tag="$1"
    local changelog="$2"

    if [[ -n ${GITHUB_TOKEN-} && -n "${GITHUB_REPOSITORY-}" ]]; then
        info "creating release ${tag} at ${GITHUB_REPOSITORY}"
        run tea release create \
            --title "${tag}" \
            --note-file "${changelog}" \
            --repo "$GITHUB_REPOSITORY" \
            "${tag}"
    fi
}

function gitea_release_asset () {
    local tag="$1"
    local asset="$2"

    if [[ -n ${GITHUB_TOKEN-} && -n "${GITHUB_REPOSITORY-}" ]]; then
        info "uploading asset to release ${tag} at ${GITHUB_REPOSITORY}"
        run tea release assets create --repo "${GITHUB_REPOSITORY}" "${tag}" "${asset}"
    fi
}
