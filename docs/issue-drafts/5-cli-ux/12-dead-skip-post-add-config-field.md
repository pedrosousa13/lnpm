TITLE: Wire up or remove the unused hooks.skip_post_add config field
LABELS: bug, good first issue
---
## Severity

Low — a documented-looking config field silently does nothing, misleading users who set it.

## Background

lnpm's config file supports a `hooks` section with settings like `skip_prepare`, which is read by `hooks.RunPrepare` to skip running `prepare`/`prepublishOnly`/`prepack` scripts before publish. There is a sibling field, `skip_post_add`, intended (per its comment) to skip the post-add hook that runs `npm install` (or the configured custom hook) after `lnpm add`.

## Problem

`HooksConfig.SkipPostAdd` is defined and exposed through `lnpm config --edit` (it round-trips through the YAML config file like any other field), but it is never read anywhere in the codebase. `hooks.RunPostAdd`, which decides whether to run the post-add install/hook, only checks its `runInstall` parameter and never consults `cfg.Hooks.SkipPostAdd`. A user who sets `hooks.skip_post_add: true` in `~/.lnpm/config.yaml`, expecting `lnpm add --install` to stop running the install step, sees no change in behavior.

## Where to look

- `internal/config/config.go:34` — `SkipPostAdd bool` field definition, with a comment describing intended behavior ("Skip post-add hook (npm install)").
- `internal/hooks/hooks.go:62-85` — `RunPostAdd`, which never reads `cfg.Hooks.SkipPostAdd`; compare to `RunPrepare` at `internal/hooks/hooks.go:17-23`, which does check `cfg.Hooks.SkipPrepare` (`if skipHooks || cfg.Hooks.SkipPrepare`).
- `internal/cli/add.go:227` and `internal/cli/add.go:385` — the two call sites of `hooks.RunPostAdd(cwd, true)`, both of which pass `runInstall` as the sole gate today.

## How to fix

Recommendation: wire it up rather than delete it — the field already has a clear, documented purpose and a natural implementation matching the existing `SkipPrepare` pattern, and it's exposed in the config file so removing it would be a breaking change for anyone who already set it.

1. In `RunPostAdd` (`internal/hooks/hooks.go:62`), add a check mirroring `RunPrepare`: after loading `cfg := config.Get()`, return early (`debug.Log("hooks: skipping post-add (disabled via config)"); return nil`) if `cfg.Hooks.SkipPostAdd` is true, before running either the custom hook or the default install command.
2. Leave the `runInstall` parameter check as the first gate (unchanged) — `skip_post_add` should suppress the hook even when `--install` was requested, since it is an explicit user override.

## Acceptance criteria

- [ ] Setting `hooks.skip_post_add: true` in the config file causes `lnpm add --install` to skip both the custom `post_add` hook and the default package-manager install.
- [ ] Leaving `skip_post_add` unset (default `false`) behaves exactly as before.
- [ ] `runInstall=false` still skips the hook regardless of `skip_post_add`, as it does today.

## Testing

Extend `internal/hooks/hooks_test.go`'s `TestRunPostAdd` (currently at line 125) with a case that sets `cfg.Hooks.SkipPostAdd = true` (via a temp `LNPM_CONFIG` file or however the existing test injects config) and `runInstall = true`, asserting no install command and no custom hook run.

```
go test ./internal/hooks/...
go test ./...
```
