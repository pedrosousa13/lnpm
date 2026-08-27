#!/bin/bash
# Tests for scripts/check-security-versions.sh (#438)
# Run from anywhere: ./scripts/test-check-security-versions.sh
#
# Every case runs against fixtures built here, never against the repo's own
# SECURITY.md or CHANGELOG.md. Both of those files move: CHANGELOG.md gains a
# section at every release, and SECURITY.md's table is edited at every major.
# A test reading them would either have to be edited each release or would
# start passing for the wrong reason — and the wrong reason here is the exact
# defect the check exists to catch, so it would be invisible.
#
# The mismatch cases are the load-bearing ones. A check that always exits 0
# passes every "matching majors" row in this file, so each of those rows has a
# mismatched twin that must exit non-zero, and the twin also asserts that the
# message names the file, the found value and the expected value.
#
# Two traps from changelog-section.sh's header are live in the fixtures here:
#
#   - Every linked heading ends in the *previous* version's tag, so a heading
#     "## [3.0.0](.../compare/v2.4.0...v3.0.0)" holds both "3.0.0" and "2.4.0".
#     Measured on that line: the bare X.Y.Z matches are 3.0.0, 2.4.0, 3.0.0, so
#     first and last both happen to be right, while the v-prefixed matches are
#     v2.4.0, v3.0.0 — first is the release *before* this one. A check that
#     searches the line for a tag rather than pulling the token out of the
#     heading therefore reads the major as 2 and passes a table that should
#     fail. Which spelling is wrong depends on the search; taking the token by
#     position is what makes it depend on none of them.
#   - "1" is a prefix of "10". A major compared as a prefix rather than as a
#     whole token calls a 10.x release a match for a "1.x" supported row.
#
# Each check here was mutation-tested against check-security-versions.sh on
# 2026-08-27: the rule was removed or loosened, the suite was run, and the row
# that turned red was read. Every mutation moves at least one row, and each
# mutation was confirmed to have actually reached the file first — a
# substitution that silently fails to apply leaves the suite green and reads
# exactly like a check that cannot be broken, which is the false green
# docs/agents/verification-discipline.md warns about. Four mutations came back
# green on the first pass and four assertions were tightened as a result; each
# of those carries a comment at its row saying which. The matrix, as failing
# rows out of 20 checks:
#
#   version read off the end of the heading line ............... 13
#   the linked heading shape not parsed ....................... 13
#   the bare heading shape not parsed .......................... 2
#   the awk `seen` flag dropped (awk's `exit` runs END) ........ 16
#   the version shape "X.Y.Z" not validated .................... 1
#   the second heading used instead of the first .............. 11
#   the mismatch branch never fires ............................ 5
#   the major compared by prefix ............................... 1
#   every table row read, not only the supported one ........... 7
#   the no-supported-row branch removed ........................ 2
#   the supported-row count check removed ...................... 1
#   the empty-cell branch removed .............................. 1
#   a missing CHANGELOG.md not refused ......................... 1
#   a missing SECURITY.md not refused .......................... 1
#
# Read a number here as a measurement with a date on it, not a fact; re-derive
# it rather than citing it. The count depends on how the mutation is spelled,
# and the spellings are not recorded: breaking "read the version off the line"
# by searching for a v-prefixed tag moves a different set of rows than breaking
# it by taking the last token, and a re-run that disagrees with a number above
# is more likely a different mutation than a regression. What is load-bearing
# is the shape, not the count — every mutation moves at least one row, and no
# row is held green only by another rule still standing.

set -uo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CHECKER="$SCRIPT_DIR/check-security-versions.sh"

TMP=$(mktemp -d)
cleanup() {
    rm -rf "$TMP"
}
trap cleanup EXIT

CHECKS=0
FAILURES=0

pass() {
    CHECKS=$((CHECKS + 1))
    echo -e "${GREEN}PASS${NC} $1"
}

fail() {
    CHECKS=$((CHECKS + 1))
    FAILURES=$((FAILURES + 1))
    echo -e "${RED}FAIL${NC} $1"
    shift
    for line in "$@"; do
        echo "     $line"
    done
}

# write_security <path> <table-body...> builds a SECURITY.md around whatever
# table rows it is given, so that a case can be malformed in the table alone
# and still sit in a file shaped like the real one.
write_security() {
    local path="$1"
    shift
    {
        printf '%s\n' '# Security Policy' '' '## Supported Versions' ''
        printf '%s\n' "$@"
        printf '%s\n' '' 'This table is not generated.' '' '## Reporting a Vulnerability' '' 'Report privately.'
    } > "$path"
}

TABLE_HEADER=('| Version | Supported          |' '| ------- | ------------------ |')

# expect_ok <name> <security> <changelog> requires exit 0 and nothing on
# stderr. A check that printed a complaint and exited 0 anyway would be useless
# in CI, so the silence is asserted rather than assumed.
expect_ok() {
    local name="$1" security="$2" changelog="$3"

    "$CHECKER" "$security" "$changelog" > "$TMP/out" 2> "$TMP/err"
    local status=$?
    if [ "$status" -ne 0 ]; then
        fail "$name" "exit status $status" "stderr: $(cat "$TMP/err")"
        return
    fi
    if [ -s "$TMP/err" ]; then
        fail "$name" "exited 0 but wrote to stderr: $(cat "$TMP/err")"
        return
    fi
    pass "$name"
}

# expect_fail <name> <security> <changelog> [needle...] requires a non-zero
# exit and a message on stderr holding each needle. The needles are how the
# "names the file, the value found and the value expected" criterion is
# checked: asserting only the exit status would pass for a check that failed
# with no explanation at all.
expect_fail() {
    local name="$1" security="$2" changelog="$3"
    shift 3

    if "$CHECKER" "$security" "$changelog" > "$TMP/out" 2> "$TMP/err"; then
        fail "$name" "exited 0, expected non-zero" "stdout: $(cat "$TMP/out")"
        return
    fi
    if [ ! -s "$TMP/err" ]; then
        fail "$name" "said nothing on stderr"
        return
    fi
    local needle
    for needle in "$@"; do
        if ! grep -qF -- "$needle" "$TMP/err"; then
            fail "$name" "stderr does not mention: $needle" "stderr: $(cat "$TMP/err")"
            return
        fi
    done
    pass "$name"
}

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}  check-security-versions.sh${NC}"
echo -e "${BLUE}========================================${NC}"
echo

# --- The linked heading shape, which is every release after the first. -------

LINKED="$TMP/linked-changelog.md"
cat > "$LINKED" << 'LINKED_EOF'
# Changelog

## [2.4.0](https://github.com/pedrosousa13/lnpm/compare/v2.3.0...v2.4.0) (2026-08-27)

### Features

* **cli:** something ([#1](https://example.invalid/1))

## [1.0.0](https://github.com/pedrosousa13/lnpm/compare/v0.1.0...v1.0.0) (2026-01-15)

### Features

* first ([#0](https://example.invalid/0))
LINKED_EOF

MATCH_2X="$TMP/security-2x.md"
write_security "$MATCH_2X" "${TABLE_HEADER[@]}" \
    '| 2.x     | :white_check_mark: |' \
    '| 1.x     | :x:                |'

expect_ok "a supported row matching a linked heading" "$MATCH_2X" "$LINKED"

MISMATCH_1X="$TMP/security-1x.md"
write_security "$MISMATCH_1X" "${TABLE_HEADER[@]}" \
    '| 1.x     | :white_check_mark: |' \
    '| 0.x     | :x:                |'

expect_fail "a supported row naming the wrong major" "$MISMATCH_1X" "$LINKED" \
    "$MISMATCH_1X" "1.x" "2.x" "2.4.0"

# --- The bare heading shape, which is what the first release got. ------------

BARE="$TMP/bare-changelog.md"
cat > "$BARE" << 'BARE_EOF'
# Changelog

## 1.0.0 (2026-01-15)

### Features

* first ([#0](https://example.invalid/0))
BARE_EOF

MATCH_1X="$TMP/security-1x-supported.md"
write_security "$MATCH_1X" "${TABLE_HEADER[@]}" \
    '| 1.x     | :white_check_mark: |'

expect_ok "a supported row matching a bare heading" "$MATCH_1X" "$BARE"
expect_fail "a bare heading against the wrong major" "$MATCH_2X" "$BARE" \
    "$MATCH_2X" "2.x" "1.x" "1.0.0"

# --- The compare-URL trap. ---------------------------------------------------
#
# 3.0.0's heading ends in "v2.4.0", the tag of the release before it. This is
# the release-please PR for a new major, which is exactly the commit this check
# exists to fail on: SECURITY.md still says 2.x and has to be updated on that
# same PR. A check reading the version off the end of the heading line would
# see 2.4.0, call the table current, and stay green through the one event it
# was written for.

MAJOR_BUMP="$TMP/major-bump-changelog.md"
cat > "$MAJOR_BUMP" << 'MAJOR_BUMP_EOF'
# Changelog

## [3.0.0](https://github.com/pedrosousa13/lnpm/compare/v2.4.0...v3.0.0) (2026-09-01)

### Features

* **cli:** a breaking change ([#2](https://example.invalid/2))

## [2.4.0](https://github.com/pedrosousa13/lnpm/compare/v2.3.0...v2.4.0) (2026-08-27)

### Features

* **cli:** something ([#1](https://example.invalid/1))
MAJOR_BUMP_EOF

expect_fail "a major release whose predecessor tag still matches the table" \
    "$MATCH_2X" "$MAJOR_BUMP" "$MATCH_2X" "2.x" "3.x" "3.0.0"

MATCH_3X="$TMP/security-3x.md"
write_security "$MATCH_3X" "${TABLE_HEADER[@]}" \
    '| 3.x     | :white_check_mark: |' \
    '| 2.x     | :x:                |'

# The companion. Without it the row above could be passing because everything
# fails on this fixture rather than because the major was read as 3.
expect_ok "the same release with the table updated" "$MATCH_3X" "$MAJOR_BUMP"

# --- The prefix trap. --------------------------------------------------------

TEN="$TMP/ten-changelog.md"
cat > "$TEN" << 'TEN_EOF'
# Changelog

## [10.1.0](https://github.com/pedrosousa13/lnpm/compare/v10.0.0...v10.1.0) (2027-01-01)

### Features

* **cli:** ten ([#3](https://example.invalid/3))
TEN_EOF

expect_fail "1.x is not a match for a 10.x release" "$MATCH_1X" "$TEN" \
    "$MATCH_1X" "1.x" "10.x" "10.1.0"

MATCH_10X="$TMP/security-10x.md"
write_security "$MATCH_10X" "${TABLE_HEADER[@]}" \
    '| 10.x    | :white_check_mark: |' \
    '| 9.x     | :x:                |'

expect_ok "10.x is a match for a 10.x release" "$MATCH_10X" "$TEN"

# --- Only the supported row is checked. --------------------------------------
#
# The unsupported rows are out of scope per #438: whether a major moves from
# supported to unsupported is a maintainer policy judgement. This row pins that
# as behaviour rather than leaving it as an intention — the table below lists
# an unsupported major *above* the current release and one that never existed,
# and the check is required not to care.

ODD_UNSUPPORTED="$TMP/security-odd-unsupported.md"
write_security "$ODD_UNSUPPORTED" "${TABLE_HEADER[@]}" \
    '| 9.x     | :x:                |' \
    '| 2.x     | :white_check_mark: |' \
    '| 0.x     | :x:                |'

expect_ok "the unsupported rows are not checked" "$ODD_UNSUPPORTED" "$LINKED"

# --- Malformed tables. -------------------------------------------------------
#
# Each of these is a way the table can stop stating a supported major without
# any row being visibly wrong. Passing them would leave the check reporting
# success over a file that no longer says anything.

NO_TABLE="$TMP/security-no-table.md"
{
    printf '%s\n' '# Security Policy' '' '## Supported Versions' '' \
        'Only the latest major is supported.' '' '## Reporting a Vulnerability'
} > "$NO_TABLE"

# The phrase again. Delete the no-row branch and a file with no table falls
# through to the empty-cell message, which names the file and exits non-zero
# but describes a cell that does not exist — measured, by deleting the branch
# and watching this row and the one below it both stay green.
expect_fail "a SECURITY.md with no table at all" "$NO_TABLE" "$LINKED" \
    "$NO_TABLE" "has no supported-versions row"

NO_SUPPORTED="$TMP/security-none-supported.md"
write_security "$NO_SUPPORTED" "${TABLE_HEADER[@]}" \
    '| 2.x     | :x:                |' \
    '| 1.x     | :x:                |'

expect_fail "a table with no supported row" "$NO_SUPPORTED" "$LINKED" \
    "$NO_SUPPORTED" "has no supported-versions row"

TWO_SUPPORTED="$TMP/security-two-supported.md"
write_security "$TWO_SUPPORTED" "${TABLE_HEADER[@]}" \
    '| 2.x     | :white_check_mark: |' \
    '| 1.x     | :white_check_mark: |'

# "marks 2 rows" rather than just the two majors. Both of them appear in the
# ordinary mismatch message too, so a run that had lost the count check
# entirely would fall through to that message and satisfy a looser needle —
# measured, by deleting the count check and watching this row stay green.
expect_fail "a table with two supported rows" "$TWO_SUPPORTED" "$LINKED" \
    "$TWO_SUPPORTED" "marks 2 rows" "2.x" "1.x"

EMPTY_CELL="$TMP/security-empty-cell.md"
write_security "$EMPTY_CELL" "${TABLE_HEADER[@]}" \
    '|         | :white_check_mark: |' \
    '| 1.x     | :x:                |'

# A phrase, for the same reason as the row above: an empty cell compared
# against "2.x" is a mismatch, so without it this row stays green with the
# empty-cell branch deleted and the generic message in its place. The phrase
# rather than the bare word "empty", because the fixture's own path contains
# that word and the message names the path — measured, by deleting the branch
# and watching a needle of "empty" match the filename and keep the row green.
expect_fail "a supported row with an empty version cell" "$EMPTY_CELL" "$LINKED" \
    "$EMPTY_CELL" "has an empty Version cell"

# The phrase matters, not just the exit status. With the -f test deleted, awk
# is handed a path that does not exist, fails on its own, and prints a message
# naming that path — so a row asserting only "non-zero, and the file is named"
# stays green over a check that no longer says what is wrong. Measured, by
# deleting the -f test and watching this row pass.
MISSING_SECURITY="$TMP/absent-security.md"
expect_fail "a SECURITY.md that does not exist" "$MISSING_SECURITY" "$LINKED" \
    "$MISSING_SECURITY" "no security policy file at"

# --- Malformed changelogs. ---------------------------------------------------

# Same shape as the SECURITY.md row above, and measured the same way: without
# the phrase, deleting the -f test leaves this green on awk's own complaint.
MISSING_CHANGELOG="$TMP/absent-changelog.md"
expect_fail "a changelog that does not exist" "$MATCH_2X" "$MISSING_CHANGELOG" \
    "$MISSING_CHANGELOG" "no changelog file at"

EMPTY_CHANGELOG="$TMP/empty-changelog.md"
: > "$EMPTY_CHANGELOG"
expect_fail "a changelog that exists but is empty" "$MATCH_2X" "$EMPTY_CHANGELOG" "$EMPTY_CHANGELOG"

NO_HEADING="$TMP/no-heading-changelog.md"
{
    printf '%s\n' '# Changelog' '' 'No releases yet.'
} > "$NO_HEADING"
expect_fail "a changelog with no version heading" "$MATCH_2X" "$NO_HEADING" "$NO_HEADING"

UNPARSEABLE="$TMP/unparseable-changelog.md"
{
    printf '%s\n' '# Changelog' '' '## Unreleased' '' '* nothing yet'
} > "$UNPARSEABLE"
# "does not name a version" rather than the path alone. Drop the shape check
# and "Unreleased" is carried through as if it were a version, producing an
# expected major of "Unreleased.x" and an ordinary mismatch message — which
# names the changelog too, so the looser row stayed green. Measured.
expect_fail "a top heading that is not a version" "$MATCH_2X" "$UNPARSEABLE" \
    "$UNPARSEABLE" "does not name a version"

# --- Defaults. ---------------------------------------------------------------
#
# With no arguments the check reads the SECURITY.md and CHANGELOG.md beside the
# script's parent directory. Checking that on a copied tree keeps the repo's
# own files out of the run — see this file's header.

mkdir -p "$TMP/repo/scripts"
cp "$CHECKER" "$TMP/repo/scripts/check-security-versions.sh"
cp "$MATCH_2X" "$TMP/repo/SECURITY.md"
cp "$LINKED" "$TMP/repo/CHANGELOG.md"

if "$TMP/repo/scripts/check-security-versions.sh" > "$TMP/out" 2> "$TMP/err"; then
    pass "the default paths are the repo's SECURITY.md and CHANGELOG.md"
else
    fail "the default paths are the repo's SECURITY.md and CHANGELOG.md" \
        "stdout: $(cat "$TMP/out")" "stderr: $(cat "$TMP/err")"
fi

# The companion, so the row above is not passing because the defaults resolve
# to something that happens to agree with itself: swapping in a mismatched
# table over the same tree must fail.
cp "$MISMATCH_1X" "$TMP/repo/SECURITY.md"
if "$TMP/repo/scripts/check-security-versions.sh" > "$TMP/out" 2> "$TMP/err"; then
    fail "the defaults catch a mismatch too" "exited 0" "stdout: $(cat "$TMP/out")"
elif grep -qF -- "$TMP/repo/SECURITY.md" "$TMP/err"; then
    pass "the defaults catch a mismatch too"
else
    fail "the defaults catch a mismatch too" \
        "stderr does not name $TMP/repo/SECURITY.md" "stderr: $(cat "$TMP/err")"
fi

echo
if [ "$FAILURES" -eq 0 ]; then
    echo -e "${GREEN}$CHECKS checks, all passed${NC}"
    exit 0
fi

echo -e "${RED}$CHECKS checks, $FAILURES failed${NC}"
exit 1
