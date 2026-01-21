#!/usr/bin/env bash

# uploads a image to the container registry
function upload_image() {
    local path="$1"
    local tag="$2"
    local arch="$3"

    if [[ -n "${REGISTRY-}" && -n "${GITHUB_REPOSITORY-}" && -n "${REGISTRY_USERNAME-}" && -n "${REGISTRY_PASSWORD-}" ]]; then
        local image
        image="docker://${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}-${arch}"

        info "uploading to ${image}"
        run skopeo --insecure-policy copy \
            --dest-creds "${REGISTRY_USERNAME}:${REGISTRY_PASSWORD}" \
            "docker-archive:${path}" "${image}"
    fi
}

function image_os() {
    local path="$1"

    local os
    os=$(skopeo inspect --format "{{.Os}}" "docker-archive:${path}")

    echo "${os}"
}

function image_arch() {
    local path="$1"

    local arch
    arch=$(skopeo inspect --format "{{.Architecture}}" "docker-archive:${path}")

    echo "${arch}"
}

function image_gzip() {
    local path="$1"

    local image
    image=$(mktemp)
    "${path}" | gzip --fast > "${image}"

    echo "${image}"
}

function manifest_push() {
    local tag="$1"
    local platforms="$2"
    local source="$3"
    local description="$4"
    local license="$5"

    if [[ -n "${REGISTRY-}" && -n "${GITHUB_REPOSITORY-}" && -n ${REGISTRY_USERNAME-} && -n ${REGISTRY_PASSWORD-} ]]; then
        template="${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}-ARCH"
        target="${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}"

        run manifest-tool \
            --username "${REGISTRY_USERNAME}" \
            --password "${REGISTRY_PASSWORD}" \
            push from-args \
            --platforms "${platforms}" \
            --template "${template}" \
            --target "${target}" \
            --tags "latest" \
            --annotations "org.opencontainers.image.source=${source}" \
            --annotations "org.opencontainers.image.description=${description}" \
            --annotations "org.opencontainers.image.licenses=${license}"
    fi
}
