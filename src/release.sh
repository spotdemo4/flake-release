#!/usr/bin/env bash

function release () {
    local version="$1"
    local changelog="$2"

    if [[ -n "${GITEA_ACTIONS-}" ]]; then
        if ! gitea_release "$version" "$changelog"; then
            warn "release failed"
        fi
    elif [[ -n "${FORGEJO_ACTIONS-}" ]]; then
        echo "forgejo is not supported yet"
    else
        if ! github_release "$version" "$changelog"; then
            warn "release failed"
        fi
    fi
}

function release_asset () {
    local version="$1"
    local asset="$2"

    if [[ -n "${GITEA_ACTIONS-}" ]]; then
        if ! gitea_release_asset "$version" "$asset"; then
            warn "uploading failed"
        fi
    elif [[ -n "${FORGEJO_ACTIONS-}" ]]; then
        echo "forgejo is not supported yet"
    else
        if ! github_release_asset "$version" "$asset"; then
            warn "uploading failed"
        fi
    fi
}