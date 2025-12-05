#!/usr/bin/env bash

function archive () {
    local source_path="$1"
    local archive_path="$2"
    local platform="$3"

    if [[ "$platform" == "windows"* ]]; then
        zip -qr "${archive_path}.zip" "${source_path}" &> /dev/null
        echo "${archive_path}.zip"
    else
        tar -cJhf "${archive_path}.tar.xz" "${source_path}" &> /dev/null
        echo "${archive_path}.tar.xz"
    fi
}
