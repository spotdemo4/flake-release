#!/usr/bin/env bash

function print () {
    local message="$1"

    if [[ "${CI-}" == "true" ]]; then
        printf "::endgroup::\n" >&2
        printf '::group::%s\n' "${message}" >&2
    else
        printf '%s\n' "${message}" >&2
    fi
}

function file_info () {
    local filepath="$1"

    local filename
    filename=$(basename "$filepath")

    local filesize
    filesize=$(du -h "$filepath" | cut -f1)

    local filehash
    filehash=$(sha256sum "$filepath" | cut -d' ' -f1)

    echo "${filename}, size: ${filesize}, hash: ${filehash}"
}

function archive () {
    local source="$1"
    local name="$2"
    local platform="$3"

    local tmpdir
    tmpdir=$(mktemp -d)

    if [[ "$platform" == "windows"* ]]; then
        echo "zipping" >&2
        zip -qr "${tmpdir}/${name}.zip" "${source}" >&2
        file_info "${tmpdir}/${name}.zip" >&2

        echo "${tmpdir}/${name}.zip"
    else
        echo "tarring" >&2
        tar -cJhf "${tmpdir}/${name}.tar.xz" "${source}" >&2
        file_info "${tmpdir}/${name}.tar.xz" >&2

        echo "${tmpdir}/${name}.tar.xz"
    fi
}
