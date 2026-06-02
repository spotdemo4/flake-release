package main

import (
	"fmt"
	"os"

	flakerelease "trev.zip/llc/flake-release/internal"
)

func main() {
	if err := flakerelease.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
