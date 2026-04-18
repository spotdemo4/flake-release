#!/usr/bin/env bash

shopt -s globstar

function archive() {
    local path="$1"
    local os="$2"

    local filecount
    filecount=$(find -L "${path}" -type f | wc -l | tr -d ' ')

    local bincount
    bincount=$(find -L "${path}/bin" -type f | wc -l | tr -d ' ')

    if [[ "${filecount}" -eq "${bincount}" ]]; then
        pushd "${path}/bin" &> /dev/null || return 1
    else
        pushd "${path}" &> /dev/null || return 1
    fi

    local outdir
    outdir=$(mktemp -d)

    # if only one binary, skip compression
    if [[ "${bincount}" -eq 1 ]]; then
        info "$(dim "archive: skipped")"
        realpath "$(find -L "${path}/bin" -type f)"

    # archive for windows as zip
    elif [[ "${os}" == "windows" ]]; then
        run zip -9r "${outdir}/archive.zip" .
        echo "${outdir}/archive.zip"

    # compress multiple files as tar.xz
    else
        run tar -I "xz -9e" -chf "${outdir}/archive.tar.xz" .
        echo "${outdir}/archive.tar.xz"
    fi

    popd &> /dev/null || return 1
}

function rename() {
    local filepath="$1"
    local name="$2"
    local version="$3"
    local os="$4"
    local arch="$5"

    local filename
    filename=$(basename "${filepath}")

    local final
    if [[ "${filename}" == *.* ]]; then
        fileext="${filename##*.}"

        if [[ "${fileext,,}" == "appimage" ]]; then
            # AppImage is only supported on Linux
            final="$(mktemp -d)/${name}_${version}_${arch}.${fileext}"
        else
            final="$(mktemp -d)/${name}_${version}_${os}_${arch}.${fileext}"
        fi
    else
        final="$(mktemp -d)/${name}_${version}_${os}_${arch}"
    fi

    info "$(dim "rename: ${filename} -> ${final}")"
    cp -R "${filepath}" "${final}"
    echo "${final}"

    delete "${filepath}"
}

function is_static() {
    local file="$1"

    # Check if the file is a binary (executable)
    encoding=$(file -b --mime-encoding "${file}" 2> /dev/null || echo "")
    if [[ "${encoding}" != "binary" ]]; then
        return 1
    fi

    # Check if the binary is dynamically linked
    info=$(file "${file}" 2> /dev/null || echo "")
    if [[ "${info}" == *"dynamically linked"* ]]; then
        return 1
    fi
}

function all_static() {
    local path="$1"

    if [[ -f "${path}" ]]; then
        is_static "${path}"
        return $?
    fi

    if [[ ! -d "${path}/bin" ]]; then
        return 1
    fi

    for file in "${path}"/bin/**; do
        # Skip directories
        if [[ ! -f "$file" ]]; then
            continue
        fi

        # Check if the file is statically linked
        if ! is_static "${file}"; then
            return 1
        fi
    done
}

function delete() {
    rm -rf "${1}" &> /dev/null || true
}

function array() {
    local string="$1"
    local new_array=()
    local array=()

    # split by either spaces or newlines
    if [[ "${string}" == *$'\n'* ]]; then
        readarray -t new_array <<< "${string}"
    else
        IFS=" " read -r -a new_array <<< "${string}"
    fi

    # remove empty entries
    for item in "${new_array[@]}"; do
        if [[ -n "${item}" ]]; then
            array+=( "${item}" )
        fi
    done

    # return empty if no entries
    if [[ "${#array[@]}" -eq 0 ]]; then
        return
    fi

    printf "%s\n" "${array[@]}"
}

function bold() {
    printf "%s%s%s\n" "${color_bold-}" "${1-}" "${color_reset-}"
}

function dim() {
    printf "%s%s%s\n" "${color_dim-}" "${1-}" "${color_reset-}"
}

function info() {
    printf "%s%s%s\n" "${color_info-}" "${1-}" "${color_reset-}" >&2
}

function warn() {
    printf "%s%s%s\n" "${color_warn-}" "${1-}" "${color_reset-}" >&2
}

function success() {
    printf "%s%s%s\n" "${color_success-}" "${1-}" "${color_reset-}" >&2
}

function run() {
    local width
    local code

    if [[ -n "${DEBUG-}" ]]; then
        "${@}" >&2
    elif [[ -n "${CI-}" ]]; then

        # print command output in collapsible group
        printf "%s%s%s%s\n" "::group::" "${color_cmd-}" "${*}" "${color_reset-}" >&2
        "${@}" >&2
        code=${?}
        printf "%s\n" "::endgroup::" >&2

        return "${code}"
    elif width=$(tput cols 2> /dev/null); then
        local line
        local clean

        # print command output on same line
        printf "%s%s%s\n" "${color_cmd-}" "${*}" "${color_reset-}" >&2
        "${@}" 2>&1 | while IFS= read -r line; do
            clean=$(echo -e "${line}" | sed -e 's/\\n//g' -e 's/\\t//g' -e 's/\\r//g' 2> /dev/null | head -c $((width - 10)))
            printf "\r\033[2K%s%s%s" "${color_dim-}" "${clean}" "${color_reset-}" >&2
        done
        code=${PIPESTATUS[0]}
        printf "\r\033[2K" >&2

        return "${code}"
    else
        "${@}" > /dev/null
    fi
}

# default TERM to linux
if [[ -n "${CI-}" || -z "${TERM-}" ]]; then
    TERM=linux
fi

# set colors
if colors=$(tput -T "${TERM}" colors 2> /dev/null); then
    color_reset=$(tput -T "${TERM}" sgr0)
    color_bold=$(tput -T "${TERM}" bold)
    color_dim=$(tput -T "${TERM}" dim)

    if [[ "$colors" -ge 256 ]]; then
        color_info=$(tput -T "${TERM}" setaf 189)
        color_cmd=$(tput -T "${TERM}" setaf 81)
        color_warn=$(tput -T "${TERM}" setaf 216)
        color_success=$(tput -T "${TERM}" setaf 117)
    elif [[ "$colors" -ge 8 ]]; then
        color_cmd=$(tput -T "${TERM}" setaf 4)
        color_warn=$(tput -T "${TERM}" setaf 3)
        color_success=$(tput -T "${TERM}" setaf 2)
    fi
fi
