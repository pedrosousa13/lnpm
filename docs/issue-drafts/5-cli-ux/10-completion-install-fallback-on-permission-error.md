TITLE: Fall back to a user completion directory when the system directory is not writable
LABELS: bug, ux
---
## Severity

Low — `lnpm completion install` fails for a common case (unprivileged user, system completion dir already exists) instead of falling back.

## Background

`lnpm completion install` auto-detects the user's shell and writes a completion script to the appropriate directory. For bash on Linux/macOS, it prefers a system-wide directory (`/etc/bash_completion.d`, or the Homebrew prefix on macOS) so completions work for all shells system-wide, and only falls back to a per-user directory (`~/.bash_completion.d`) when the system directory doesn't exist.

## Problem

`installBashCompletion` selects the system directory whenever `os.Stat(compDir)` succeeds — i.e., whenever the directory *exists* — regardless of whether the current user can actually write to it. On most Linux systems and Homebrew installs, `/etc/bash_completion.d` (or the Homebrew equivalent) exists but is only writable by root/the Homebrew owner. For an unprivileged user, `os.Create(compFile)` at that path fails with a permission error (`EACCES`), and the whole command errors out — even though the fallback path (`~/.bash_completion.d`) would have worked fine. The fallback logic only triggers on "directory absent," never on "directory present but not writable."

## Where to look

- `internal/cli/completion.go:110-142` — `installBashCompletion`: `compDir` is chosen based purely on `os.Stat(compDir)` success (macOS branch at `completion.go:117-131`, Linux branch at `completion.go:133-138`), and `os.MkdirAll(compDir, 0755)` at `completion.go:140` plus `os.Create(compFile)` at `completion.go:145` are where the permission failure actually surfaces.

## How to fix

1. After selecting the system `compDir` (and before writing), check writability rather than mere existence — for example, attempt `os.MkdirAll(compDir, 0755)` and creating the file, and on a permission-denied error, fall back to `compDir = filepath.Join(home, ".bash_completion.d")` and retry, instead of returning the error immediately.
2. Keep the current "directory doesn't exist → fall back" behavior; add "directory exists but write fails with permission error → fall back" as an additional condition, so both cases route to the same fallback path.
3. Preserve the existing "Add this to your ~/.bashrc" hint (`completion.go:157-162`), which is already conditioned on the completion having landed under the home directory.

## Acceptance criteria

- [ ] Running `lnpm completion install` as an unprivileged user when `/etc/bash_completion.d` exists but is not writable succeeds by writing to `~/.bash_completion.d` instead of failing.
- [ ] Running as a user who *can* write to the system directory still writes there (no unnecessary fallback).
- [ ] The "add this to your ~/.bashrc" hint still only appears when the completion actually landed in the home directory.

## Testing

Add a test in a new `internal/cli/completion_test.go` that points the "system" directory at a temp directory created with read-only permissions (e.g. `os.Chmod(dir, 0555)`, skip on Windows/CI where this isn't meaningful) and asserts the completion file ends up under a fallback home-relative path instead of returning an error. This likely requires extracting the directory-selection/write logic from `installBashCompletion` into a small testable helper that accepts the candidate and fallback directories as parameters.

```
go test ./internal/cli/...
go test ./...
```
