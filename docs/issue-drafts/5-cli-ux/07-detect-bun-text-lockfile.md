TITLE: Detect Bun's text lockfile bun.lock
LABELS: bug, good first issue
---
## Severity

Low — misdetects the package manager for current Bun projects, causing lnpm to run the wrong install command.

## Background

lnpm detects which package manager a consumer project uses (npm, yarn, pnpm, or bun) by checking for lock files, so it can run the right install command after linking a package (for example after `lnpm retreat --install` or a configured post-add hook).

## Problem

`DetectPackageManager` only checks for `bun.lockb`, Bun's old binary lockfile format. Since Bun 1.2, `bun install` defaults to a text-based `bun.lock` file instead. A project using a current Bun version has neither `bun.lockb` nor any of the other checked lock files (it has `bun.lock`), so `DetectPackageManager` falls through every check and defaults to `NPM`. lnpm then runs `npm install --legacy-peer-deps` in a Bun project instead of `bun install`.

## Where to look

- `internal/config/config.go:185-193` — the `lockFiles` slice in `DetectPackageManager`, which lists `{"bun.lockb", Bun}` but not `bun.lock`.

## How to fix

1. Add an entry for `{"bun.lock", Bun}` to the `lockFiles` slice, alongside the existing `{"bun.lockb", Bun}` entry (order between the two doesn't matter since both map to `Bun`).

## Acceptance criteria

- [ ] A project directory containing only `bun.lock` (no `bun.lockb`) is detected as `Bun`.
- [ ] A project directory containing `bun.lockb` is still detected as `Bun` (existing behavior unchanged).
- [ ] Detection for npm/yarn/pnpm lock files is unaffected.

## Testing

Add a test case to `internal/config/config_test.go` for `DetectPackageManager` that creates a temp directory with only a `bun.lock` file and asserts the result is `config.Bun`.

```
go test ./internal/config/...
go test ./...
```
