#!/bin/bash
# Tests for scripts/changelog-section.sh (#410)
# Run from anywhere: ./scripts/test-changelog-section.sh
#
# Every case runs against a fixture changelog built here, never against the
# repo's own CHANGELOG.md: the real file gains a section at every release, so a
# test reading it would have to be edited each time or would start passing for
# the wrong reason.
#
# The fixture is built so that the two ways of getting the wrong section are
# both live in it. Its 2.2.10 heading carries "...v2.2.1...v2.2.10", so a
# search for "2.2.1" anywhere on a heading line finds 2.2.10's heading first
# and a prefix comparison matches it too. Its 2.3.0 heading carries "v2.2.10",
# so the same holds one section further up. A run that returns the requested
# section from this fixture cannot be doing either.
#
# Output is compared as files rather than as shell strings, because command
# substitution strips trailing newlines and would hide exactly the trailing
# blank line trim these tests are checking.

set -uo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
EXTRACTOR="$SCRIPT_DIR/changelog-section.sh"

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

FIXTURE="$TMP/CHANGELOG.md"

# Trailing blank lines are deliberate in two places: three before "## 1.0.0",
# and two at the end of the file. Both have to be trimmed off the section that
# precedes them.
cat > "$FIXTURE" << 'FIXTURE_EOF'
# Changelog

## [2.3.0](https://github.com/pedrosousa13/lnpm/compare/v2.2.10...v2.3.0) (2026-08-24)

> **Upgrade note for 2.3.0.** The hand-written part, which is the whole point.

### Bug Fixes

* **pack:** newest ([#3](https://example.invalid/3))

## [2.2.10](https://github.com/pedrosousa13/lnpm/compare/v2.2.1...v2.2.10) (2026-08-23)

### Bug Fixes

* **pack:** ten ([#2](https://example.invalid/2))

## [2.2.1](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.2.1) (2026-08-22)

> **Upgrade note for 2.2.1.** Also hand-written.

### Bug Fixes

* **pack:** one ([#1](https://example.invalid/1))



## 1.0.0 (2026-01-15)

### Features

* first ([#0](https://example.invalid/0))


FIXTURE_EOF

# expect_section <name> <version> [changelog] reads the expected section from
# stdin and compares it byte for byte with what the extractor prints.
expect_section() {
    local name="$1" version="$2" changelog="${3:-$FIXTURE}"
    cat > "$TMP/expected"

    "$EXTRACTOR" "$version" "$changelog" > "$TMP/actual" 2> "$TMP/err"
    local status=$?
    if [ "$status" -ne 0 ]; then
        fail "$name" "exit status $status" "stderr: $(cat "$TMP/err")"
        return
    fi
    if ! diff -u "$TMP/expected" "$TMP/actual" > "$TMP/diff" 2>&1; then
        fail "$name"
        sed 's/^/     /' "$TMP/diff"
        return
    fi
    pass "$name"
}

# expect_failure <name> <args...> requires a non-zero exit, nothing at all on
# stdout, and a message on stderr. The empty stdout is the load-bearing half:
# the workflow pipes this into `gh release edit`, and an empty body would wipe
# a published release's notes.
expect_failure() {
    local name="$1"
    shift

    if "$EXTRACTOR" "$@" > "$TMP/actual" 2> "$TMP/err"; then
        fail "$name" "exited 0, expected non-zero" "stdout: $(cat "$TMP/actual")"
        return
    fi
    if [ -s "$TMP/actual" ]; then
        fail "$name" "wrote to stdout: $(cat "$TMP/actual")"
        return
    fi
    if [ ! -s "$TMP/err" ]; then
        fail "$name" "said nothing on stderr"
        return
    fi
    pass "$name"
}

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}  changelog-section.sh${NC}"
echo -e "${BLUE}========================================${NC}"
echo

expect_section "the first section in the file" 2.3.0 << 'EOF'
## [2.3.0](https://github.com/pedrosousa13/lnpm/compare/v2.2.10...v2.3.0) (2026-08-24)

> **Upgrade note for 2.3.0.** The hand-written part, which is the whole point.

### Bug Fixes

* **pack:** newest ([#3](https://example.invalid/3))
EOF

expect_section "a middle section" 2.2.10 << 'EOF'
## [2.2.10](https://github.com/pedrosousa13/lnpm/compare/v2.2.1...v2.2.10) (2026-08-23)

### Bug Fixes

* **pack:** ten ([#2](https://example.invalid/2))
EOF

# 2.2.1 is the trap: it is a prefix of 2.2.10's version and it also appears in
# 2.2.10's compare URL, so both wrong answers sit above the right one.
expect_section "2.2.1 is not 2.2.10, and not the heading whose URL names it" 2.2.1 << 'EOF'
## [2.2.1](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.2.1) (2026-08-22)

> **Upgrade note for 2.2.1.** Also hand-written.

### Bug Fixes

* **pack:** one ([#1](https://example.invalid/1))
EOF

# 1.0.0 is last in the file, has the bare heading shape release-please wrote
# for the first release, and is followed by trailing blank lines.
expect_section "the last section, with a bare heading" 1.0.0 << 'EOF'
## 1.0.0 (2026-01-15)

### Features

* first ([#0](https://example.invalid/0))
EOF

expect_section "a v-prefixed tag name" v2.3.0 << 'EOF'
## [2.3.0](https://github.com/pedrosousa13/lnpm/compare/v2.2.10...v2.3.0) (2026-08-24)

> **Upgrade note for 2.3.0.** The hand-written part, which is the whole point.

### Bug Fixes

* **pack:** newest ([#3](https://example.invalid/3))
EOF

expect_section "a v-prefixed tag naming the last section" v1.0.0 << 'EOF'
## 1.0.0 (2026-01-15)

### Features

* first ([#0](https://example.invalid/0))
EOF

expect_failure "a version with no section" 2.9.9 "$FIXTURE"
expect_failure "a version that is only a prefix of two others" 2.2 "$FIXTURE"
expect_failure "a changelog that does not exist" 2.3.0 "$TMP/absent.md"
expect_failure "no arguments"

# An existing but empty file is a separate path from a missing one: the -f test
# passes, and every failure below it has to hold instead.
: > "$TMP/empty-changelog.md"
expect_failure "a changelog that exists but is empty" 2.3.0 "$TMP/empty-changelog.md"

# A version named only by a compare URL. The main fixture above cannot express
# this: each of its three URLs — v2.2.10, v2.2.1 and v1.0.0 — names a version
# that also has a section of its own, 1.0.0's being the bare heading at the end
# of the file. So the case gets a fixture of its own, shaped like a changelog
# whose earliest entries have been trimmed: the oldest heading still names the
# predecessor it was released against, and that predecessor's section is gone.
# 1.9.0 below therefore appears on exactly one line of the file, inside 2.0.0's
# compare URL, and a search that looks anywhere on a heading line rather than
# at the version token hands back 2.0.0's section for it.
URL_ONLY="$TMP/url-only.md"
cat > "$URL_ONLY" << 'URL_ONLY_EOF'
# Changelog

## [2.1.0](https://github.com/pedrosousa13/lnpm/compare/v2.0.0...v2.1.0) (2026-03-01)

### Bug Fixes

* **pack:** later ([#2](https://example.invalid/2))

## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.9.0...v2.0.0) (2026-02-01)

### Bug Fixes

* **pack:** earlier ([#1](https://example.invalid/1))
URL_ONLY_EOF

expect_failure "a version that only appears inside a compare URL" 1.9.0 "$URL_ONLY"
# The companion: the heading that carries that URL is still found by its own
# version, so the check above is not passing because everything fails.
expect_section "the section whose compare URL names that version" 2.0.0 "$URL_ONLY" << 'EOF'
## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.9.0...v2.0.0) (2026-02-01)

### Bug Fixes

* **pack:** earlier ([#1](https://example.invalid/1))
EOF

# Fenced code blocks. A "## " line inside one is not a section boundary — the
# release notes are hand-written prose, and a fenced markdown example is an
# ordinary thing to write there. Each of the three notes below closes its fence
# a different way: a plain backtick fence with an info string, a four-backtick
# fence that a three-backtick line inside it must not close, and a tilde fence.
FENCED="$TMP/fenced.md"
cat > "$FENCED" << 'FENCED_EOF'
# Changelog

## [3.0.0](https://github.com/pedrosousa13/lnpm/compare/v2.0.0...v3.0.0) (2026-03-01)

> **Upgrade note for 3.0.0.** The heading below is an example, not a boundary:

```markdown
## [9.9.9] this is not a section heading
```

### Bug Fixes

* **pack:** three ([#3](https://example.invalid/3))

## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)

> **Upgrade note for 2.0.0.** A longer fence, holding a shorter one:

````markdown
```
## [9.9.9] still not a section heading
```
````

### Bug Fixes

* **pack:** two ([#2](https://example.invalid/2))

## [1.0.0](https://github.com/pedrosousa13/lnpm/compare/v0.1.0...v1.0.0) (2026-01-01)

> **Upgrade note for 1.0.0.** Tildes fence too, and a backtick line does not
> close a tilde fence:

~~~
```
## [9.9.9] not a section heading either
```
~~~

### Features

* **cli:** one ([#1](https://example.invalid/1))
FENCED_EOF

expect_section "a fenced heading is not a boundary" 3.0.0 "$FENCED" << 'EOF'
## [3.0.0](https://github.com/pedrosousa13/lnpm/compare/v2.0.0...v3.0.0) (2026-03-01)

> **Upgrade note for 3.0.0.** The heading below is an example, not a boundary:

```markdown
## [9.9.9] this is not a section heading
```

### Bug Fixes

* **pack:** three ([#3](https://example.invalid/3))
EOF

expect_section "a shorter fence does not close a longer one" 2.0.0 "$FENCED" << 'EOF'
## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)

> **Upgrade note for 2.0.0.** A longer fence, holding a shorter one:

````markdown
```
## [9.9.9] still not a section heading
```
````

### Bug Fixes

* **pack:** two ([#2](https://example.invalid/2))
EOF

expect_section "a tilde fence, which a backtick line does not close" 1.0.0 "$FENCED" << 'EOF'
## [1.0.0](https://github.com/pedrosousa13/lnpm/compare/v0.1.0...v1.0.0) (2026-01-01)

> **Upgrade note for 1.0.0.** Tildes fence too, and a backtick line does not
> close a tilde fence:

~~~
```
## [9.9.9] not a section heading either
```
~~~

### Features

* **cli:** one ([#1](https://example.invalid/1))
EOF

# The 9.9.9 headings above are inside fences, so they are not sections. A run
# that returns one of them is treating fenced lines as boundaries.
expect_failure "a heading that only appears inside a fence" 9.9.9 "$FENCED"

# The two rules a "starts with ```" check gets wrong in opposite directions. A
# fence may be indented up to three spaces, so 2.0.0's is one and its contents
# are not headings. A backtick fence may not carry a backtick in its info
# string, so 1.0.0's line is not a fence — read as one it would never close,
# and the section would fail instead of ending at the next heading.
EDGES="$TMP/fence-edges.md"
cat > "$EDGES" << 'EDGES_EOF'
# Changelog

## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)

> **Upgrade note for 2.0.0.** An indented fence is still a fence:

   ```markdown
## [9.9.9] this is not a section heading
   ```

### Bug Fixes

* **pack:** two ([#2](https://example.invalid/2))

## [1.0.0](https://github.com/pedrosousa13/lnpm/compare/v0.1.0...v1.0.0) (2026-01-01)

> **Upgrade note for 1.0.0.** The line below is not a fence, because a backtick
> fence may not carry a backtick in its info string:

```js `inline`

### Features

* **cli:** one ([#1](https://example.invalid/1))
EDGES_EOF

expect_section "an indented fence is still a fence" 2.0.0 "$EDGES" << 'EOF'
## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)

> **Upgrade note for 2.0.0.** An indented fence is still a fence:

   ```markdown
## [9.9.9] this is not a section heading
   ```

### Bug Fixes

* **pack:** two ([#2](https://example.invalid/2))
EOF

expect_section "a backtick in the info string means it is not a fence" 1.0.0 "$EDGES" << 'EOF'
## [1.0.0](https://github.com/pedrosousa13/lnpm/compare/v0.1.0...v1.0.0) (2026-01-01)

> **Upgrade note for 1.0.0.** The line below is not a fence, because a backtick
> fence may not carry a backtick in its info string:

```js `inline`

### Features

* **cli:** one ([#1](https://example.invalid/1))
EOF

# The rest of the fence rules the extractor implements. Before this block, the
# comment in changelog-section.sh claimed every rule had a case here, and three
# did not: each of the mutations below left all 25 checks of the day passing.
#
#   - deleting `&& f_rest ~ /^[ \t]*$/`, so a closing fence may carry an info
#     string — moves "a closing fence may not carry an info string"
#   - loosening `if (i > 4) return 0` to `i > 5`, so four spaces of indent
#     still open a fence — moves "four spaces of indent is not a fence"
#   - loosening `if (n < 3) return 0` to `n < 1`, so one fence character
#     opens a block — moves "two fence characters do not open a fence"
#
# Each was applied to scripts/changelog-section.sh, run, and reverted; the row
# named beside it is the one that turned red.
# One fixture per rule rather than one shared file, because a fence rule that
# misfires leaks: the fence state carries across section boundaries, so a
# mutation that leaves a block open swallows every later heading. On a shared
# fixture the first of these three mutations turned all three rows red — one
# of them for its own reason and two of them for that leak — and a reader
# re-running it would have had to work out which. Measured, then split.

# A closing fence may not carry an info string, so the ```text line is content
# and the block runs on to the line after it.
CLOSE_INFO="$TMP/closing-fence-info.md"
cat > "$CLOSE_INFO" << 'CLOSE_INFO_EOF'
# Changelog

## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)

> **Upgrade note for 2.0.0.** The line below looks like a closing fence but
> carries an info string, so it is content and the block runs on:

```markdown
## [9.9.9] not a section heading
```text
## [9.9.8] not a section heading either
```

### Bug Fixes

* **pack:** two ([#2](https://example.invalid/2))

## [1.0.0](https://github.com/pedrosousa13/lnpm/compare/v0.1.0...v1.0.0) (2026-01-01)

### Features

* **cli:** one ([#1](https://example.invalid/1))
CLOSE_INFO_EOF

expect_section "a closing fence may not carry an info string" 2.0.0 "$CLOSE_INFO" << 'EOF'
## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)

> **Upgrade note for 2.0.0.** The line below looks like a closing fence but
> carries an info string, so it is content and the block runs on:

```markdown
## [9.9.9] not a section heading
```text
## [9.9.8] not a section heading either
```

### Bug Fixes

* **pack:** two ([#2](https://example.invalid/2))
EOF

# Four spaces of indent is an indented code block, not a fence. This row pins a
# truncation rather than a rescue, and the expected output says so: the section
# stops at the "## " line inside the would-be fence and the extractor exits 0,
# because nothing about the result is detectably wrong.
#
# CommonMark agrees, and that was checked rather than reasoned: markdown-it
# 3.0.0 in commonmark mode parses the indented "```markdown" line below as a
# code_block and the column-zero "## [9.9.7]" as an h2. The extractor is
# therefore right and the test records the behaviour instead of arguing with
# it. It is the last shape that can hand `gh release edit` a short body without
# failing; docs/releasing.md tells note authors not to write it.
#
# The same parser is why there is no guard: it parses a "```" indented four
# spaces inside a "1.  " list item as a fence, not as a code block, so a guard
# keyed on indentation would refuse legitimate markdown.
INDENT4="$TMP/four-space-fence.md"
cat > "$INDENT4" << 'INDENT4_EOF'
# Changelog

## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)

> **Upgrade note for 2.0.0.** Four spaces of indent is an indented code block,
> not a fence, so the heading below ends this section:

    ```markdown
## [9.9.7] this ends the section
    ```

### Bug Fixes

* **pack:** two ([#2](https://example.invalid/2))

## [1.0.0](https://github.com/pedrosousa13/lnpm/compare/v0.1.0...v1.0.0) (2026-01-01)

### Features

* **cli:** one ([#1](https://example.invalid/1))
INDENT4_EOF

expect_section "four spaces of indent is not a fence" 2.0.0 "$INDENT4" << 'EOF'
## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)

> **Upgrade note for 2.0.0.** Four spaces of indent is an indented code block,
> not a fence, so the heading below ends this section:

    ```markdown
EOF

# Fewer than three fence characters is not a fence, for either character.
SHORT="$TMP/short-fence.md"
cat > "$SHORT" << 'SHORT_EOF'
# Changelog

## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)

> **Upgrade note for 2.0.0.** Two fence characters are not a fence:

`` two backticks do not open one
~~ and neither do two tildes

### Bug Fixes

* **pack:** two ([#2](https://example.invalid/2))

## [1.0.0](https://github.com/pedrosousa13/lnpm/compare/v0.1.0...v1.0.0) (2026-01-01)

### Features

* **cli:** one ([#1](https://example.invalid/1))
SHORT_EOF

expect_section "two fence characters do not open a fence" 2.0.0 "$SHORT" << 'EOF'
## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)

> **Upgrade note for 2.0.0.** Two fence characters are not a fence:

`` two backticks do not open one
~~ and neither do two tildes

### Bug Fixes

* **pack:** two ([#2](https://example.invalid/2))
EOF

# A tab indents by four columns in CommonMark, so a tab-indented fence is an
# indented code block too and truncates a section the same way. None of the
# three mutations above moves this row — the one that does is teaching the
# leading-whitespace scan in fence_at to skip tabs as well as spaces, which was
# applied, run and reverted. The fixture is built with printf so that
# reformatting this file cannot turn the tab into spaces and the case into a
# duplicate of the one above.
TABBED="$TMP/tab-fence.md"
{
    printf '%s\n' \
        '# Changelog' \
        '' \
        '## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)' \
        '' \
        '> **Upgrade note for 2.0.0.** The fence below is indented with a tab:' \
        ''
    printf '\t%s\n' '```markdown'
    printf '%s\n' '## [9.9.7] this ends the section'
    printf '\t%s\n' '```'
    printf '%s\n' \
        '' \
        '### Bug Fixes' \
        '' \
        '* **pack:** two ([#2](https://example.invalid/2))'
} > "$TABBED"

{
    printf '%s\n' \
        '## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)' \
        '' \
        '> **Upgrade note for 2.0.0.** The fence below is indented with a tab:' \
        ''
    printf '\t%s\n' '```markdown'
} > "$TMP/tab-expected"
expect_section "a tab of indent is not a fence either" 2.0.0 "$TABBED" < "$TMP/tab-expected"

# An unclosed fence swallows the rest of the file — CommonMark says the block
# runs to the end of the document — so the extracted "section" would carry
# every later version's notes. That is a wrong body, not a truncated one, and
# it has to fail rather than be published.
UNCLOSED="$TMP/unclosed-fence.md"
cat > "$UNCLOSED" << 'UNCLOSED_EOF'
# Changelog

## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)

> **Upgrade note for 2.0.0.** The fence below is never closed:

```markdown
## [9.9.9] this is not a section heading

## [1.0.0](https://github.com/pedrosousa13/lnpm/compare/v0.1.0...v1.0.0) (2026-01-01)

### Features

* **cli:** one ([#1](https://example.invalid/1))
UNCLOSED_EOF

expect_failure "a fence that is never closed" 2.0.0 "$UNCLOSED"
# 1.0.0's heading is inside that unclosed block, so it is not a section either.
expect_failure "a heading below an unclosed fence" 1.0.0 "$UNCLOSED"

# A heading with nothing under it prints one line and used to exit 0, so the
# workflow's non-empty check passed and the release body became a bare heading.
HEADING_ONLY="$TMP/heading-only.md"
cat > "$HEADING_ONLY" << 'HEADING_ONLY_EOF'
# Changelog

## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v2.0.0) (2026-02-01)

## [1.0.0](https://github.com/pedrosousa13/lnpm/compare/v0.1.0...v1.0.0) (2026-01-01)

### Features

* **cli:** one ([#1](https://example.invalid/1))

## [0.1.0](https://github.com/pedrosousa13/lnpm/compare/v0.0.1...v0.1.0) (2025-12-01)
HEADING_ONLY_EOF

expect_failure "a section that is a heading and a blank line" 2.0.0 "$HEADING_ONLY"
expect_failure "a heading-only section at the end of the file" 0.1.0 "$HEADING_ONLY"
# The section between them is unaffected: the guard is about content, not about
# the neighbours.
expect_section "a section between two heading-only ones" 1.0.0 "$HEADING_ONLY" << 'EOF'
## [1.0.0](https://github.com/pedrosousa13/lnpm/compare/v0.1.0...v1.0.0) (2026-01-01)

### Features

* **cli:** one ([#1](https://example.invalid/1))
EOF

# The extractor's own comment claims a repeated heading yields the first
# section rather than splicing two together. Nothing pinned that claim.
DUPLICATE="$TMP/duplicate-heading.md"
cat > "$DUPLICATE" << 'DUPLICATE_EOF'
# Changelog

## [1.5.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v1.5.0) (2026-02-01)

### Bug Fixes

* **pack:** the first one ([#1](https://example.invalid/1))

## [1.5.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v1.5.0) (2026-02-01)

### Bug Fixes

* **pack:** the second one ([#2](https://example.invalid/2))
DUPLICATE_EOF

expect_section "a repeated heading yields the first section alone" 1.5.0 "$DUPLICATE" << 'EOF'
## [1.5.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v1.5.0) (2026-02-01)

### Bug Fixes

* **pack:** the first one ([#1](https://example.invalid/1))
EOF

# The default changelog is the CHANGELOG.md beside the script's parent
# directory. Checking that on a copied tree keeps the real file out of it.
mkdir -p "$TMP/repo/scripts"
cp "$EXTRACTOR" "$TMP/repo/scripts/changelog-section.sh"
cp "$FIXTURE" "$TMP/repo/CHANGELOG.md"
if "$TMP/repo/scripts/changelog-section.sh" 2.2.1 > "$TMP/actual" 2> "$TMP/err" &&
    head -1 "$TMP/actual" | grep -q '^## \[2.2.1\]'; then
    pass "the default changelog path is the repo's CHANGELOG.md"
else
    fail "the default changelog path is the repo's CHANGELOG.md" \
        "stdout: $(head -1 "$TMP/actual")" "stderr: $(cat "$TMP/err")"
fi

echo
if [ "$FAILURES" -eq 0 ]; then
    echo -e "${GREEN}$CHECKS checks, all passed${NC}"
    exit 0
fi

echo -e "${RED}$CHECKS checks, $FAILURES failed${NC}"
exit 1
