package update

import (
	"regexp"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// describeSuffixRE matches what `git describe --tags --always --dirty` appends
// to the tag it resolved: "-<commits ahead>-g<abbreviated sha>" once the build
// is past the tag, "-dirty" for a modified working tree, or both. The Makefile
// stamps that output straight into the binary, so "v1.12.0-53-g7079f81-dirty"
// is an ordinary local build's version string.
//
// The shape is matched specifically rather than treating every pre-release as a
// dev build, because a real release may well carry one: "v2.1.0-rc.1" and
// "v2.0.0-beta.2" are versions we ship and must keep updating.
var describeSuffixRE = regexp.MustCompile(`(?:-[0-9]+-g[0-9a-f]+)?-dirty$|-[0-9]+-g[0-9a-f]+$`)

// IsDevBuild reports whether version names a build made from a working tree
// rather than a published release.
//
// This is the single answer to that question for the whole program - the
// version reporting in cmd/lnpm and every update-check guard below ask through
// here. They used to each spell it out inline, and disagreed: three copies of
// `v == "dev" || v == ""` never learned about the pseudo-version case cmd/lnpm
// knew about, and none of them knew about `git describe` output at all.
//
// Note that an unparseable version (a bare commit sha, "garbage") is not a dev
// build by this definition. It is not a release either; callers that care reject
// it separately, because "we cannot read this" and "this came from a working
// tree" want different handling.
func IsDevBuild(version string) bool {
	if version == "" || version == "dev" {
		return true
	}
	v := vPrefixed(version)
	// A pseudo-version is what Go >= 1.24 stamps into a plain `go build` from a
	// checkout, and build metadata ("+dirty") is how it marks that the checkout
	// had uncommitted changes.
	return module.IsPseudoVersion(v) || semver.Build(v) != "" || describeSuffixRE.MatchString(v)
}

// Baseline returns the released version an update check must compare version
// against, and whether one exists at all.
//
// This exists because a dev build is not simply disqualified from update checks.
// A `git describe` stamp names the release it was built from, so a local build
// of v1.12.0 plus 53 commits still wants to hear about v1.13.0 - it just must
// not be told that v1.12.0, the tag it is already ahead of, is an upgrade.
// Comparing the tag rather than the full stamp gives both: semver ranks the
// pre-release-suffixed string below the plain tag it descends from, which is
// exactly the downgrade that got reported in issue #283.
//
// The other dev-build shapes name no release. "dev" and "" carry no version at
// all; a pseudo-version's base is synthesised from a timestamp and a commit,
// not from a tag anyone published. Those keep skipping the check outright.
func Baseline(version string) (string, bool) {
	v := vPrefixed(version)
	if loc := describeSuffixRE.FindStringIndex(v); loc != nil {
		v = v[:loc[0]]
	} else if IsDevBuild(version) {
		return "", false
	}

	// semver.Canonical returns "" for anything it cannot parse, which covers
	// both an untagged `git describe --always` sha and outright garbage.
	base := semver.Canonical(v)
	return base, base != ""
}

// vPrefixed puts version into the "v"-prefixed form golang.org/x/mod requires.
// Release tags carry the prefix but the ldflags stamp may not, and semver.Build
// and friends silently report nothing for a bare "1.12.0".
func vPrefixed(version string) string {
	return "v" + strings.TrimPrefix(version, "v")
}
