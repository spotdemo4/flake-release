#!/usr/bin/env bash

function nix_system () {
    local system

    system=$(nix "${NIX_ARGS[@]}" eval --impure --raw --expr "builtins.currentSystem")
    info "$(dim "system: ${system}")"

    echo "${system}"
}

function nix_packages () {
    local system="$1"

    local packages
    packages=$(nix "${NIX_ARGS[@]}" flake show --json 2> /dev/null)

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
    pkg_path=$(nix "${NIX_ARGS[@]}" eval --raw ".#${package}")
    info "$(dim "path: ${pkg_path}")"

    echo "${pkg_path}"
}

function nix_pkg_name () {
    local package="$1"

    local name
    name=$(nix "${NIX_ARGS[@]}" eval --raw ".#${package}.name" 2> /dev/null || echo "")

    if [[ -n "$name" ]]; then
        info "$(dim "name: ${name}")"
    fi

    echo "${name}"
}

function nix_pkg_version () {
    local package="$1"

    local version
    version=$(nix "${NIX_ARGS[@]}" eval --raw ".#${package}.version" 2> /dev/null || echo "")

    if [[ -n "$version" ]]; then
        info "$(dim "version: ${version}")"
    fi

    echo "${version}"
}

function nix_pkg_image_name () {
    local package="$1"

    local image_name
    image_name=$(nix "${NIX_ARGS[@]}" eval --raw ".#${package}.imageName" 2> /dev/null || echo "")

    if [[ -n "$image_name" ]]; then
        info "$(dim "image name: ${image_name}")"
    fi

    echo "${image_name}"
}

function nix_pkg_image_tag () {
    local package="$1"

    local image_tag
    image_tag=$(nix "${NIX_ARGS[@]}" eval --raw ".#${package}.imageTag" 2> /dev/null || echo "")

    if [[ -n "$image_tag" ]]; then
        info "$(dim "image tag: ${image_tag}")"
    fi

    echo "${image_tag}"
}

function nix_pkg_exe () {
    local package="$1"

    local exe
    exe=$(nix "${NIX_ARGS[@]}" eval --raw --impure ".#${package}" --apply "(import <nixpkgs> {}).lib.meta.getExe" 2> /dev/null || echo "")

    if [[ -n "$exe" ]]; then
        info "$(dim "exe: ${exe}")"
    fi

    echo "${exe}"
}

function nix_build () {
    local package="$1"

    local code
    run nix "${NIX_ARGS[@]}" build ".#${package}" --no-link
    code=$?

    return ${code}
}

function nix_bundle () {
    local package="$1"

    local tmpdir
    tmpdir=$(mktemp -u)
    
    local code
    run nix "${NIX_ARGS[@]}" bundle --bundler github:DavHau/nix-portable#zstd-max ".#${package}" -o "${tmpdir}"
    code=$?

    echo "${tmpdir}"
    return ${code}
}

# https://discourse.nixos.org/t/warning-about-home-ownership/52351
if [[ "${DOCKER-}" == "true" && -n "${CI-}" ]]; then
    chown -R "${USER}:${USER}" "${HOME}"
fi

NIX_ARGS=("--extra-experimental-features" "nix-command flakes" "--accept-flake-config" "--no-warn-dirty")

if [[ -n "${GITHUB_TOKEN-}" ]]; then
    NIX_ARGS+=("--access-tokens" "github.com=${GITHUB_TOKEN}")
fi