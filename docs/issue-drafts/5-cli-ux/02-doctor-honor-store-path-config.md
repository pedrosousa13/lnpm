TITLE: Make doctor honor the configured store_path when checking the store
LABELS: bug
---
## Severity

Medium — `doctor` can report a healthy store while inspecting the wrong directory entirely, hiding real problems and creating false alarms.

## Background

lnpm keeps published packages in a content-addressed store, normally at `~/.lnpm`. The store location can be overridden via the `LNPM_STORE` environment variable or the `store_path` key in `~/.lnpm/config.yaml`. `internal/config.GetStorePath()` is the single source of truth for resolving this path and is used by publish, add, and the database layer.

## Problem

`internal/cli/doctor.go` does not call `config.GetStorePath()`. It has its own local `getStorePath()` helper that only checks the `LNPM_STORE` env var and otherwise falls back to `~/.lnpm`, ignoring `store_path` from the config file entirely.

Scenario:

```
lnpm config store_path /data/lnpm
lnpm publish          # writes to /data/lnpm, via config.GetStorePath()
lnpm doctor           # checks ~/.lnpm instead
```

`doctor` prints `Store directory does not exist: ~/.lnpm` and reports an issue, even though the real store at `/data/lnpm` is present and healthy. Conversely, if `~/.lnpm` happens to exist from a previous install, `doctor` can report "OK" while never looking at the store the rest of the tool is actually using.

## Where to look

- `internal/cli/doctor.go:20` — `storePath := getStorePath()`, the call that needs to use the config-aware resolver.
- `internal/cli/doctor.go:134-141` — the local `getStorePath()` helper that duplicates and diverges from `config.GetStorePath()`.
- `internal/config/config.go:138-156` — `GetStorePath()`, which correctly resolves `LNPM_STORE` env var, then `store_path` config, then `~/.lnpm`.

## How to fix

1. Delete the local `getStorePath()` function in `internal/cli/doctor.go:134-141`.
2. In `RunDoctor`, replace `storePath := getStorePath()` with `storePath, err := config.GetStorePath()`, handling the error (print it and increment `issues`, matching the style already used for the other checks in this function).
3. Add the `github.com/pedrosousa13/lnpm/internal/config` import to `internal/cli/doctor.go`.

## Acceptance criteria

- [ ] `lnpm config store_path <dir>` followed by `lnpm doctor` inspects `<dir>`, not `~/.lnpm`.
- [ ] `doctor` behavior when `LNPM_STORE` is set is unchanged (env var still wins).
- [ ] `internal/cli/doctor.go` no longer defines its own store path resolution.

## Testing

Add a test (e.g. `internal/cli/doctor_test.go`) that sets `LNPM_CONFIG` to a temp config file with a custom `store_path`, runs `RunDoctor()`, and asserts it checks that directory rather than `~/.lnpm` (for example, by creating the configured directory and confirming no "NOT FOUND" issue is reported). Follow the env-var setup pattern used in `internal/config/config_test.go`'s `TestGetStorePathHonorsConfig`.

```
go test ./internal/cli/... -run TestDoctor
go test ./...
```
