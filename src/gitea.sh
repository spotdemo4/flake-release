#!/usr/bin/env bash

function gitea_login() {
    if [[ -z "${GITHUB_SERVER_URL-}" ]]; then
        warn "GITHUB_SERVER_URL is not set, cannot login to Gitea"
        return 1
    fi

    if [[ -z "${GITHUB_TOKEN-}" ]]; then
        warn "GITHUB_TOKEN is not set, cannot login to Gitea"
        return 1
    fi

    if [[ -z "${GITHUB_ACTOR-}" ]]; then
        warn "GITHUB_ACTOR is not set, cannot login to Gitea"
        return 1
    fi

    info "logging in to ${GITHUB_SERVER_URL}"
    run tea login add --name "${GITHUB_ACTOR}" --url "${GITHUB_SERVER_URL}" --token "${GITHUB_TOKEN}" || true
    run tea login default "${GITHUB_ACTOR}"
}

function gitea_logout() {
    if [[ -z "${GITHUB_ACTOR-}" ]]; then
        warn "GITHUB_ACTOR is not set, cannot logout of Gitea"
        return 1
    fi

    info "logging out of Gitea"
    run tea login delete "${GITHUB_ACTOR}"
}

function gitea_release() {
    local tag="$1"
    local changelog="$2"

    if [[ -z "${GITHUB_REPOSITORY-}" ]]; then
        warn "GITHUB_REPOSITORY is not set, cannot create Gitea release"
        return 1
    fi

    info "creating release ${tag} at ${GITHUB_REPOSITORY}"
    run tea release create \
        --title "${tag}" \
        --note-file "${changelog}" \
        --repo "${GITHUB_REPOSITORY}" \
        "${tag}"
}

function gitea_release_asset() {
    local tag="$1"
    local asset="$2"

    if [[ -z "${GITHUB_REPOSITORY-}" ]]; then
        warn "GITHUB_REPOSITORY is not set, cannot upload asset to Gitea"
        return 1
    fi

    info "uploading asset to release ${tag} at ${GITHUB_REPOSITORY}"
    run tea release assets create --repo "${GITHUB_REPOSITORY}" "${tag}" "${asset}"
}

function gitea_api_release_cleanup_assets() {
    local type="$1"
    local current_tag="$2"
    local provider
    provider="${type^}"

    if [[ -z "${GITHUB_SERVER_URL-}" ]]; then
        warn "GITHUB_SERVER_URL is not set, cannot delete old ${provider} release assets"
        return 1
    fi

    if [[ -z "${GITHUB_REPOSITORY-}" ]]; then
        warn "GITHUB_REPOSITORY is not set, cannot delete old ${provider} release assets"
        return 1
    fi

    if [[ -z "${GITHUB_TOKEN-}" ]]; then
        warn "GITHUB_TOKEN is not set, cannot delete old ${provider} release assets"
        return 1
    fi

    local server
    server="${GITHUB_SERVER_URL%/}"

    local page=1
    local limit=100
    local releases
    local count
    local failed=false

    info "deleting old ${provider} release assets at ${GITHUB_REPOSITORY}"

    while true; do
        if ! releases=$(curl --fail --silent --show-error \
            --header "Authorization: token ${GITHUB_TOKEN}" \
            --header "Accept: application/json" \
            "${server}/api/v1/repos/${GITHUB_REPOSITORY}/releases?page=${page}&limit=${limit}"); then
            warn "failed to fetch ${provider} releases"
            return 1
        fi

        if ! count=$(echo "${releases}" | jq 'length'); then
            warn "failed to parse ${provider} releases"
            return 1
        fi

        if [[ "${count}" -eq 0 ]]; then
            break
        fi

        local release_id
        local release_tag
        local asset_id
        local asset_name

        while IFS=$'\t' read -r release_id release_tag asset_id asset_name; do
            if [[ -z "${asset_id}" ]]; then
                continue
            fi

            info "deleting asset ${asset_name} from release ${release_tag}"
            if ! curl --fail --silent --show-error \
                --request DELETE \
                --header "Authorization: token ${GITHUB_TOKEN}" \
                --header "Accept: application/json" \
                --output /dev/null \
                "${server}/api/v1/repos/${GITHUB_REPOSITORY}/releases/${release_id}/assets/${asset_id}"; then
                warn "failed to delete asset ${asset_name} from release ${release_tag}"
                failed=true
            fi
        done < <(echo "${releases}" | jq -r --arg current_tag "${current_tag}" '
            .[]
            | . as $release
            | ($release.tag_name // $release.tagName // $release.tag // "") as $release_tag
            | select($release_tag != $current_tag)
            | ($release.assets // [])[]
            | [($release.id | tostring), $release_tag, (.id | tostring), .name]
            | @tsv
        ')

        if [[ "${count}" -lt "${limit}" ]]; then
            break
        fi

        page=$((page + 1))
    done

    if [[ "${failed}" == "true" ]]; then
        return 1
    fi
}
