#!/usr/bin/env bash

detect_platform () {
    local filepath="$1"

    local file_output
    file_output=$(file -b "$filepath")
    
    # detect os
    local os
    if [[ "$file_output" =~ "ELF" ]]; then
        os="linux"
    elif [[ "$file_output" =~ "Mach-O" ]]; then
        os="darwin"
    elif [[ "$file_output" =~ "PE32" ]] || [[ "$file_output" =~ "MS Windows" ]]; then
        os="windows"
    fi

    if [[ -n "${os-}" ]]; then
        info "$(dim "os: ${os}")"
    fi
    
    # detect architecture
    local arch
    if [[ "$file_output" =~ "x86-64" ]] || [[ "$file_output" =~ "x86_64" ]]; then
        arch="amd64"
    elif [[ "$file_output" =~ "Intel 80386" ]] || [[ "$file_output" =~ "i386" ]]; then
        arch="386"
    elif [[ "$file_output" =~ "ARM aarch64" ]] || [[ "$file_output" =~ "arm64" ]]; then
        arch="arm64"
    elif [[ "$file_output" =~ "ARM" ]]; then
        arch="arm"
    elif [[ "$file_output" =~ "MIPS" ]]; then
        arch="mips"
    fi

    if [[ -n "${arch-}" ]]; then
        info "$(dim "architecture: ${arch}")"
    fi
    
    echo "${os:-"unknown"}-${arch:-"unknown"}"
}

host_platform () {
    local uname_os
    uname_os=$(uname -s)
    local uname_arch
    uname_arch=$(uname -m)

    local os
    case "${uname_os}" in
        Linux*)     os="linux" ;;
        Darwin*)    os="darwin" ;;
        MINGW*|MSYS*|CYGWIN*) os="windows" ;;
        *)          os="unknown" ;;
    esac

    local arch
    case "${uname_arch}" in
        x86_64*)    arch="amd64" ;;
        i386*|i686*) arch="386" ;;
        aarch64*|arm64*) arch="arm64" ;;
        armv7*|armv6*) arch="arm" ;;
        mips*)      arch="mips" ;;
        *)          arch="unknown" ;;
    esac

    info "$(dim "host platform: ${os}-${arch}")"
    echo "${os}-${arch}"
}