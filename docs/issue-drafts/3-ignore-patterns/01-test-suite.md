TITLE: Add a failing test suite that codifies gitignore and npm ignore semantics for isExcluded
LABELS: tests
---
## Severity

Medium. No production behavior changes here, but this suite defines the contract that the follow-up fix must satisfy, and it exposes the exact cases that currently leak files.

## Background

`lnpm publish` copies a package's files into a shared store at `~/.lnpm`, which is then linked into every project on the machine that consumes the package. The set of files to copy is decided by walking the package directory and calling `isExcluded(relPath, patterns)` on each path. The patterns come from the package's `.npmignore` or `.gitignore` file plus a built-in default-exclude list. `isExcluded` lives in `internal/pack/pack.go`.

Today `isExcluded` implements only a subset of the pattern syntax that `.gitignore` and `.npmignore` support. Some common patterns silently match nothing. This issue adds a test suite that pins down what the correct behavior should be. It only adds tests; the fix that makes them pass is a separate issue. Tests that assert not-yet-implemented behavior should be marked so they clearly document the gap rather than break the build.

## Problem

Three pattern forms behave incorrectly today. A concrete scenario: a developer has a package with a file `credentials.json` at the package root and adds the line `/credentials.json` to `.npmignore`, expecting it to stay out of the published package. Because the current matcher never strips the leading `/`, the pattern matches nothing, so `credentials.json` is copied into the shared store and linked into every consuming project. The developer has no signal that their ignore rule did nothing.

The other two forms:
- Trailing-slash directory patterns: `dist/` is intended to exclude the `dist` directory and everything under it, but matches nothing today.
- Negation: `!keep.js` is intended to re-include a file that an earlier pattern excluded, but is skipped entirely today, so the file stays excluded.

## Where to look

- `internal/pack/pack.go:311-353` — `isExcluded`, the function under test. Read it to understand the five current branches (negation skip, `/**` suffix, exact match, basename/full-path glob, directory prefix).
- `internal/pack/pack.go:316-318` — negation patterns (`!...`) are skipped with a `continue`.
- `internal/pack/pack.go:321-327` — trailing `/**` suffix handling (this is the only directory-style form that works today).
- `internal/pack/pack.go:330-350` — exact match, basename glob, full-path glob, and `prefix/` directory-prefix branches.
- `internal/pack/pack_test.go:85-114` — existing `TestIsExcluded`, a table-driven test. Mirror its structure (slice of `{path, patterns, want}` structs, `t.Run` per case).
- `internal/pack/git_filter_test.go:9-38` — another table-driven example in the same package to match style.

## How to fix

1. In `internal/pack/pack_test.go`, add a new table-driven test function (for example `TestIsExcludedGitignoreSemantics`) alongside the existing `TestIsExcluded`. Use the same `{path string; patterns []string; want bool}` shape.
2. Add cases for trailing-slash directory patterns:
   - `{"dist/index.js", []string{"dist/"}, true}`
   - `{"dist", []string{"dist/"}, true}`
   - `{"src/index.ts", []string{"dist/"}, false}`
3. Add cases for root-anchored patterns (leading `/` anchors to the package root):
   - `{"credentials.json", []string{"/credentials.json"}, true}`
   - `{"dist/index.js", []string{"/dist"}, true}`
   - `{"src/dist/index.js", []string{"/dist"}, false}` (anchored to root, so a nested `dist` is not matched)
4. Add cases for negation (last matching pattern wins; `!` re-includes):
   - `{"dist/keep.js", []string{"dist/", "!dist/keep.js"}, false}`
   - `{"dist/drop.js", []string{"dist/", "!dist/keep.js"}, true}`
5. These cases fail against the current `isExcluded`. So the suite is committable and green now, guard the not-yet-implemented cases: either put them in a test that begins with `t.Skip("pending isExcluded rewrite — see follow-up issue")`, or split them into a separate test function that calls `t.Skip`. Add a comment on each block naming the semantic it encodes (trailing slash, anchoring, negation).
6. Do not change `isExcluded` itself in this issue.

## Acceptance criteria

- [ ] A new table-driven test in `internal/pack/pack_test.go` enumerates trailing-slash, root-anchored, and negation cases with expected results.
- [ ] Each group has a comment naming the gitignore/npm semantic it documents.
- [ ] Cases that current code cannot pass are marked with `t.Skip` (with a message pointing at the follow-up) so `go test ./...` is green.
- [ ] The existing `TestIsExcluded` is left unchanged and still passes.

## Testing

Run the pack package tests:

```
go test ./internal/pack/
```

Confirm the new test is present and either skipped or passing (never failing) with verbose output:

```
go test -v -run TestIsExcluded ./internal/pack/
```

The skipped cases become the acceptance target for the follow-up fix issue, which will remove the `t.Skip` and make them pass.
