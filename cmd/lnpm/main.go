package main

import (
	"os"
	"runtime/debug"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// Version, Commit, and Date are set at build time via ldflags (see .goreleaser.yaml)
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// resolveVersion returns the version to report. Release builds are stamped via
// ldflags, but `go install ...@version` is not, so fall back to the module
// version Go embeds in the binary's build metadata.
func resolveVersion() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	cli.SetVersion(resolveVersion())
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
