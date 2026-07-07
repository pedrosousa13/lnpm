TITLE: Fix documentation describing behavior that doesn't match the code
LABELS: epic, docs

## Overview

Several of lnpm's docs (`README.md`, `MONOREPO.md`, `ROADMAP.md`, and the `examples/` guides) describe behavior that the current codebase either never implemented or has since removed. This ranges from small factual slips (a command description that's out of date) to whole workflows that don't work as written (staging files in git before running `lnpm push` has no effect, and running `lnpm push` from a monorepo root pushes the wrong package). There's also a design document, `ROADMAP.md`, that predates the current Go implementation and is still linked from the README as the live feature roadmap.

## Why this matters

A developer who follows these docs literally will hit workflows that silently do the wrong thing: staging changes in git before `lnpm push` (no effect — push always re-packs the current working tree), adding `lnpm` to `devDependencies` and running `npm install -D lnpm` (fails outright — lnpm is not published to npm), or running `lnpm push` from a monorepo root expecting it to update a workspace package (it pushes the root's own `package.json` instead). The README also overstates the file-filtering behavior as "identical to npm publish," which isn't true and could give a false sense of security around what gets copied into the shared store. Finally, `ROADMAP.md` is a stale pre-implementation design doc that lists dependencies and a database (SQLite) the project doesn't use, and shows every already-shipped command as an unchecked, pending task — actively misleading anyone using it to gauge project maturity.

## Sub-issues

1. Correct README claim that publish uses npm pack for file filtering
2. Remove git-staging change detection claims from docs
3. Fix MONOREPO.md push-location and npm-install-lnpm guidance
4. Rewrite ROADMAP.md to reflect actual implementation
5. Fix small documentation inaccuracies about status, add, push, and reflink history

## Definition of done

- `README.md`'s "File Filtering" section accurately describes the Go-native filtering in `internal/pack/pack.go`, with no claim of shelling out to `npm pack` or being "identical to npm publish."
- `MONOREPO.md` and the `examples/` guides no longer claim `lnpm push` reacts to git staging, and no longer show `lnpm push` being run from a monorepo root or `lnpm` being added to `devDependencies`.
- `ROADMAP.md` reflects the actual dependency list and shipped feature set (or is replaced with a short, accurate pointer to real planning artifacts), and the README's link text describing it is accurate.
- The small factual mismatches in `README.md`, `ARCHITECTURE.md`, and `CHANGELOG.md` (status command scope, add's install behavior, push's re-link behavior, and the misplaced changelog section) are corrected.
- None of the fixes touch behavior — this epic is documentation-only.
