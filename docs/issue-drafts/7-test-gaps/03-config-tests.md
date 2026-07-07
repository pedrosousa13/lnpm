TITLE: Add tests for config subcommands and config persistence
LABELS: tests
---
## Severity

Medium. `lnpm config` is how users change the store location and link mode; the write half of persistence (`SaveConfig`) is never verified, so a serialization or path regression would corrupt or misplace user config without any test noticing.

## Background

lnpm reads its configuration from a YAML file — `~/.lnpm/config.yaml` by default, overridable with the `LNPM_CONFIG` env var (`getConfigPath`, `internal/config/config.go:93`). The `Config` struct holds `store_path`, `link_mode` (`hardlink`/`copy`), `manage_gitignore`, and hook commands. `LoadConfig` reads it once per process through a `sync.Once`; `SaveConfig` marshals the struct back to YAML and writes it.

The `lnpm config` command dispatches to four unexported helpers in `internal/cli/config.go`: `showConfig` (dump all), `getConfigKey` (print one key), `setConfigKey` (validate, mutate, save), and `editConfig` (create-if-missing, then launch `$EDITOR`). `setConfigKey` validates `link_mode` values and parses `manage_gitignore` as a bool, and rejects unknown keys.

Today `internal/config/config_test.go` only tests the read side (`GetStorePath` with env/config precedence). Nothing tests `SaveConfig`, `GetConfigPath`, `GetPackageStorePath`, or any of the four `config` subcommand helpers.

## Problem

- `SaveConfig` (`internal/config/config.go:76`) is 0% covered: nothing verifies that what it writes can be read back, that it creates the parent directory, or that omitted fields stay omitted (`omitempty`). A YAML-tag typo or marshal regression would silently destroy user settings on the next `lnpm config <key> <value>`.
- `showConfig`, `getConfigKey`, `setConfigKey`, and `editConfig` (`internal/cli/config.go:74-181`) are all 0% covered, including the user-facing validation errors ("link_mode must be 'hardlink' or 'copy'", "unknown config key", bool parsing for `manage_gitignore`).
- `GetConfigPath` (`internal/config/config.go:103`) and `GetPackageStorePath` (`internal/config/config.go:174`, which appends `store` to the store path) are untested.

## Where to look

Untested code:

- `internal/config/config.go:76` — `SaveConfig` (MkdirAll + yaml.Marshal + WriteFile).
- `internal/config/config.go:93` — `getConfigPath` (`LNPM_CONFIG` override) and `internal/config/config.go:103` — `GetConfigPath`.
- `internal/config/config.go:174` — `GetPackageStorePath`.
- `internal/config/config.go:44` — `LoadConfig` caches via `sync.Once` with no reset hook — a testability constraint, see step 1.
- `internal/cli/config.go:74` — `showConfig`; `internal/cli/config.go:87` — `getConfigKey`; `internal/cli/config.go:113` — `setConfigKey`; `internal/cli/config.go:147` — `editConfig`.

Existing tests to mirror:

- `internal/config/config_test.go:9` — `TestGetStorePathHonorsConfig`: shows the established pattern (temp dir, write YAML, `t.Setenv("LNPM_CONFIG", ...)`).
- `internal/cli/update_test.go:10` — example unit test in `package cli` where the unexported subcommand helpers are reachable.

## How to fix

1. **Mind the `sync.Once`.** `LoadConfig` caches the first load for the life of the test process and there is no reset hook. Avoid it in new tests: construct `&config.Config{...}` values directly, and verify writes by calling the unexported `loadConfigFile()` (reachable from `package config` tests) or by reading the file and `yaml.Unmarshal`-ing it yourself. Do not add tests that depend on `LoadConfig` observing a file written mid-test.
2. In `internal/config/config_test.go`, add:
   - `TestSaveConfigRoundTrip`: `t.Setenv("LNPM_CONFIG", filepath.Join(t.TempDir(), "nested", "config.yaml"))` (nested path proves MkdirAll works). Save a `Config` with `StorePath`, `LinkMode: "copy"`, `ManageGitignore` pointer set to false, and one hook set. Read it back via `loadConfigFile()` and assert every field survives. Also assert the raw file does not contain keys for unset fields (`omitempty` intact).
   - `TestGetConfigPath`: with `LNPM_CONFIG` set, `GetConfigPath()` returns exactly that value; with `t.Setenv("LNPM_CONFIG", "")` (an empty value falls through to the default, per `getConfigPath`), the result ends in `.lnpm/config.yaml` under the home directory.
   - `TestGetPackageStorePath`: `t.Setenv("LNPM_STORE", dir)`, assert the result is `filepath.Join(dir, "store")`.
3. Create `internal/cli/config_test.go` (`package cli`) for the subcommand helpers. For each test, `t.Setenv("LNPM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))` so `SaveConfig` writes into the sandbox:
   - `TestSetThenGetConfigKey`: `cfg := &config.Config{}`; `setConfigKey(cfg, "link_mode", "copy")` returns nil, the file now exists, and re-parsing it shows `link_mode: copy`. Then `getConfigKey(cfg, "link_mode")` returns nil (it prints; asserting the nil error is sufficient, capturing stdout optional). Repeat for `store_path`, `manage_gitignore` (value `"true"`), and one `hooks.*` key.
   - `TestSetConfigKeyValidation`: `setConfigKey(cfg, "link_mode", "banana")` returns an error mentioning `hardlink`/`copy`; `setConfigKey(cfg, "manage_gitignore", "not-a-bool")` returns the boolean error; `setConfigKey(cfg, "nonsense", "x")` and `getConfigKey(cfg, "nonsense")` both return "unknown config key" errors. Assert the config file was NOT written on the validation failures.
   - `TestShowConfig`: `showConfig(&config.Config{LinkMode: "hardlink"})` returns nil.
   - `editConfig`: only test the create-if-missing branch if cheap — e.g. on non-Windows, `t.Setenv("EDITOR", "true")` (the `/usr/bin/true` no-op) and assert the config file gets created; otherwise skip `editConfig` entirely (launching editors is out of scope).
4. No production changes are expected. If you find `setConfigKey` behaves differently than described, pin the actual behavior and note it.

## Acceptance criteria

- [ ] `SaveConfig` → load round-trip test passes, including nested-directory creation and `omitempty` behavior.
- [ ] `GetConfigPath` and `GetPackageStorePath` have unit tests.
- [ ] `setConfigKey`/`getConfigKey` are tested for every supported key plus all three validation errors (bad `link_mode`, bad bool, unknown key).
- [ ] Tests never rely on `LoadConfig` re-reading a file mid-process (the `sync.Once` caveat is respected, with a comment explaining it).
- [ ] `go test ./internal/config/ -cover` reports `SaveConfig` covered; `go test ./internal/cli/ -cover` shows `config.go` no longer at 0%.
- [ ] All tests pass with `-race` and on Windows (guard any `EDITOR=true` trick with a `runtime.GOOS` check).

## Testing

```
go test ./internal/config/ -cover -v
go test ./internal/cli/ -run TestConfig -v
go test ./internal/cli/ -run 'TestSetThenGetConfigKey|TestSetConfigKeyValidation|TestShowConfig' -v
go test -race ./internal/config/ ./internal/cli/
```
