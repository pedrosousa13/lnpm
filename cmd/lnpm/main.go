package main

import (
	"os"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// Version, Commit, and Date are set at build time via ldflags (see .goreleaser.yaml)
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func main() {
	cli.SetVersion(Version)
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
