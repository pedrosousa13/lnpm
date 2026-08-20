package main

import (
	"os"
	"runtime/debug"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
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
	info, ok := debug.ReadBuildInfo()
	return pickVersion(Version, info, ok)
}

// pickVersion decides which version to report given the ldflags stamp and the
// binary's embedded build info.
//
// Only a real released module version is accepted from the build info. Go >=
// 1.24 stamps a VCS-derived pseudo-version (optionally suffixed "+dirty") into
// a plain `go build` from a checkout; reporting that would make a local build
// look like a release to the updater, which would then overwrite it with a
// release tarball. Anything unrecognised falls through to "dev".
func pickVersion(stamped string, info *debug.BuildInfo, ok bool) string {
	if stamped != "dev" && stamped != "" {
		return stamped
	}
	if !ok {
		return "dev"
	}
	v := info.Main.Version
	if v == "" || v == "(devel)" || !semver.IsValid(v) {
		return "dev"
	}
	if module.IsPseudoVersion(v) || semver.Build(v) != "" {
		return "dev"
	}
	return v
}

func main() {
	cli.SetVersion(resolveVersion(), Commit, Date)
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
