TITLE: Abort destructive operations in non-interactive mode instead of auto-confirming
LABELS: bug, ux
---
## Severity

Medium — silently deletes user data (store packages, project links) when output is redirected or piped.

## Background

lnpm keeps published packages in a content-addressed store under `~/.lnpm`. Two commands permanently delete data: `lnpm gc` removes store packages that have no links, and `lnpm remove --all` unlinks every package from the current project. Before deleting, both call a shared `confirm()` helper that is supposed to ask the user "are you sure?". The tool decides whether to show decorative output and prompts based on whether it is attached to a terminal (a TTY).

## Problem

`confirm()` returns `true` (proceed) without asking whenever stdin or stdout is not a terminal. That means any non-interactive invocation deletes immediately. For example:

```
lnpm gc --older-than 30d | tee gc.log
```

Because stdout is a pipe, `confirm()` never prompts and every matching package is deleted from the store. The same happens in CI, under `nohup`, or when a user simply redirects output to a file. There is no `--yes` flag to opt into deletion, so there is currently no safe non-interactive path.

## Where to look

- `internal/cli/output.go:73-76` — `confirm()` returns `true` immediately when `!isTTY(os.Stdin) || !isTTY(os.Stdout)`.
- `internal/cli/gc.go:124` — `if !confirm("Permanently delete these package(s) from the store?")` guards the store deletion at `gc.go:128-141`.
- `internal/cli/remove.go:44` — `if !confirm(...)` guards `remove --all`.
- `internal/cli/commands.go:150-166` — `gcCmd` definition and its flag registration around `commands.go:239` (`gcCmd.Flags().Bool("dry-run", ...)`).
- `internal/cli/commands.go:64-90` — `removeCmd` definition; its `--all` flag is registered at `commands.go:229`.

## How to fix

1. Add a `yes bool` parameter to `confirm`, or add a separate helper, so callers can pass an explicit override. When not interactive: if `yes` is set, proceed; otherwise print a message like `Refusing to delete without confirmation; re-run with --yes` and return `false` (abort). When interactive, keep the current prompt behavior.
2. Add a `--yes`/`-y` boolean flag to `gcCmd` and `removeCmd` (register alongside the existing flags in `commands.go`), read it in the command's `RunE` the same way `dry-run`/`all` are read, and thread it into `RunGC` and `RunRemove`.
3. Pass the flag value through to the `confirm` calls at `gc.go:124` and `remove.go:44`.
4. Leave the interactive prompt path unchanged so existing terminal behavior is preserved.

## Acceptance criteria

- [ ] `lnpm gc | cat` and `lnpm remove --all | cat` abort with a clear message and delete nothing.
- [ ] `lnpm gc --yes | cat` and `lnpm remove --all --yes | cat` proceed with deletion.
- [ ] Running `lnpm gc` interactively still prompts and honors a `y`/`n` answer.
- [ ] `--dry-run` still reports without deleting regardless of `--yes`.

## Testing

Add a unit test for the confirm logic (for example `internal/cli/output_test.go`) that exercises the non-interactive branch with `yes` true and false. Because `confirm` reads `os.Stdin`/`os.Stdout` directly, prefer refactoring the decision into a small pure function (for example `func shouldProceed(interactive, yes bool) (proceed bool, msg string)`) and test that directly.

Manual verification:

```
go build -o /tmp/lnpm ./cmd/lnpm   # adjust to the real main package path
/tmp/lnpm gc | cat                 # expect: aborts, deletes nothing
/tmp/lnpm gc --yes | cat           # expect: proceeds
go test ./...
```
