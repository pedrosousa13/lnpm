#!/bin/bash
# Changelog section extractor: print one version's section of CHANGELOG.md (#410)
# Run from anywhere: ./scripts/changelog-section.sh <version> [changelog-path]
#
# Prints the section for <version> — its "## " heading line through to just
# before the next "## " heading, with trailing blank lines trimmed. <version>
# is accepted with or without a leading "v", so both "2.3.0" and the tag name
# "v2.3.0" work. The changelog defaults to the CHANGELOG.md beside this
# script's parent directory; pass a path to read another file.
#
# The release workflow feeds this to `gh release edit --notes-file`, which is
# why the failure mode gets more care here than the success one: a version with
# no section in the file exits non-zero with a message on stderr rather than
# printing nothing, because an empty notes file would blank the body of an
# already published release. For the same reason nothing but the section is
# written to stdout — no colors, no headers, unlike the other scripts here.
#
# Three ways of producing a body get the same treatment, because a *wrong* body
# overwrites a published release just as an empty one does: no section at all,
# a section that is a heading with nothing under it, and a section whose text
# opens a code fence that the file never closes. Each exits 1 with a message
# and writes nothing to stdout.
#
# A fourth shape is not caught, and guarding it would cost more than it saves.
# A fenced example indented by four spaces or a tab is an indented code block
# rather than a fence, so an unindented "## " line inside it does end the
# section: the output is the heading, the prose and the fence line, exit 0 —
# measured, not reasoned. That is CommonMark-correct, and refusing it would
# mean refusing indented code blocks, which are ordinary markdown — both halves
# checked against markdown-it 3.0.0 in commonmark mode, which parses the
# indented line as a code block and the "## " after it as a heading, and parses
# a fence indented four spaces inside a "1.  " list item as a fence. So the
# behaviour is pinned by test-changelog-section.sh instead, and
# docs/releasing.md tells note authors to indent by three spaces or none.
#
# Matching is on the version alone and never on a substring of the heading
# line. Every heading release-please writes carries a compare URL ending in the
# *previous* version's tag —
# "## [2.3.0](https://github.com/o/r/compare/v2.2.1...v2.3.0) (2026-08-24)" —
# so a line-wide search for "2.2.1" finds 2.3.0's heading first and returns the
# wrong section. The awk below pulls the version token out of the heading and
# compares it as a whole string, which also keeps "2.2.1" from matching
# "2.2.10". Both heading shapes this repo's changelog holds are parsed: the
# linked form above, and the bare "## 1.0.0 (2026-01-15)" that the first
# release got.

set -euo pipefail

usage() {
    echo "Usage: $0 <version> [changelog-path]" >&2
    echo >&2
    echo "  <version>         2.3.0 or v2.3.0" >&2
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

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
    usage
    exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

VERSION="${1#v}"
CHANGELOG="${2:-$PROJECT_ROOT/CHANGELOG.md}"

if [ -z "$VERSION" ]; then
    echo "error: empty version" >&2
    usage
    exit 2
fi

if [ ! -f "$CHANGELOG" ]; then
    echo "error: no changelog file at $CHANGELOG" >&2
    exit 2
fi

# A "## " line inside a fenced code block is not a section boundary. The
# release notes are hand-written prose (docs/releasing.md step 3), and a fenced
# markdown example is an ordinary thing to write there — before this was
# tracked, extracting a version whose note contained one printed the heading,
# the prose and the opening fence, then stopped, and exited 0.
#
# Fences are recognised the way CommonMark defines them rather than as a
# literal "```": up to three leading spaces, then three or more backticks or
# tildes, optionally followed by an info string. The block closes on a line
# whose fence uses the same character, is at least as long, and carries nothing
# but whitespace after it, so a shorter fence inside a longer one does not end
# the block. A backtick fence whose info string contains a backtick is not a
# fence at all. Every rule here is the CommonMark spec's, from its "Fenced code
# blocks" section.
#
# Each of those rules has a case in test-changelog-section.sh, and that was
# established by mutating the rule out of the awk below one at a time and
# reading which row went red — not by reading the suite and matching names.
# The claim was false when it was first written: three of the rules had no case
# at all, and dropping the closing fence's blank-remainder test, loosening the
# indent limit from `i > 4` to `i > 5`, and loosening the length floor from
# `n < 3` to `n < 1` each left every check passing. Each now moves exactly one
# row. Re-derive it the same way rather than trusting this paragraph.
#
# Command substitution strips every trailing newline, which is the trailing
# blank line trim; the printf below puts exactly one back.
#
# awk reports the two unusable-body cases through its exit status rather than
# stderr, so the message wording lives here with the others and nothing has to
# assume /dev/stderr is writable.
AWK_STATUS=0
SECTION=$(awk -v want="$VERSION" '
    function fence_at(line,   i, c, n) {
        i = 1
        while (i <= 4 && substr(line, i, 1) == " ") i++
        if (i > 4) return 0
        c = substr(line, i, 1)
        if (c != "`" && c != "~") return 0
        n = 0
        while (substr(line, i + n, 1) == c) n++
        if (n < 3) return 0
        f_char = c
        f_len = n
        f_rest = substr(line, i + n)
        return 1
    }
    {
        if (fence_at($0)) {
            if (fence_char == "") {
                if (f_char == "~" || index(f_rest, "`") == 0) {
                    fence_char = f_char
                    fence_len = f_len
                }
            } else if (f_char == fence_char && f_len >= fence_len && f_rest ~ /^[ \t]*$/) {
                fence_char = ""
            }
        } else if (fence_char == "" && substr($0, 1, 3) == "## ") {
            # The stop condition, stated literally. For a heading naming a
            # different version the assignment at the end of this block would
            # clear in_section anyway — but deleting the line moves two rows of
            # test-changelog-section.sh, measured, and both are cases the
            # assignment cannot cover: a changelog that repeats one heading
            # splices both sections together instead of yielding the first,
            # because "found" is true again; and a heading-only section
            # followed by another section absorbs that one, which makes the
            # has_content check below pass on borrowed content.
            if (in_section) exit
            heading = substr($0, 4)
            if (substr(heading, 1, 1) == "[") {
                close_bracket = index(heading, "]")
                found = (close_bracket > 2) && (substr(heading, 2, close_bracket - 2) == want)
            } else {
                space = index(heading, " ")
                if (space == 0) space = length(heading) + 1
                found = (substr(heading, 1, space - 1) == want)
            }
            in_section = found
        }
        if (in_section) {
            section_lines++
            if (section_lines > 1 && NF > 0) has_content = 1
            print
        }
    }
    END {
        # Reached only at end of file or at the next heading, and the heading
        # branch runs only outside a fence — so an open fence here means the
        # section ran to the end of the file inside a code block, swallowing
        # every later version with it.
        if (in_section && fence_char != "") exit 3
        if (in_section && !has_content) exit 4
    }
' "$CHANGELOG") || AWK_STATUS=$?

case "$AWK_STATUS" in
    0) ;;
    3)
        echo "error: the section for version $VERSION in $CHANGELOG opens a code fence that is never closed" >&2
        exit 1
        ;;
    4)
        echo "error: the section for version $VERSION in $CHANGELOG is a heading with nothing under it" >&2
        exit 1
        ;;
    *)
        echo "error: could not read $CHANGELOG (awk exited $AWK_STATUS)" >&2
        exit 1
        ;;
esac

if [ -z "$SECTION" ]; then
    echo "error: no section for version $VERSION in $CHANGELOG" >&2
    exit 1
fi

printf '%s\n' "$SECTION"
