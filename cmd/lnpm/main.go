package main

import (
	"os"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// Version is set at build time via ldflags
var Version = "dev"

func main() {
	cli.SetVersion(Version)
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
