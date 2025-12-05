#!/usr/bin/env bash

function nix_system () {
    nix --extra-experimental-features "nix-command flakes" eval --impure --raw --expr "builtins.currentSystem"
}

function nix_packages () {
    local system="$1"
    nix --extra-experimental-features "nix-command flakes" flake show --json 2> /dev/null | jq -r --arg system "$system" '.packages[$system] | keys[]'
}

function nix_pkg_path () {
    local package="$1"
    nix --extra-experimental-features "nix-command flakes" eval --raw ".#${package}"
}

function nix_pkg_name () {
    local package="$1"
    nix --extra-experimental-features "nix-command flakes" eval --raw ".#${package}.name" 2> /dev/null || echo ""
}

function nix_pkg_version () {
    local package="$1"
    nix --extra-experimental-features "nix-command flakes" eval --raw ".#${package}.version" 2> /dev/null || echo ""
}

function nix_pkg_build () {
    local package="$1"
    nix --extra-experimental-features "nix-command flakes" build ".#${package}" --no-link --quiet
}

function nix_pkg_image_name () {
    local package="$1"
    nix --extra-experimental-features "nix-command flakes" eval --raw ".#${package}.imageName" 2> /dev/null || echo ""
}

function nix_pkg_image_tag () {
    local package="$1"
    nix --extra-experimental-features "nix-command flakes" eval --raw ".#${package}.imageTag" 2> /dev/null || echo ""
}

function nix_pkg_exe () {
    local package="$1"
    nix --extra-experimental-features "nix-command flakes" eval --raw --impure ".#${package}" --apply "(import <nixpkgs> {}).lib.meta.getExe" 2> /dev/null || echo ""
}

function nix_bundle () {
    local package="$1"
    local tmpdir=$(mktemp -u)
    nix --extra-experimental-features "nix-command flakes" bundle --bundler github:DavHau/nix-portable ".#${package}" -o "${tmpdir}" &> /dev/null || echo ""
    echo "${tmpdir}"
}