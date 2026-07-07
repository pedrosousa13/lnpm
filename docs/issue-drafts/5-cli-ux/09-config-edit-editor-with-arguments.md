TITLE: Support editors with arguments in `config --edit`
LABELS: bug, good first issue
---
## Severity

Low — `lnpm config --edit` fails outright for a common `$EDITOR` configuration.

## Background

`lnpm config --edit` opens the lnpm config file in the user's preferred editor, read from the `$EDITOR` (or `$VISUAL`) environment variable, falling back to `vi`/`notepad`.

## Problem

Many editors are configured with extra arguments, most commonly `EDITOR="code -w"` (VS Code, wait for the file to close) or `EDITOR="subl -w"`. `editConfig` passes the entire `$EDITOR` value as a single executable name to `exec.Command(editor, configPath)`. `exec.Command`'s first argument must be a single executable, not a shell command line, so `exec.Command("code -w", configPath)` fails to find an executable literally named `"code -w"` and `lnpm config --edit` errors with `failed to launch editor "code -w": ...`.

## Where to look

- `internal/cli/config.go:159-169` — where `editor` is read from `$EDITOR`/`$VISUAL`/platform default.
- `internal/cli/config.go:173` — `cmd := exec.Command(editor, configPath)`, which breaks when `editor` contains spaces/arguments.
- `internal/shellcmd` — existing helper package (already imported by `internal/cli/retreat.go:12` and used in `internal/hooks/hooks.go:130`) that runs a command string via `sh -c`, correctly handling arguments.

## How to fix

1. Build the command string as `editor + " " + shellQuote(configPath)` (or equivalent) and run it via `shellcmd.Command(...)` instead of `exec.Command(editor, configPath)`, the same pattern used in `internal/hooks/hooks.go`'s `runScript`. This lets `sh -c` split `$EDITOR` into its arguments correctly.
2. Ensure `configPath` is quoted/escaped when interpolated into the shell command string, since config paths can contain spaces (home directories on some systems do).
3. Keep `cmd.Stdin = os.Stdin`, `cmd.Stdout = os.Stdout`, `cmd.Stderr = os.Stderr` wiring as-is so the editor still gets a real terminal.

## Acceptance criteria

- [ ] `EDITOR="code -w" lnpm config --edit` launches `code` with `-w <configPath>` instead of failing.
- [ ] `EDITOR=vi lnpm config --edit` (single-word editor) still works as before.
- [ ] A config path containing a space is handled correctly.

## Testing

Add a test in `internal/cli/config_test.go` (new file, if one doesn't already exist) that sets `EDITOR` to a short shell script or a command with a benign flag (e.g. a fake "editor" script under `t.TempDir()` that just writes its `os.Args` to a file) and asserts the arguments were split and passed correctly — this avoids invoking a real interactive editor in CI.

```
go test ./internal/cli/...
go test ./...
```
