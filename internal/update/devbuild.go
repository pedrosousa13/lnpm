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
//
// Two details are load-bearing, because Baseline cuts the version at wherever
// this matches - it decides not just whether a suffix is there but where the
// tag ends:
//
//   - The leading "-N-g<sha>" group is optional but must stay part of the
//     "-dirty" alternative. Simplify it to `-dirty$|-[0-9]+-g[0-9a-f]+$` and
//     "v1.12.0-53-g7079f81-dirty" cuts to "v1.12.0-53-g7079f81" instead of
//     "v1.12.0", which is still ranked below v1.12.0 and reinstates #283.
//   - The sha and its "g" marker are matched case-insensitively. git emits
//     lowercase, so the Makefile is safe either way, but a version stamped by
//     hand is not, and case is not what should decide whether the guard holds.
var describeSuffixRE = regexp.MustCompile(`(?:-[0-9]+-[gG][0-9a-fA-F]+)?-dirty$|-[0-9]+-[gG][0-9a-fA-F]+$`)

// IsDevBuild reports whether version names a build made from a working tree
// rather than a published release.
//
// This is the single definition of that question for the whole program.
// cmd/lnpm calls it directly, to decide which version to report; the three
// update-check guards reach it through Baseline, which is the form of the
// question they actually need. Those four sites used to spell the test out
// inline and disagreed: three copies of `v == "dev" || v == ""` never learned
// about the pseudo-version case cmd/lnpm knew about, and none of them knew
// about `git describe` output at all.
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
// exactly the downgrade that got reported in issue #283. Being a dev build is
// therefore not the test - naming a release is, and "v1.11.0+dirty" names one
// just as "v1.12.0-53-g7079f81-dirty" does.
//
// Only the shapes that name no release skip the check outright: "dev" and ""
// carry no version at all, an untagged `git describe --always` sha is not a
// version, and a pseudo-version's base is synthesised from a timestamp and a
// commit rather than from a tag anyone published.
func Baseline(version string) (string, bool) {
	// Strip the two markers a build picks up from the working tree it was made
	// in: the `git describe` suffix, and build metadata (semver.Canonical drops
	// that, along with normalising a "v1.12" style tag to "v1.12.0"). Canonical
	// also returns "" for anything it cannot parse, which is what rejects "dev",
	// "" and an untagged `git describe --always` sha.
	v := vPrefixed(version)
	if loc := describeSuffixRE.FindStringIndex(v); loc != nil {
		v = v[:loc[0]]
	}
	base := semver.Canonical(v)

	// Whatever survives that has to be a release. Asking IsDevBuild here rather
	// than about the original is what separates the two questions: a dev build
	// whose markers strip away to a real tag has a baseline, and a
	// pseudo-version - whose base is synthesised from a timestamp and a commit
	// rather than from a tag anyone published - does not, because stripping
	// leaves it still a pseudo-version.
	if base == "" || IsDevBuild(base) {
		return "", false
	}
	return base, true
}

// vPrefixed puts version into the "v"-prefixed form golang.org/x/mod requires.
// Release tags carry the prefix but the ldflags stamp may not, and semver.Build
// and friends silently report nothing for a bare "1.12.0".
func vPrefixed(version string) string {
	return "v" + strings.TrimPrefix(version, "v")
}
