#!/usr/bin/env bash

function nix_system () {
    nix eval --impure --raw --expr "builtins.currentSystem"
}

function nix_packages () {
    local system="$1"
    nix flake show --json 2> /dev/null | jq -r --arg system "$system" '.packages[$system] | keys[]'
}

function nix_pkg_path () {
    local package="$1"
    nix eval --raw ".#${package}"
}

function nix_pkg_name () {
    local package="$1"
    nix eval --raw ".#${package}.name"
}

function nix_pkg_build () {
    local package="$1"
    nix build ".#${package}" --no-link --quiet
}

function nix_pkg_image_name () {
    local package="$1"
    nix eval --raw ".#${package}.imageName" 2> /dev/null || echo ""
}

function nix_pkg_image_tag () {
    local package="$1"
    nix eval --raw ".#${package}.imageTag" 2> /dev/null || echo ""
}

function nix_pkg_exe () {
    local package="$1"
    nix eval --raw --impure ".#${package}" --apply "(import <nixpkgs> {}).lib.meta.getExe" 2> /dev/null || echo ""
}