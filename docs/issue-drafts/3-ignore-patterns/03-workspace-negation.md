TITLE: Subtract workspace negation patterns in publish --all instead of dropping them
LABELS: bug
---
## Severity

Medium. `lnpm publish --all` publishes a package that the workspace config explicitly excludes, so an internal-only package can be pushed into the shared store and linked into consuming projects against the maintainer's intent.

## Background

A monorepo lists its packages under a `workspaces` field in `package.json` (or `packages` in `pnpm-workspace.yaml`). These are glob patterns such as `packages/*`. `lnpm publish --all` detects the workspace and expands those globs to a list of package directories, then publishes each one. The expansion happens in `expandGlobs` in `internal/workspace/workspace.go`. npm and pnpm both support negation entries such as `!packages/internal-secret` to subtract a package from a broader glob.

## Problem

Given this workspace config:

```json
"workspaces": ["packages/*", "!packages/internal-secret"]
```

The developer expects `lnpm publish --all` to publish every package under `packages/` except `packages/internal-secret`. Instead, `expandGlobs` sees the `!packages/internal-secret` entry, hits `if strings.HasPrefix(pattern, "!") { continue }`, and drops it. The negation is never subtracted from the include set, so `packages/*` still matches `packages/internal-secret` and the excluded package is published into the shared store.

## Where to look

- `internal/workspace/workspace.go:157-190` — `expandGlobs`, which loops over patterns and collects matching package directories into `packages` (deduped via the `seen` map).
- `internal/workspace/workspace.go:162-165` — the `if strings.HasPrefix(pattern, "!") { continue }` that drops negation patterns instead of subtracting them.
- `internal/workspace/workspace.go:168` — `doublestar.Glob(os.DirFS(root), pattern)` performs the glob expansion; reuse it for negation patterns too.
- `internal/workspace/workspace.go:173-186` — where matched directories are validated (must contain `package.json`) and appended.
- `tests/workspace_test.go:63-80` — `TestDetectNPMWorkspace`, which detects the `npm-workspace` fixture and asserts the package count. This is the closest existing test to model a new one on.
- `tests/fixtures/npm-workspace/` — an existing fixture: root `package.json` with `"workspaces": ["packages/*"]` and `packages/package-a`, `packages/package-b`. Use it as the template for a new negation fixture.

## How to fix

1. In `expandGlobs`, collect negated patterns separately instead of dropping them. When a pattern starts with `!`, strip the `!` and add the remainder to a `negations []string` slice; continue to the next pattern.
2. After the main loop builds the `packages` include list, expand each negation pattern with `doublestar.Glob(os.DirFS(root), negPattern)` the same way includes are expanded, and build a set of absolute directory paths to exclude (`filepath.Join(root, match)`).
3. Filter the `packages` slice, removing any entry present in the exclusion set. Preserve ordering and dedup behavior.
4. Keep the existing `package.json`-presence check for included directories unchanged.

## Acceptance criteria

- [ ] A workspace config of `["packages/*", "!packages/internal-secret"]` excludes `packages/internal-secret` from the expanded package list.
- [ ] Non-negated globs behave exactly as before (existing workspace tests still pass).
- [ ] Negation works for both `package.json` `workspaces` and `pnpm-workspace.yaml` `packages`, since both route through `expandGlobs`.

## Testing

`internal/workspace` currently has no test file, and `expandGlobs` is unexported, so add a white-box unit test at `internal/workspace/workspace_test.go` (package `workspace`) that calls `expandGlobs(root, []string{"packages/*", "!packages/package-b"})` against a temp directory containing `packages/package-a/package.json` and `packages/package-b/package.json`, and asserts only `package-a` is returned.

Also add an end-to-end style case in `tests/workspace_test.go`: create a new fixture `tests/fixtures/npm-workspace-negation/` (copy the `npm-workspace` fixture, set `"workspaces": ["packages/*", "!packages/package-b"]`), then assert `workspace.Detect(...).ListPackages()` returns only `package-a`. Model it on `TestDetectNPMWorkspace` at `tests/workspace_test.go:63-80`.

Run:

```
go test ./internal/workspace/ ./tests/
```
