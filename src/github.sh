#!/usr/bin/env bash

# creates a GitHub release if it does not exist
function github_release() {
    local tag="$1"
    local changelog="$2"

    if [[ -z "${GITHUB_REPOSITORY-}" ]]; then
        warn "GITHUB_REPOSITORY is not set, cannot create GitHub release"
        return 1
    fi

    if [[ -z "${GITHUB_TOKEN-}" ]]; then
        warn "GITHUB_TOKEN is not set, cannot create GitHub release"
        return 1
    fi

    info "creating release ${tag} at ${GITHUB_REPOSITORY}"
    run gh release create \
        --title "${tag}" \
        --notes-file "${changelog}" \
        --repo "${GITHUB_REPOSITORY}" \
        "${tag}"
}

# uploads a file to a GitHub release
function github_release_asset() {
    local tag="$1"
    local asset="$2"

    if [[ -z "${GITHUB_REPOSITORY-}" ]]; then
        warn "GITHUB_REPOSITORY is not set, cannot upload asset to GitHub"
        return 1
    fi

    if [[ -z "${GITHUB_TOKEN-}" ]]; then
        warn "GITHUB_TOKEN is not set, cannot upload asset to GitHub"
        return 1
    fi

    info "uploading asset to release ${tag} at ${GITHUB_REPOSITORY}"
    run gh release upload --repo "${GITHUB_REPOSITORY}" "${tag}" "${asset}"
}

function github_release_cleanup_assets() {
    local current_tag="$1"

    if [[ -z "${GITHUB_REPOSITORY-}" ]]; then
        warn "GITHUB_REPOSITORY is not set, cannot delete old GitHub release assets"
        return 1
    fi

    if [[ -z "${GITHUB_TOKEN-}" ]]; then
        warn "GITHUB_TOKEN is not set, cannot delete old GitHub release assets"
        return 1
    fi

    local releases
    if ! releases=$(gh release list \
        --repo "${GITHUB_REPOSITORY}" \
        --limit 1000 \
        --json tagName \
        --jq '.[].tagName'); then
        warn "failed to fetch GitHub releases"
        return 1
    fi

    info "deleting old GitHub release assets at ${GITHUB_REPOSITORY}"

    if [[ -z "${releases}" ]]; then
        return 0
    fi

    local failed=false
    local release_tag
    local assets
    local asset

    while IFS= read -r release_tag; do
        if [[ -z "${release_tag}" || "${release_tag}" == "${current_tag}" ]]; then
            continue
        fi

        if ! assets=$(gh release view "${release_tag}" \
            --repo "${GITHUB_REPOSITORY}" \
            --json assets \
            --jq '.assets[].name'); then
            warn "failed to fetch GitHub release assets for ${release_tag}"
            failed=true
            continue
        fi

        if [[ -z "${assets}" ]]; then
            continue
        fi

        while IFS= read -r asset; do
            if [[ -z "${asset}" ]]; then
                continue
            fi

            info "deleting asset ${asset} from release ${release_tag}"
            if ! run gh release delete-asset \
                --repo "${GITHUB_REPOSITORY}" \
                "${release_tag}" \
                "${asset}" \
                --yes; then
                warn "failed to delete asset ${asset} from release ${release_tag}"
                failed=true
            fi
        done <<< "${assets}"
    done <<< "${releases}"

    if [[ "${failed}" == "true" ]]; then
        return 1
    fi
}
