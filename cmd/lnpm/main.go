package main

import (
	"os"
	"runtime/debug"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/update"
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
// The ldflags stamp is reported verbatim, suffix and all: a Makefile build
// stamps `git describe --tags --always --dirty`, and "v1.12.0-53-g7079f81-dirty"
// is precisely what someone running `lnpm --version` on that binary needs to
// see. The update check disqualifies such a version on its own terms rather
// than by having this function launder it.
//
// Only a real released module version is accepted from the build info. Go >=
// 1.24 stamps a VCS-derived pseudo-version (optionally suffixed "+dirty") into
// a plain `go build` from a checkout; reporting that would make a local build
// look like a release to the updater, which would then overwrite it with a
// release tarball. update.IsDevBuild is the shared answer to which versions
// those are - the same one the update-check guards ask - so this can never
// again drift from what the updater accepts. Anything unrecognised falls
// through to "dev".
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
	if update.IsDevBuild(v) {
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
