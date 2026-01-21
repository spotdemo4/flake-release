#!/usr/bin/env bash

# uploads a image to the container registry
function upload_image() {
    local path="$1"
    local tag="$2"

    local arch
    arch=$(skopeo inspect --format "{{.Os}}/{{.Architecture}}" "docker-archive:${path}")

    if [[ -n "${REGISTRY-}" && -n "${GITHUB_REPOSITORY-}" && -n "${REGISTRY_USERNAME-}" && -n "${REGISTRY_PASSWORD-}" ]]; then
        local image
        image="docker://${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}-${arch}"

        info "uploading to ${image}"
        run skopeo --insecure-policy copy \
            --dest-creds "${REGISTRY_USERNAME}:${REGISTRY_PASSWORD}" \
            "docker-archive:${path}" "${image}"

        echo "${arch}"
    fi
}

# streams an image to a container registry
function stream_image() {
    local path="$1"
    local tag="$2"

    local tmpdir
    tmpdir=$(mktemp -d)
    "${path}" | gzip --fast > "${tmpdir}/image.tar.gz"

    local arch
    arch=$(skopeo inspect --format "{{.Os}}/{{.Architecture}}" "docker-archive:${tmpdir}/image.tar.gz")

    if [[ -n "${REGISTRY-}" && -n "${GITHUB_REPOSITORY-}" && -n "${REGISTRY_USERNAME-}" && -n "${REGISTRY_PASSWORD-}" ]]; then
        local image
        image="docker://${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}-${arch}"

        info "uploading to ${image}"
        run skopeo --insecure-policy copy \
            --dest-creds "${REGISTRY_USERNAME}:${REGISTRY_PASSWORD}" \
            "docker-archive:${tmpdir}/image.tar.gz" "${image}"

        echo "${arch}"
    fi

    rm -rf "${tmpdir}"
}

function manifest_push() {
    local tag="$1"
    local platforms="$2"

    if [[ -n "${REGISTRY-}" && -n "${GITHUB_REPOSITORY-}" && -n ${REGISTRY_USERNAME-} && -n ${REGISTRY_PASSWORD-} ]]; then
        template="${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}-ARCH"
        target="${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}"

        manifest-tool \
            --username "${REGISTRY_USERNAME}" \
            --password "${REGISTRY_PASSWORD}" \
            push from-args \
            --platforms "${platforms}" \
            --template "${template}" \
            --target "${target}" \
            --tags "latest"
    fi
}
