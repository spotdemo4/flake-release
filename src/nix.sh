#!/usr/bin/env bash

function nix_system() {
    local system

    system=$(nix eval --impure --raw --expr "builtins.currentSystem" 2> /dev/null)
    info "$(dim "system: ${system}")"

    echo "${system}"
}

function nix_pkg_path() {
    local package="$1"

    local pkg_path
    pkg_path=$(nix eval --raw ".#${package}" 2> /dev/null)
    info "$(dim "path: ${pkg_path}")"

    echo "${pkg_path}"
}

function nix_pkg_pname() {
    local package="$1"

    local pname
    pname=$(nix eval --raw ".#${package}.pname" 2> /dev/null || echo "")

    if [[ -n "${pname}" ]]; then
        info "$(dim "pname: ${pname}")"
    fi

    echo "${pname}"
}

function nix_pkg_version() {
    local package="$1"

    local version
    version=$(nix eval --raw ".#${package}.version" 2> /dev/null || echo "")

    if [[ -n "$version" ]]; then
        info "$(dim "version: ${version}")"
    fi

    echo "${version}"
}

function nix_pkg_platform() {
    local package="$1"

    local platform
    platform=$(nix eval --json ".#${package}.stdenv.hostPlatform.go" 2> /dev/null || echo "")

    if [[ -n "${platform}" ]]; then
        info "$(dim "os: $(echo "${platform}" | jq -r '.GOOS')")"
        info "$(dim "arch: $(echo "${platform}" | jq -r '.GOARCH')")"
    fi

    echo "${platform}"
}

function nix_image_name() {
    local package="$1"

    local image_name
    image_name=$(nix eval --raw ".#${package}.imageName" 2> /dev/null || echo "")

    if [[ -n "$image_name" ]]; then
        info "$(dim "image name: ${image_name}")"
    fi

    echo "${image_name}"
}

function nix_image_tag() {
    local package="$1"

    local image_tag
    image_tag=$(nix eval --raw ".#${package}.imageTag" 2> /dev/null || echo "")

    if [[ -n "$image_tag" ]]; then
        info "$(dim "image tag: ${image_tag}")"
    fi

    echo "${image_tag}"
}

function nix_build() {
    local package="$1"

    local code
    run nix build ".#${package}" --no-link
    code=$?

    return ${code}
}

function nix_bundle_appimage() {
    local package="$1"

    local tmplink
    tmplink=$(mktemp -u)
    
    if ! run nix bundle --bundler github:spotdemo4/nur#appimage ".#${package}" -o "${tmplink}"; then
        warn "AppImage bundle failed"
        return 1
    fi

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
