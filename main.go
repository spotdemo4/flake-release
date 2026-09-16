package main

import (
	"os"

	flakerelease "trev.zip/llc/flake-release/internal"
)

func main() {
	if err := flakerelease.Run(os.Args[1:]); err != nil {
		flakerelease.PrintError(err)
		os.Exit(1)
	}
}
