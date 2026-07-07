TITLE: Rewrite isExcluded to satisfy gitignore and npm ignore semantics
LABELS: bug, security
---
## Severity

Medium-High. A root-anchored `.npmignore` pattern such as `/credentials.json` currently matches nothing, so a file the developer meant to exclude is copied into the shared store at `~/.lnpm` and linked into every consuming project on the machine — a silent secret leak.

## Background

`lnpm publish` copies a package's files into a shared store, which is linked into every project that consumes the package. The decision of which files to copy is made per-path by `isExcluded(relPath, patterns)` in `internal/pack/pack.go`. The patterns are the package's `.npmignore` or `.gitignore` lines plus a built-in default-exclude list. `isExcluded` today handles only exact matches, a `/**` suffix, basename globs, full-path globs, and a `prefix/` directory-prefix check. It does not handle trailing-slash directory patterns, leading-slash root anchoring, or negation. This issue makes `isExcluded` follow standard gitignore/npm semantics so the failing tests from the test-suite issue pass.

## Problem

Given a package whose `.npmignore` contains:

```
/credentials.json
dist/
!dist/keep.js
```

Expected behavior (matching git and npm): `credentials.json` at the root is excluded, everything under `dist/` is excluded, but `dist/keep.js` is re-included. Actual behavior today: `credentials.json` is published (leading slash never stripped), `dist/` matches nothing (the exact-match and `filepath.Match` branches both fail, and the prefix branch compares against the literal `"dist//"`), and `!dist/keep.js` is skipped entirely so it can never re-include anything.

## Where to look

- `internal/pack/pack.go:311-353` — `isExcluded`, the function to rewrite. Trace each branch:
  - `internal/pack/pack.go:316-318` — negation (`!...`) skipped via `continue`.
  - `internal/pack/pack.go:321-327` — `/**` suffix handling.
  - `internal/pack/pack.go:330-332` — exact match.
  - `internal/pack/pack.go:336-345` — basename glob (no `/` in pattern) vs full-path glob.
  - `internal/pack/pack.go:348-350` — `prefix/` directory-prefix check. This is where `dist/` breaks: `pattern+"/"` becomes `"dist//"`.
- `internal/pack/pack.go:145` — `collectFiles` appends `defaultExcludes` after the loaded ignore patterns, so both flow through `isExcluded`. Default excludes must keep winning.
- `internal/pack/pack.go:55-86` — `defaultExcludes`, the always-excluded list (`.git`, `node_modules`, `.env`, `.npmrc`, etc.).
- `internal/pack/pack.go:179-196` — where `collectFiles` calls `isExcluded`, then applies the `files` whitelist via `isIncluded`/`isDefaultInclude`. Note excludes are checked before the whitelist, so default excludes win even in whitelist mode.
- `internal/pack/pack.go:170-173` — symlink skipping in the walk. Preserve it.
- `internal/pack/pack.go:462-511` — `filterGitFiles`/`isGitRelatedPath`, the defense-in-depth `.git` filter applied after collection. Preserve it.

## How to fix

Pick one of two approaches. Prefer the library approach unless adding a dependency is undesirable.

Approach A — adopt a gitignore library:
1. Add a dependency such as `github.com/sabhiram/go-gitignore` or reuse go-git's `gitignore` package (the repo already vendors go-git-adjacent tooling; check `go.mod` first).
2. Compile the loaded patterns plus `defaultExcludes` into a matcher, then replace the body of `isExcluded` with a call to that matcher. Keep the `isExcluded(relPath, patterns)` signature so `collectFiles` and the existing tests do not change.
3. Ensure negation ordering is preserved: patterns must be fed to the matcher in file order, with `defaultExcludes` appended last so they cannot be negated away.

Approach B — extend `isExcluded` by hand:
1. Track a running result and let the last matching pattern win (gitignore semantics), instead of returning `true` on the first match.
2. For each pattern, detect a leading `!` as negation; strip it and remember that a match should set the result to "not excluded".
3. Strip a leading `/` and treat the remaining pattern as anchored to the package root (match only against the full `relPath`, not the basename).
4. Strip a trailing `/` and treat the pattern as a directory: it matches the directory path itself and everything under it (`relPath == p || strings.HasPrefix(relPath, p+"/")`).
5. Keep the existing exact, `/**`, basename-glob, full-path-glob, and directory-prefix behaviors for the remaining forms.
6. Append `defaultExcludes` after the user patterns (already done at `pack.go:145`) so a user `!` cannot re-include a default-excluded path — a default exclude must always win.

For both approaches:
7. Remove the `t.Skip` guards added in the test-suite issue and make those cases pass.
8. Do not touch symlink skipping (`pack.go:170-173`) or the `.git` filter (`pack.go:462-511`).

## Acceptance criteria

- [ ] Trailing-slash directory patterns (`dist/`) exclude the directory and its contents.
- [ ] Root-anchored patterns (`/credentials.json`, `/dist`) match only at the package root and do exclude the intended file.
- [ ] Negation (`!pattern`) re-includes a previously excluded file, with last-matching-pattern-wins ordering.
- [ ] Default excludes still always win, including in `files`-whitelist mode, and cannot be negated away by a user pattern.
- [ ] Symlinks are still skipped and the `.git` safety filter still runs.
- [ ] The previously skipped cases from the test-suite issue now pass with their `t.Skip` removed.

## Testing

Run the pack tests, including the suite from the test-suite issue:

```
go test -v -run TestIsExcluded ./internal/pack/
```

Run the full suite to confirm no regression in whitelist mode or the `.git` filter:

```
go test ./...
```

Add an integration-style test in `internal/pack/pack_test.go` that builds a temp package with a `.npmignore` containing `/credentials.json`, `dist/`, and `!dist/keep.js`, runs `Pack`, and asserts `credentials.json` and `dist/drop.js` are absent while `dist/keep.js` is present. Model it on `TestPack` (`internal/pack/pack_test.go:164-240`) and `TestNpmPackIntegrationWithGitFiles` (`internal/pack/git_filter_test.go:72-139`).
