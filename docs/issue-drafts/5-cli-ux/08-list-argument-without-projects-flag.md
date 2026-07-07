TITLE: Make `lnpm list <package>` honor its argument without --projects
LABELS: bug, good first issue
---
## Severity

Low — a typed argument is silently dropped, producing output the user did not ask for with no indication anything was ignored.

## Background

`lnpm list` shows linked packages in the current project. It also supports `lnpm list --store` (list everything in the store) and `lnpm list <package> --projects` (list which projects link a given package).

## Problem

`RunList` only uses the `packageName` positional argument when `showProjects` (the `--projects` flag) is also set. If a user runs `lnpm list my-package` without `--projects`, expecting to see information about `my-package`, the argument is silently ignored and lnpm instead lists every linked package in the current project — with no message that `my-package` was not recognized as a flag or used in any way.

## Where to look

- `internal/cli/status.go:134` — `if packageName != "" && showProjects {` — the only branch that reads `packageName`; the fallback branch at `internal/cli/status.go:161-188` lists the current project's packages regardless of whether `packageName` was supplied.
- `internal/cli/commands.go:128-145` — `listCmd` definition, where `packageName` is taken from `args[0]` and `showProjects`/`showStore` flags are read (flags registered at `commands.go:235-236`).

## How to fix

1. In `RunList` (`internal/cli/status.go`), when `packageName != "" && !showProjects`, do not fall through to listing all current-project packages. Either:
   - return an error such as `fmt.Errorf("use --projects to see which projects link %s (e.g. lnpm list %s --projects)", packageName, packageName)`, or
   - treat `packageName` as implying `--projects` (i.e. drop the `&& showProjects` requirement and let `packageName != ""` alone select that branch).
2. Prefer the explicit error unless the project's existing UX favors permissive flag inference elsewhere — either resolves the silent-drop bug; pick the one consistent with how `showStore` combined with a package name is handled today (currently `showStore` takes priority and ignores `packageName` too — decide whether that combination also deserves a similar guard while making this change, but only if it's a small, consistent addition).

## Acceptance criteria

- [ ] `lnpm list my-package` (no `--projects`) either shows the projects linking `my-package` or returns a clear error — it no longer silently lists unrelated current-project packages.
- [ ] `lnpm list my-package --projects` behavior is unchanged.
- [ ] `lnpm list` with no arguments still lists the current project's linked packages.
- [ ] `lnpm list --store` behavior is unchanged.

## Testing

Add a test in `internal/cli/status_test.go` (new file, following the naming convention of other `internal/cli/*_test.go` files) calling `RunList` with a non-empty `packageName` and `showProjects=false`, asserting it does not fall through to the current-project listing path.

```
go test ./internal/cli/...
go test ./...
```
