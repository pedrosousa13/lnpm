package main

import (
	"os"

	"github.com/user/lnpm/internal/cli"
)

// Version is set at build time via ldflags
var Version = "dev"

func main() {
	cli.SetVersion(Version)
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
