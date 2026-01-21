#!/usr/bin/env bash

# uploads a image to the container registry
function image_upload() {
    local path="$1"
    local tag="$2"
    local arch="$3"

    local image
    image="docker://${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}-${arch}"

    info "uploading to ${image}"
    run skopeo --insecure-policy copy \
        --dest-creds "${REGISTRY_USERNAME}:${REGISTRY_PASSWORD}" \
        "docker-archive:${path}" "${image}"
}

function image_os() {
    local path="$1"

    local os
    os=$(skopeo --insecure-policy inspect --creds "${REGISTRY_USERNAME}:${REGISTRY_PASSWORD}" --format "{{.Os}}" "docker-archive:${path}")

    echo "${os}"
}

function image_arch() {
    local path="$1"

    local arch
    arch=$(skopeo --insecure-policy inspect --creds "${REGISTRY_USERNAME}:${REGISTRY_PASSWORD}" --format "{{.Architecture}}" "docker-archive:${path}")

    echo "${arch}"
}

function image_gzip() {
    local path="$1"

    local image
    image=$(mktemp)
    "${path}" | gzip --fast > "${image}"

    echo "${image}"
}

function image_exists() {
    local tag="$1"
    local arch="$2"

    local image
    image="docker://${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}-${arch}"

    if skopeo --insecure-policy inspect --creds "${REGISTRY_USERNAME}:${REGISTRY_PASSWORD}" "${image}" &> /dev/null; then
        return 0
    fi

    return 1
}

function manifest_update() {
    local tag="$1"

    local remote_tags
    readarray -t remote_tags < <(skopeo --insecure-policy list-tags --creds "${REGISTRY_USERNAME}:${REGISTRY_PASSWORD}" "docker://${REGISTRY,,}/${GITHUB_REPOSITORY,,}" | jq -r ".Tags[] | select(startswith(\"${tag}-\"))")

    local inspect
    local platforms=()
    local label_keys=()
    local label_value
    local annotations=()
    local first=true

    for remote_tag in "${remote_tags[@]}"; do
        inspect=$(skopeo --insecure-policy inspect --creds "${REGISTRY_USERNAME}:${REGISTRY_PASSWORD}" "docker://${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${remote_tag}")

        platforms+=( "$(echo "${inspect}" | jq -r '(.Os + "/" + .Architecture)')" )

        if [[ "${first}" == true ]]; then
            first=false

            readarray -t label_keys < <(echo "${inspect}" | jq -r '.Labels | keys[]')
            for label_key in "${label_keys[@]}"; do
                label_value=$(echo "${inspect}" | jq -r --arg key "$label_key" '.Labels[$key]')
                annotations+=( "--annotations" "${label_key}=${label_value}" )
            done
        fi
    done

    template="${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}-ARCH"
    target="${REGISTRY,,}/${GITHUB_REPOSITORY,,}:${tag}"

    run manifest-tool \
        --username "${REGISTRY_USERNAME}" \
        --password "${REGISTRY_PASSWORD}" \
        push \
        --type oci \
        from-args \
        --platforms "$( IFS=','; echo "${platforms[*]}" )" \
        --template "${template}" \
        --target "${target}" \
        --tags "latest" \
        "${annotations[@]}"
}
