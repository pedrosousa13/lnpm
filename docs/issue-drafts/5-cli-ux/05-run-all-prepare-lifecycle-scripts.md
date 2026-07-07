TITLE: Run all applicable publish lifecycle scripts, not just the first
LABELS: bug
---
## Severity

Medium — can cause stale build output to be published to the store and every consumer project.

## Background

Before publishing a package, lnpm runs the package's npm lifecycle scripts via `hooks.RunPrepare`, so that a build step (`prepack`, `prepare`, etc.) runs before the package's files are copied into the content-addressed store. npm itself runs every applicable script from this family, in a fixed order, not just one.

## Problem

`RunPrepare` iterates over `[]string{"prepare", "prepublishOnly", "prepack"}`, and as soon as it finds one script present in `package.json`, it runs that single script and returns — it never checks the remaining names. npm runs all of the applicable scripts in order: `prepublishOnly`, then `prepare`, then `prepack`.

Scenario: a package's `package.json` has both:

```json
"scripts": {
  "prepare": "husky install",
  "prepack": "npm run build"
}
```

`RunPrepare` finds `"prepare"` first (it is first in the slice), runs `husky install`, and returns — `npm run build` (`prepack`) never runs. `lnpm publish` then copies the stale (or entirely absent) `dist/` output into the store, and every project consuming the package via `lnpm add` receives the stale build.

## Where to look

- `internal/hooks/hooks.go:40-54` — the loop in `RunPrepare` that breaks out after the first match: `scripts := []string{"prepare", "prepublishOnly", "prepack"}` at line 40, `return nil` at line 52 inside the loop body.

## How to fix

1. Change the loop in `RunPrepare` to run every script present in `pkgJSON.Scripts` from the set `{prepublishOnly, prepare, prepack}`, in that order (npm's order for publish), instead of stopping at the first match.
2. Reorder the `scripts` slice to `[]string{"prepublishOnly", "prepare", "prepack"}` to match npm's actual execution order.
3. Remove the early `return nil` inside the loop; only return early on error (propagate the first failing script's error immediately, matching current error-wrapping behavior with `%s script failed: %w`).
4. After the loop completes without error, keep the final `return nil`.
5. Keep `debug.Log("hooks: no prepare scripts found")` behavior for the case where none of the three scripts exist.

## Acceptance criteria

- [ ] A package with both `prepare` and `prepack` scripts runs both, in the order `prepublishOnly` → `prepare` → `prepack`, when publishing.
- [ ] A package with only one of the three scripts still runs exactly that one, as before.
- [ ] A package with none of the three scripts is unaffected (no scripts run, no error).
- [ ] If an earlier script in the sequence fails, later scripts do not run and the error is surfaced.

## Testing

Extend `internal/hooks/hooks_test.go`'s `TestRunPrepare` (currently at line 11) with a case where `package.json` defines both `prepare` and `prepack` scripts (e.g. each appending a marker to a file) and assert both markers are present afterward, in the right order.

```
go test ./internal/hooks/...
go test ./...
```
