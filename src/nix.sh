#!/usr/bin/env bash

# https://discourse.nixos.org/t/warning-about-home-ownership/52351
if [[ "${DOCKER-}" == "true" && -n "${CI-}" ]]; then
    chown -R "${USER}:${USER}" "${HOME}"
fi

NIX_ARGS=("--extra-experimental-features" "nix-command flakes" "--accept-flake-config" "--no-warn-dirty")

if [[ -n "${GITHUB_TOKEN-}" ]]; then
    NIX_ARGS+=("--access-tokens" "github.com=${GITHUB_TOKEN}")
fi

function nix_system () {
    local system

    system=$(nix "${NIX_ARGS[@]}" eval --impure --raw --expr "builtins.currentSystem")
    echo "system: ${system}" >&2

    echo "${system}"
}

function nix_packages () {
    local system="$1"

    local packages
    packages=$(nix "${NIX_ARGS[@]}" flake show --json 2> /dev/null)

    local packages_list
    packages_list=$(echo "${packages}" | jq -r --arg system "$system" '.packages[$system] | keys | join(", ")')
    echo "packages: ${packages_list}" >&2

    local packages_json
    packages_json=$(echo "${packages}" | jq -r --arg system "$system" '.packages[$system] | keys[]')

    echo "${packages_json}"
}

function nix_pkg_path () {
    local package="$1"

    local pkg_path
    pkg_path=$(nix "${NIX_ARGS[@]}" eval --raw ".#${package}")
    echo "path: ${pkg_path}" >&2

    echo "${pkg_path}"
}

function nix_pkg_name () {
    local package="$1"

    local name
    name=$(nix "${NIX_ARGS[@]}" eval --raw ".#${package}.name" 2> /dev/null || echo "")

    if [[ -n "$name" ]]; then
        echo "name: ${name}" >&2
    fi

    echo "${name}"
}

function nix_pkg_version () {
    local package="$1"

    local version
    version=$(nix "${NIX_ARGS[@]}" eval --raw ".#${package}.version" 2> /dev/null || echo "")

    if [[ -n "$version" ]]; then
        echo "version: ${version}" >&2
    fi

    echo "${version}"
}

function nix_pkg_image_name () {
    local package="$1"

    local image_name
    image_name=$(nix "${NIX_ARGS[@]}" eval --raw ".#${package}.imageName" 2> /dev/null || echo "")

    if [[ -n "$image_name" ]]; then
        echo "image name: ${image_name}" >&2
    fi

    echo "${image_name}"
}

function nix_pkg_image_tag () {
    local package="$1"

    local image_tag
    image_tag=$(nix "${NIX_ARGS[@]}" eval --raw ".#${package}.imageTag" 2> /dev/null || echo "")

    if [[ -n "$image_tag" ]]; then
        echo "image tag: ${image_tag}" >&2
    fi

    echo "${image_tag}"
}

function nix_pkg_exe () {
    local package="$1"

    local exe
    exe=$(nix "${NIX_ARGS[@]}" eval --raw --impure ".#${package}" --apply "(import <nixpkgs> {}).lib.meta.getExe" 2> /dev/null || echo "")

    if [[ -n "$exe" ]]; then
        echo "exe: ${exe}" >&2
    fi

    echo "${exe}"
}

function nix_build () {
    local package="$1"
    nix "${NIX_ARGS[@]}" build ".#${package}" --no-link >&2
}

function nix_bundle () {
    local package="$1"

    local tmpdir
    tmpdir=$(mktemp -u)
    
    nix "${NIX_ARGS[@]}" bundle --bundler github:DavHau/nix-portable ".#${package}" -o "${tmpdir}" >&2

    echo "${tmpdir}"
}