TITLE: Align ignore-pattern matching with gitignore and npm semantics
LABELS: epic, security, bug
---
## Overview

`lnpm publish` decides which files to copy into the shared store (`~/.lnpm`) by walking the package directory and testing each file against three sources of exclude patterns: the package.json `files` whitelist, the package's `.npmignore`/`.gitignore` file, and a built-in list of default excludes. The function that decides whether a path is excluded is `isExcluded` in `internal/pack/pack.go`. Separately, `lnpm publish --all` expands the workspace globs in `internal/workspace/workspace.go` to find every package to publish.

Both matchers implement only a small subset of the pattern syntax that developers expect from `.gitignore` and `.npmignore`. Several common and important pattern forms silently do nothing. Because the store is linked into every consuming project on the machine, a pattern that fails to exclude a file means that file is copied into the store and propagated everywhere.

## Why this matters

The most serious consequence is a secret leak. If a developer adds a root-anchored pattern such as `/credentials.json` to their `.npmignore` expecting the file to stay out of the published package, the current matcher never strips the leading slash, so the pattern matches nothing and the secret is copied into the shared store and linked into every project that consumes the package. Trailing-slash directory patterns (`dist/`) and workspace negation patterns (`!packages/internal-secret`) fail in similar ways: patterns the developer wrote in good faith do not do what the ecosystem's tools do.

This epic fixes the matching to follow standard gitignore/npm semantics, driven test-first so the intended behavior is documented before any code changes.

## Sub-issues

1. Add a failing test suite that codifies gitignore and npm ignore semantics for `isExcluded`
2. Rewrite `isExcluded` to satisfy gitignore and npm ignore semantics
3. Subtract workspace negation patterns in `publish --all` instead of dropping them

## Definition of done

- A table-driven test suite documents the expected exclude behavior, including trailing-slash directory patterns, root-anchored patterns, and negation.
- `isExcluded` passes that suite: trailing-slash and root-anchored patterns match correctly, and negation re-includes files.
- Default excludes still always win, symlinks are still skipped, and the `.git` safety filter is preserved.
- `lnpm publish --all` no longer publishes a package that a workspace negation pattern excludes.
- All new and existing tests pass with `go test ./...`.
