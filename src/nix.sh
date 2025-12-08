#!/usr/bin/env bash

# https://github.com/NixOS/nix/issues/10202
git config --global --add safe.directory "$(pwd)"

# https://discourse.nixos.org/t/warning-about-home-ownership/52351
if [[ "${CI}" == "true" ]]; then
    chown -R "${USER}:${USER}" "${HOME}"
fi

NIX_ARGS=("--extra-experimental-features" "nix-command flakes" "--accept-flake-config" "--no-warn-dirty")

function nix_system () {
    nix "${NIX_ARGS[@]}" eval --impure --raw --expr "builtins.currentSystem"
}

function nix_packages () {
    local system="$1"
    nix "${NIX_ARGS[@]}" flake show --json 2> /dev/null | jq -r --arg system "$system" '.packages[$system] | keys[]'
}

function nix_pkg_path () {
    local package="$1"
    nix "${NIX_ARGS[@]}" eval --raw ".#${package}"
}

function nix_pkg_name () {
    local package="$1"
    nix "${NIX_ARGS[@]}" eval --raw ".#${package}.name" 2> /dev/null || echo ""
}

function nix_pkg_version () {
    local package="$1"
    nix "${NIX_ARGS[@]}" eval --raw ".#${package}.version" 2> /dev/null || echo ""
}

function nix_pkg_build () {
    local package="$1"
    nix "${NIX_ARGS[@]}" build ".#${package}" --no-link --quiet
}

function nix_pkg_image_name () {
    local package="$1"
    nix "${NIX_ARGS[@]}" eval --raw ".#${package}.imageName" 2> /dev/null || echo ""
}

function nix_pkg_image_tag () {
    local package="$1"
    nix "${NIX_ARGS[@]}" eval --raw ".#${package}.imageTag" 2> /dev/null || echo ""
}

function nix_pkg_exe () {
    local package="$1"
    nix "${NIX_ARGS[@]}" eval --raw --impure ".#${package}" --apply "(import <nixpkgs> {}).lib.meta.getExe" 2> /dev/null || echo ""
}

function nix_bundle () {
    local package="$1"

    local tmpdir
    tmpdir=$(mktemp -u)
    
    nix "${NIX_ARGS[@]}" bundle --bundler github:DavHau/nix-portable ".#${package}" -o "${tmpdir}" &> /dev/null || echo ""
    echo "${tmpdir}"
}