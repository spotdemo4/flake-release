#!/usr/bin/env bash

# uploads a image to the container registry
function upload_image () {
    local path="$1"
    local tag="$2"

    if [[ -n ${REGISTRY-} && -n ${REGISTRY_USERNAME-} && -n ${REGISTRY_PASSWORD-} ]]; then
        info "uploading to ${REGISTRY}/${GITHUB_REPOSITORY}:${tag}"
        run skopeo --insecure-policy copy \
            --dest-creds "${REGISTRY_USERNAME}:${REGISTRY_PASSWORD}" \
            "docker-archive:${path}" \
            "docker://${REGISTRY}/${GITHUB_REPOSITORY}:${tag}"
    fi
}

function stream_image_helper() {
    local path="$1"
    local tag="$2"

    "${path}" | gzip --fast | skopeo --insecure-policy copy --dest-creds "${REGISTRY_USERNAME}:${REGISTRY_PASSWORD}" docker-archive:/dev/stdin "docker://${REGISTRY}/${GITHUB_REPOSITORY}:${tag}"
}

# streams an image to a container registry
function stream_image () {
    local path="$1"
    local tag="$2"

    if [[ -n ${REGISTRY-} && -n ${REGISTRY_USERNAME-} && -n ${REGISTRY_PASSWORD-} ]]; then
        info "streaming to ${REGISTRY}/${GITHUB_REPOSITORY}:${tag}"
        run stream_image_helper "${path}" "${tag}"
    fi
}
