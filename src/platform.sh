#!/usr/bin/env bash

detect_platform() {
    local filepath="$1"
    local file_output=$(file -b "$filepath")
    
    # detect os
    if [[ "$file_output" =~ "ELF" ]]; then
        os="linux"
    elif [[ "$file_output" =~ "Mach-O" ]]; then
        os="darwin"
    elif [[ "$file_output" =~ "PE32" ]] || [[ "$file_output" =~ "MS Windows" ]]; then
        os="windows"
    else
        os="unknown"
    fi
    
    # detect architecture
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
    else
        arch="unknown"
    fi
    
    echo "${os}-${arch}"
}