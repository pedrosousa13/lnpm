TITLE: Make doctor exit non-zero on failure and respect NO_COLOR
LABELS: bug, ux
---
## Severity

Low — breaks scripting against `doctor` and leaks decorative glyphs into piped/non-color output, but does not lose data.

## Background

`lnpm doctor` runs a series of health checks against the store and database and prints a summary of issues and warnings found. `internal/cli/output.go` provides `iconOK()`/`iconFail()`/`iconWarn()` helpers that render `✓`/`✗`/`⚠` on a real terminal and plain ASCII (`OK`/`x`/`!`) when output is piped or `NO_COLOR` is set, so scripted output stays clean. `internal/cli/remove.go` already uses these helpers.

## Problem

Two related issues in `RunDoctor`:

1. `RunDoctor` always `return nil`, even when it found issues. A caller scripting `lnpm doctor && deploy.sh` has no way to detect that doctor reported problems — the command always looks like it succeeded.
2. `doctor.go` and `retreat.go` print the raw glyphs `✓`, `✗`, `⚠` directly with `fmt.Println`/`fmt.Printf` instead of calling `iconOK()`, `iconFail()`, `iconWarn()`. This means `NO_COLOR=1 lnpm doctor` or `lnpm doctor | cat` still emits Unicode glyphs, unlike the rest of the CLI.

## Where to look

- `internal/cli/doctor.go:130` — `return nil` at the end of `RunDoctor`, unconditional regardless of `issues`.
- `internal/cli/doctor.go:23,28,35,40,65,69,90,94,109,113,120,123,126` — raw `"✓ OK"`, `"✗ NOT FOUND"`, `"⚠ %d orphaned..."` etc. instead of `iconOK()`/`iconFail()`/`iconWarn()`.
- `internal/cli/retreat.go:95,97,101,103,119,121,128,130,137,139,153,158,161` — same pattern of raw `✓`/`⚠` glyphs in `RunRetreat`.
- `internal/cli/output.go:44-47` — the existing `iconOK()`, `iconFail()`, `iconWarn()` helpers to reuse.
- `internal/cli/remove.go:125` — example of the correct pattern already in use: `fmt.Printf("%s Install failed: %v\n", iconWarn(), err)`.

## How to fix

1. In `RunDoctor`, after the summary block, return a distinct error when `issues > 0`, e.g. `return fmt.Errorf("doctor found %d issue(s)", issues)`. Keep returning `nil` when `issues == 0` (warnings alone should not fail the command, matching the existing summary logic that separates issues from warnings).
2. Replace every raw `"✓ ..."`, `"✗ ..."`, `"⚠ ..."` string literal in `doctor.go` with the corresponding `iconOK()`, `iconFail()`, `iconWarn()` call, following the pattern at `remove.go:125`.
3. Do the same for the raw glyphs in `retreat.go`.
4. Check `internal/cli/commands.go` where `doctorCmd` is defined (around `doctorCmd.RunE`) — cobra will already print a non-nil error and set a non-zero exit code, so no extra wiring should be needed there.

## Acceptance criteria

- [ ] `lnpm doctor; echo $?` prints `1` (or any non-zero code) when issues were found, `0` when clean.
- [ ] `NO_COLOR=1 lnpm doctor` and `lnpm doctor | cat` print `OK`/`x`/`!` instead of `✓`/`✗`/`⚠`.
- [ ] `NO_COLOR=1 lnpm retreat --force` and piped `lnpm retreat --force` print plain ASCII instead of `✓`/`⚠`.
- [ ] Interactive terminal output is unchanged (still shows the Unicode glyphs).

## Testing

Add a test in `internal/cli/doctor_test.go` asserting `RunDoctor()` returns a non-nil error when a check fails (e.g. point it at a missing store directory via a temp `LNPM_CONFIG`) and nil when everything passes. Add/extend a test in `internal/cli/output_test.go` or a new `retreat_test.go` under `internal/cli/` asserting no raw `✓`/`✗`/`⚠` bytes appear in captured stdout when `NO_COLOR=1` is set.

```
go test ./internal/cli/...
go test ./...
```
