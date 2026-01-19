#!/usr/bin/env bash

# logs in to Gitea using the GITHUB_TOKEN
function gitea_login () {
    if [[ -n ${GITHUB_TOKEN-} && -n ${GITHUB_SERVER_URL} ]]; then
        info "logging in to ${GITHUB_SERVER_URL}"
        run tea login add --name gitea --url "${GITHUB_SERVER_URL}" --token "${GITHUB_TOKEN}"
        run tea login default gitea
    fi
}

# creates a Gitea release if it does not exist
function gitea_release () {
    local version="$1"
    local changelog="$2"

    if [[ -n ${GITHUB_TOKEN-} && -n "${GITHUB_REPOSITORY}" ]]; then
        info "creating release v$version at $GITHUB_REPOSITORY"
        run tea release create \
            --title "v$version" \
            --note "$changelog" \
            --repo "$GITHUB_REPOSITORY" \
            "v$version"
    fi
}

function gitea_release_asset () {
    local version="$1"
    local asset="$2"

    if [[ -n ${GITHUB_TOKEN-} && -n "${GITHUB_REPOSITORY}" ]]; then
        info "uploading asset to release v${version} at $GITHUB_REPOSITORY"
        run tea release assets create --repo "$GITHUB_REPOSITORY" "v${version}" "${asset}"
    fi
}