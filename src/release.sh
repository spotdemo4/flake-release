#!/usr/bin/env bash

function release () {
    local version="$1"
    local changelog="$2"

    if [[ -n "${GITEA_ACTIONS-}" ]]; then
        gitea_release "$version" "$changelog"
    elif [[ -n "${FORGEJO_ACTIONS-}" ]]; then
        echo "forgejo is not supported yet"
    else
        github_release "$version" "$changelog"
    fi
}

function release_asset () {
    local version="$1"
    local asset="$2"

    if [[ -n "${GITEA_ACTIONS-}" ]]; then
        gitea_release_asset "$version" "$asset"
    elif [[ -n "${FORGEJO_ACTIONS-}" ]]; then
        echo "forgejo is not supported yet"
    else
        github_release_asset "$version" "$asset"
    fi
}