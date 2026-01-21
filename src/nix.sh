#!/usr/bin/env bash

function nix_system () {
    local system

    system=$(nix eval --impure --raw --expr "builtins.currentSystem" 2> /dev/null)
    info "$(dim "system: ${system}")"

    echo "${system}"
}

function nix_packages () {
    local system="$1"

    local packages
    packages=$(nix flake show --json 2> /dev/null)

    local packages_list
    packages_list=$(echo "${packages}" | jq -r --arg system "$system" '.packages[$system] | keys | join(", ")')
    info "$(dim "packages: ${packages_list}")"

    local packages_json
    packages_json=$(echo "${packages}" | jq -r --arg system "$system" '.packages[$system] | keys[]')

    echo "${packages_json}"
}

function nix_pkg_path () {
    local package="$1"

    local pkg_path
    pkg_path=$(nix eval --raw ".#${package}" 2> /dev/null)
    info "$(dim "path: ${pkg_path}")"

    echo "${pkg_path}"
}

function nix_pkg_pname () {
    local package="$1"

    local pname
    pname=$(nix eval --raw ".#${package}.pname" 2> /dev/null || echo "")

    if [[ -n "${pname}" ]]; then
        info "$(dim "pname: ${pname}")"
    fi

    echo "${pname}"
}

function nix_pkg_version () {
    local package="$1"

    local version
    version=$(nix eval --raw ".#${package}.version" 2> /dev/null || echo "")

    if [[ -n "$version" ]]; then
        info "$(dim "version: ${version}")"
    fi

    echo "${version}"
}

function nix_pkg_image_name () {
    local package="$1"

    local image_name
    image_name=$(nix eval --raw ".#${package}.imageName" 2> /dev/null || echo "")

    if [[ -n "$image_name" ]]; then
        info "$(dim "image name: ${image_name}")"
    fi

    echo "${image_name}"
}

function nix_pkg_image_tag () {
    local package="$1"

    local image_tag
    image_tag=$(nix eval --raw ".#${package}.imageTag" 2> /dev/null || echo "")

    if [[ -n "$image_tag" ]]; then
        info "$(dim "image tag: ${image_tag}")"
    fi

    echo "${image_tag}"
}

function nix_pkg_exe () {
    local package="$1"

    local exe
    exe=$(nix eval --raw --impure ".#${package}" --apply "(import <nixpkgs> {}).lib.meta.getExe" 2> /dev/null || echo "")

    if [[ -n "$exe" ]]; then
        info "$(dim "exe: ${exe}")"
    fi

    echo "${exe}"
}

function nix_build () {
    local package="$1"

    local code
    run nix build ".#${package}" --no-link
    code=$?

    return ${code}
}

function nix_bundle () {
    local package="$1"
    local bundle="$2"

    local tmplink
    tmplink=$(mktemp -u)
    
    case "${bundle}" in
        "appimage")
            info "bundling as AppImage"
            if ! run nix bundle --bundler github:ralismark/nix-appimage ".#${package}" -o "${tmplink}"; then
                warn "AppImage bundle failed"
                return 1
            fi
            ;;

        "arx")
            info "bundling as arx"
            if ! run nix bundle --bundler github:nix-community/nix-bundle ".#${package}" -o "${tmplink}"; then
                warn "arx bundle failed"
                return 1
            fi
            ;;

        *)
            info "bundling as portable executable"
            if ! run nix bundle --bundler github:DavHau/nix-portable#zstd-max ".#${package}" -o "${tmplink}"; then
                warn "portable bundle failed"
                return 1
            fi
            ;;
    esac

    find "$(readlink "${tmplink}")" -type f
}

# https://discourse.nixos.org/t/warning-about-home-ownership/52351
if [[ "${DOCKER-}" == "true" && -n "${CI-}" ]]; then
    chown -R "${USER}:${USER}" "${HOME}"
fi

NIX_CONFIG="extra-experimental-features = nix-command flakes"$'\n'
NIX_CONFIG+="accept-flake-config = true"$'\n'
NIX_CONFIG+="warn-dirty = false"$'\n'
NIX_CONFIG+="always-allow-substitutes = true"$'\n'
NIX_CONFIG+="fallback = true"$'\n'

if [[ -n "${GITHUB_TOKEN-}" ]]; then
    NIX_CONFIG+="access-tokens = github.com=${GITHUB_TOKEN}"$'\n'
fi

export NIX_CONFIG
