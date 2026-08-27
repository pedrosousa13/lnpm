#!/bin/bash
# Supported-versions check: SECURITY.md's table against the current release (#438)
# Run from anywhere: ./scripts/check-security-versions.sh [security-path] [changelog-path]
#
# Exits 0 when the row of SECURITY.md's supported-versions table marked
# ":white_check_mark:" names the same major as the repository's current
# release, and non-zero with a message on stderr when it does not. Both paths
# default to the files beside this script's parent directory.
#
# Why this exists at all. #389 settled that the table stays hand-maintained:
# every release-please substitution token injects *the version being released*
# into a line that already exists, and none can express a previous major moving
# from supported to unsupported. So the table is manual on purpose, and what
# that leaves is the ordinary failure of anything manual — at some major
# release someone forgets, and a security document goes on claiming support for
# a line that is no longer supported. This check does not need the tokens'
# missing expressiveness. It compares two things that already exist.
#
# ---------------------------------------------------------------------------
# The version source is CHANGELOG.md's topmost "## " heading
# ---------------------------------------------------------------------------
#
# That is the version release-please maintains in-repo, so it is authoritative
# rather than a second hardcoded copy of the number — which would be the same
# class of defect one layer down.
#
# Git tags are the other authoritative source and are deliberately not used.
# CI checkouts are frequently shallow and may carry no tags at all, so a check
# reading `git describe` would pass vacuously in exactly the environment it is
# meant to run in. CHANGELOG.md is always present in the working tree.
#
# Both heading shapes this repo's changelog holds are parsed: the linked form
#   "## [2.4.0](https://github.com/o/r/compare/v2.3.0...v2.4.0) (2026-08-27)"
# and the bare "## 1.0.0 (2026-01-15)" that the first release got.
#
# Matching is on the version token alone and never on a substring of the
# heading line — the trap scripts/changelog-section.sh documents. Every linked
# heading ends in the *previous* version's tag, so a heading for 3.0.0 holds
# "2.4.0" as well: measured on that line the v-prefixed matches are v2.4.0 then
# v3.0.0, so a search for a tag finds the release *before* this one. That would
# make this check green on precisely the commit it exists for. The awk below
# pulls the
# token out and the comparison is on whole strings, which also keeps a "1.x"
# row from matching a 10.x release.
#
# Fenced code blocks are not handled, and do not need to be. The heading this
# reads is the *first* one in the file, and nothing precedes it but the
# "# Changelog" title — the hand-written prose that could contain a fenced
# markdown example lives under a release heading, never above the topmost one.
# changelog-section.sh, which extracts an arbitrary section, does have to care.
#
# ---------------------------------------------------------------------------
# Only the supported row is checked
# ---------------------------------------------------------------------------
#
# The unsupported rows are out of scope per #438. Whether a major moves from
# supported to unsupported is a policy judgement about how long you intend to
# support it, which #389 records as deliberately staying with the maintainer. A
# table listing a major this check has never heard of as unsupported is fine.
#
# What is refused is a table that has stopped naming a supported major at all:
# no table, no ":white_check_mark:" row, more than one of them, or one whose
# version cell is empty. Each would otherwise let the check report success over
# a file that says nothing, which is worse than the staleness it is here for.
#
# ---------------------------------------------------------------------------
# A release commit and an ordinary PR behave differently, and that is the point
# ---------------------------------------------------------------------------
#
# On an ordinary PR, CHANGELOG.md's top heading is the last *released* version
# and SECURITY.md already matches it, so this check passes and says nothing. It
# is not a per-PR chore.
#
# On the release-please PR — and on the release commit it becomes — CHANGELOG.md
# gains the new version's heading. For a patch or a minor the major is
# unchanged and the check still passes. For a new **major** the check FAILS,
# and that failure is the entire feature: it is the reminder, and it arrives on
# the one PR that must carry the fix, where updating SECURITY.md's table is a
# one-line edit that turns it green.
#
# So do not "fix" a red release PR by skipping release commits, by exempting
# the release-please branch, or by comparing against the second heading in the
# file. Any of those removes the only case this check was written to catch and
# leaves a check that can never fail.

set -euo pipefail

usage() {
    echo "Usage: $0 [security-path] [changelog-path]" >&2
    echo >&2
    echo "  [security-path]   defaults to the repo's SECURITY.md" >&2
    echo "  [changelog-path]  defaults to the repo's CHANGELOG.md" >&2
}

if [ $# -ge 1 ]; then
    case "$1" in
        -h | --help)
            usage
            exit 0
            ;;
    esac
fi

if [ $# -gt 2 ]; then
    usage
    exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SECURITY="${1:-$PROJECT_ROOT/SECURITY.md}"
CHANGELOG="${2:-$PROJECT_ROOT/CHANGELOG.md}"

if [ ! -f "$SECURITY" ]; then
    echo "error: no security policy file at $SECURITY" >&2
    exit 2
fi

if [ ! -f "$CHANGELOG" ]; then
    echo "error: no changelog file at $CHANGELOG" >&2
    exit 2
fi

# The version out of the topmost "## " heading. awk reports the two failure
# shapes through its exit status rather than stderr, so the wording lives here
# with the others and nothing has to assume /dev/stderr is writable.
#
# Exit 3: no "## " heading in the file at all. Exit 4: there is one, but the
# token it carries is not a version — an "## Unreleased" heading is the shape
# that produces it.
AWK_STATUS=0
RELEASE=$(awk '
    substr($0, 1, 3) == "## " {
        # `exit` runs the END block, so the flag has to be set before either
        # exit below rather than only on the failure path. Without it a heading
        # that parsed cleanly would still leave END reporting "no heading".
        seen = 1
        heading = substr($0, 4)
        if (substr(heading, 1, 1) == "[") {
            close_bracket = index(heading, "]")
            if (close_bracket > 2) version = substr(heading, 2, close_bracket - 2)
        } else {
            space = index(heading, " ")
            if (space == 0) space = length(heading) + 1
            version = substr(heading, 1, space - 1)
        }
        # Anchored at both ends, so nothing that merely starts with a version
        # is accepted. A prerelease or build suffix is not expected here and is
        # not accepted either: release-please writes plain "X.Y.Z" for this
        # repo, and guessing at a shape that has never appeared would mean
        # guessing at which part of it the major is.
        if (version ~ /^[0-9]+\.[0-9]+\.[0-9]+$/) {
            print version
            exit 0
        }
        exit 4
    }
    END { if (!seen) exit 3 }
' "$CHANGELOG") || AWK_STATUS=$?

case "$AWK_STATUS" in
    0) ;;
    3)
        echo "error: no \"## \" version heading in $CHANGELOG, so there is no current release to check against" >&2
        exit 1
        ;;
    4)
        echo "error: the topmost \"## \" heading in $CHANGELOG does not name a version, so there is no current release to check against" >&2
        exit 1
        ;;
    *)
        echo "error: could not read $CHANGELOG (awk exited $AWK_STATUS)" >&2
        exit 1
        ;;
esac

EXPECTED="${RELEASE%%.*}.x"

# Every supported row's version cell, one per line. A row is a table line whose
# Supported column carries the check mark; the separator line and the
# unsupported rows carry neither, so neither reaches this.
#
# The cell is taken by position rather than by searching the line, for the same
# reason the version token is: a major like "2.x" also appears in the prose
# under the table, so a line-wide search is answering a different question than
# "what does the table say". Position is what makes the answer the table's.
#
# Each line is prefixed with "v=" so that a row whose cell is empty is still a
# line. Printing the bare cell would make an empty one indistinguishable from
# no row at all, and those are two different failures with two different
# messages.
SUPPORTED_ROWS=$(awk '
    function trim(s) { gsub(/^[ \t]+|[ \t]+$/, "", s); return s }
    substr($0, 1, 1) == "|" && index($0, ":white_check_mark:") > 0 {
        n = split($0, cells, "|")
        # A leading and a trailing pipe make the first and last pieces empty,
        # so a row with two columns splits into four.
        if (n >= 4) print "v=" trim(cells[2])
        else print "v="
    }
' "$SECURITY")

# Reached both by a file with no table at all and by a table whose every row is
# :x:. The two are one failure from here: neither states a supported major, and
# neither is fixed by anything but adding the row.
if [ -z "$SUPPORTED_ROWS" ]; then
    echo "error: $SECURITY has no supported-versions row" >&2
    echo "  expected a table row whose Supported column is :white_check_mark:, naming $EXPECTED" >&2
    echo "  the current release is $RELEASE, from the topmost \"## \" heading of $CHANGELOG" >&2
    exit 1
fi

SUPPORTED_COUNT=$(printf '%s\n' "$SUPPORTED_ROWS" | wc -l | tr -d ' ')
if [ "$SUPPORTED_COUNT" -ne 1 ]; then
    echo "error: $SECURITY marks $SUPPORTED_COUNT rows as supported, and this check reads exactly one" >&2
    echo "  found: $(printf '%s\n' "$SUPPORTED_ROWS" | sed 's/^v=//' | paste -sd ' ' -)" >&2
    echo "  expected a single row naming $EXPECTED, the major of release $RELEASE ($CHANGELOG)" >&2
    exit 1
fi

SUPPORTED="${SUPPORTED_ROWS#v=}"

if [ -z "$SUPPORTED" ]; then
    echo "error: the supported-versions row in $SECURITY has an empty Version cell" >&2
    echo "  expected: $EXPECTED, the major of release $RELEASE ($CHANGELOG)" >&2
    exit 1
fi

if [ "$SUPPORTED" != "$EXPECTED" ]; then
    echo "error: $SECURITY's supported-versions table names the wrong major" >&2
    echo "  found:    $SUPPORTED" >&2
    echo "  expected: $EXPECTED" >&2
    echo "  the current release is $RELEASE, from the topmost \"## \" heading of $CHANGELOG" >&2
    echo >&2
    echo "  Edit the :white_check_mark: row of the Supported Versions table in $SECURITY" >&2
    echo "  to read $EXPECTED. Whether $SUPPORTED should now be listed as unsupported, or" >&2
    echo "  dropped, is a policy call this check deliberately does not make." >&2
    exit 1
fi
