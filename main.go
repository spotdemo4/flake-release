package main

import (
	"fmt"
	"os"

	flakerelease "github.com/spotdemo4/flake-release/internal"
)

func main() {
	if err := flakerelease.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
