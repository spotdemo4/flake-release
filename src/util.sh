#!/usr/bin/env bash

function archive() {
    local path="$1"
    local os="$2"

    local filecount
    filecount=$(find -L "${path}" -type f | wc -l | tr -d ' ')

    local bincount
    filecount=$(find -L "${path}/bin" -type f | wc -l | tr -d ' ')

    local indir
    indir=$(mktemp -d)

    local outdir
    outdir=$(mktemp -d)

    if [[ "${filecount}" -eq "${bincount}" ]]; then
        cp -R "${path}/bin/"* "${indir}/"
    else
        cp -R "${path}/"* "${indir}/"
    fi

    # archive for windows as zip
    if [[ "${os}" == "windows" ]]; then

        pushd "${indir}" &> /dev/null || return 1
        run zip -9r "${outdir}/archive.zip" .
        popd &> /dev/null || return 1

        echo "${outdir}/archive.zip"

    # if only one binary, compress directly
    elif [[ "${bincount}" -eq 1 ]]; then

        local filepath
        filepath=$(find -L "${indir}" -type f)
        info "compressing ${filepath}"
        xz -9e -c "${filepath}" > "${outdir}/archive.xz"

        echo "${outdir}/archive.xz"

    # compress multiple files as tar.xz
    else
        pushd "${indir}" &> /dev/null || return 1
        run tar -I "xz -9e" -chf "${outdir}/archive.tar.xz" .
        popd &> /dev/null || return 1

        echo "${outdir}/archive.tar.xz"
    fi

    rm -rf "${indir}"
}

function rename() {
    local filepath="$1"
    local name="$2"
    local version="$3"
    local platform="$4"

    local filename
    filename=$(basename "${filepath}")
    fileext="${filename##*.}"

    local tmpdir
    tmpdir=$(mktemp -d)

    local final
    final="${tmpdir}/${name}_${version}_${platform}.${fileext}"

    cp -R "${filepath}" "${final}"
    rm -rf "${filepath}" &> /dev/null || true

    echo "${final}"
}

function only_bins() {
    local path="$1"

    local filecount
    filecount=$(find -L "${path}" -type f | wc -l | tr -d ' ')
    if [[ "${filecount}" -eq 0 ]]; then
        return
    fi

    local bincount
    bincount=$(find -L "${path}/bin" -type f -executable -exec sh -c 'file -i "$1" | grep -q "executable; charset=binary"' shell {} \; -print | wc -l | tr -d ' ')
    if [[ "${filecount}" -eq "${bincount}" ]]; then
        echo "true"
    fi
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
