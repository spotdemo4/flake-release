#!/usr/bin/env bash

# uploads a image to the container registry
function upload_image() {
    local path="$1"
    local tag="$2"

    local arch
    arch=$(skopeo inspect --format "{{.Architecture}}" "docker-archive:${path}")

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
    arch=$(skopeo inspect --format "{{.Architecture}}" "docker-archive:${tmpdir}/image.tar.gz")

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

function manifest_create() {
    local tag="$1"

    if [[ -n "${REGISTRY-}" && -n "${GITHUB_REPOSITORY-}" ]]; then
        image="docker://${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}"

        run buildah manifest create "${image}"
    fi
}

function manifest_add() {
    local tag="$1"
    local arch="$2"

    if [[ -n "${REGISTRY-}" && -n "${GITHUB_REPOSITORY-}" ]]; then
        image="docker://${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}"
        image_arch="docker://${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}-${arch}"

        run buildah manifest add "${image}" --arch "${arch}" "${image_arch}"
    fi
}

function manifest_push() {
    local tag="$1"

    if [[ -n "${REGISTRY-}" && -n "${GITHUB_REPOSITORY-}" && -n ${REGISTRY_USERNAME-} && -n ${REGISTRY_PASSWORD-} ]]; then
        image="docker://${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}"

        info "pushing manifest ${image}"
        run buildah manifest push \
            --creds "${REGISTRY_USERNAME}:${REGISTRY_PASSWORD}" \
            --all "${image}"
    fi
}
