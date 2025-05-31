package main

import (
	"os"

	"github.com/numtide/nix-binary-cache/cmd"
)

func main() {
	root := cmd.New()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
