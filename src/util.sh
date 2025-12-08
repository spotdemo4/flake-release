#!/usr/bin/env bash

GLOBAL_PACKAGE="default"

function print () {
    local message="$1"

    printf '%s: %s\n' "${GLOBAL_PACKAGE}" "${message}" >&2
}

function file_info () {
    local filepath="$1"

    local filename
    filename=$(basename "$filepath")

    local filesize
    filesize=$(du -h "$filepath" | cut -f1)

    local filehash
    filehash=$(sha256sum "$filepath" | cut -d' ' -f1)

    echo "${filename}: size: ${filesize}, hash: ${filehash}"
}

function archive () {
    local source="$1"
    local name="$2"
    local platform="$3"

    local tmpdir
    tmpdir=$(mktemp -d)

    if [[ "$platform" == "windows"* ]]; then
        print "archiving as zip"

        zip -qr "${tmpdir}/${name}.zip" "${source}" &> /dev/null
        echo "${tmpdir}/${name}.zip"
    else
        print "archiving as tar.xz"

        tar -cJhf "${tmpdir}/${name}.tar.xz" "${source}" &> /dev/null
        echo "${tmpdir}/${name}.tar.xz"
    fi
}
