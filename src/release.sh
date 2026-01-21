#!/usr/bin/env bash

function release () {
    local tag="$1"
    local changelog="$2"

    if [[ -n "${GITEA_ACTIONS-}" ]]; then
        gitea_release "${tag}" "${changelog}"
    elif [[ -n "${FORGEJO_ACTIONS-}" ]]; then
        echo "forgejo is not supported yet"
    else
        github_release "${tag}" "${changelog}"
    fi
}

function release_asset () {
    local tag="$1"
    local asset="$2"

    if [[ -n "${GITEA_ACTIONS-}" ]]; then
        gitea_release_asset "${tag}" "${asset}"
    elif [[ -n "${FORGEJO_ACTIONS-}" ]]; then
        echo "forgejo is not supported yet"
    else
        github_release_asset "${tag}" "${asset}"
    fi

    rm -rf "${asset}"
}