#!/usr/bin/env bash

function release() {
    local type="$1"
    local tag="$2"
    local changelog="$3"

    if [[ "${type}" == "gitea" ]]; then
        gitea_release "${tag}" "${changelog}"
    elif [[ "${type}" == "forgejo" ]]; then
        forgejo_release "${tag}" "${changelog}"
    elif [[ "${type}" == "github" ]]; then
        github_release "${tag}" "${changelog}"
    fi
}

function release_asset() {
    local type="$1"
    local tag="$2"
    local asset="$3"

    if [[ "${type}" == "gitea" ]]; then
        gitea_release_asset "${tag}" "${asset}"
    elif [[ "${type}" == "forgejo" ]]; then
        forgejo_release_asset "${tag}" "${asset}"
    elif [[ "${type}" == "github" ]]; then
        github_release_asset "${tag}" "${asset}"
    fi
}

function release_type() {
    local origin="$1"

    if [[ "${GIT_TYPE-}" == "gitea" ]]; then
        echo "gitea"
        return 0
    elif [[ "${GIT_TYPE-}" == "forgejo" ]]; then
        echo "forgejo"
        return 0
    elif [[ "${GIT_TYPE-}" == "github" ]]; then
        echo "github"
        return 0
    fi

    if [[ -n "${GITEA_ACTIONS-}" ]]; then
        echo "gitea"
        return 0
    elif [[ -n "${FORGEJO_ACTIONS-}" ]]; then
        echo "forgejo"
        return 0
    elif [[ -n "${GITHUB_ACTIONS-}" ]]; then
        echo "github"
        return 0
    fi

    if [[ -n "${origin}" ]]; then
        if [[ "${origin}" == *"gitea"* ]]; then
            echo "gitea"
            return 0
        elif [[ "${origin}" == *"forgejo"* ]]; then
            echo "forgejo"
            return 0
        elif [[ "${origin}" == *"github"* ]]; then
            echo "github"
            return 0
        fi
    fi

    warn "unknown release type"
    return 1
}
