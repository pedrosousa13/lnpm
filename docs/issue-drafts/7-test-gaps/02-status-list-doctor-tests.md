TITLE: Add tests for the status, list, and doctor commands
LABELS: tests
---
## Severity

Medium. Three user-facing read-only commands are never invoked by any test, so a panic or wrong output in `lnpm status`, `lnpm list`, or `lnpm doctor` would ship completely unnoticed.

## Background

`lnpm status` (`RunStatus`) prints three sections: all published packages in the store (name/version/hash/age), all active links grouped by project, and — if the current directory has an `lnpm.lock` — the current project's linked packages. `lnpm list` (`RunList`) has three modes selected by its arguments: `--store` lists every package in the store, a package name plus `--projects` lists the projects using that package, and no flags lists the linked packages in the current project from its lockfile. `lnpm doctor` (`RunDoctor`) runs five health checks (store dir exists/writable, database opens, orphaned packages, orphaned links, missing store files) and prints a summary; it always returns nil.

The integration suite in `tests/` calls the exported `cli.RunX` functions in-process inside an isolated environment: `setupTest` points `LNPM_STORE` at a temp dir and resets the database singleton, and helpers like `publishAndAdd` publish a package and link it into a fresh project. None of those ~90 tests ever call `RunStatus`, `RunList`, or `RunDoctor`.

## Problem

Zero coverage of `RunStatus`, `RunList`, and `RunDoctor` means:

- Any nil-pointer or formatting panic in these commands (e.g. against an empty store, a package with no links, or a missing lockfile) ships silently.
- `RunList`'s error paths — nonexistent package with `--projects`, no lockfile in cwd — are unverified.
- The pure display helpers `truncate` (rune-safe string truncation) and `formatTimeAgo` (humanized "X ago" formatting with singular/plural and a date fallback past 7 days) have subtle branch logic and no tests. `truncate` in particular has rune-slicing edge cases (multibyte characters, `maxLen <= 3`).

## Where to look

Untested code:

- `internal/cli/status.go:14` — `RunStatus`.
- `internal/cli/status.go:109` — `RunList(showStore bool, packageName string, showProjects bool)`.
- `internal/cli/status.go:192` — `truncate`.
- `internal/cli/status.go:204` — `formatTimeAgo`.
- `internal/cli/doctor.go:12` — `RunDoctor`.
- `internal/cli/doctor.go:134` — `getStorePath` (honors `LNPM_STORE`, so the isolated test store is used automatically).

Existing tests to mirror:

- `tests/helpers_test.go:27` — `setupTest`, the isolated-environment constructor every integration test starts with.
- `tests/helpers_test.go:643` — `publishAndAdd`, the standard publish-then-link setup helper (see also `simplePkg` at line 616 and `chdir` at line 594).
- `tests/gc_test.go` / `tests/lifecycle_test.go` — integration tests exercising other `cli.RunX` commands; match their shape.
- `internal/cli/update_test.go:10` — example of a plain unit test inside `package cli`, where the unexported helpers are reachable.

## How to fix

1. Create `tests/status_test.go` (same `package tests`) with integration tests that follow the `setupTest` pattern:
   - `TestStatusEmptyStore`: `env := setupTest(t)`, then `cli.RunStatus()` returns nil against a completely empty store (this exercises the "(none)" branches).
   - `TestStatusWithPackagesAndLinks`: `env.publishAndAdd("status-pkg")` (cwd is left inside the project, so the "Current Project" lockfile section also runs), then `cli.RunStatus()` returns nil.
   - `TestListStore`: after `publishAndAdd`, `cli.RunList(true, "", false)` returns nil; also call it on an empty store.
   - `TestListProjectsForPackage`: `cli.RunList(false, "status-pkg", true)` returns nil after a link exists; `cli.RunList(false, "no-such-pkg", true)` returns an error containing "not found".
   - `TestListCurrentProject`: from inside the linked project, `cli.RunList(false, "", false)` returns nil; from a directory with no lockfile, assert the observed behavior (lockfile load of a missing file — pin whatever it does today: nil with "No linked packages" or an error).
   - Optionally capture stdout (swap `os.Stdout` with an `os.Pipe`) and assert the package name appears in the output; at minimum, assert the error results.
2. In the same file (or `tests/doctor_test.go`), add:
   - `TestDoctorHealthy`: after `publishAndAdd`, `cli.RunDoctor()` returns nil.
   - `TestDoctorEmptyStore`: `cli.RunDoctor()` on a fresh env returns nil (store dir exists because `setupTest` creates it as the DB home; if not, this pins the "NOT FOUND" branch not panicking).
3. Create `internal/cli/status_helpers_test.go` (`package cli`) with unit tests for the unexported helpers:
   - `truncate`: table with short string (unchanged), exact length, longer string (ends in `...`, total length == maxLen), `maxLen <= 3` (hard cut, no ellipsis), and a multibyte string (e.g. `"héllo wörld"`) asserting no invalid UTF-8 is produced.
   - `formatTimeAgo`: table using `time.Now().Add(-d)` for: 30s ("just now"), 1 minute exactly ("1 minute ago"), 5 minutes, 1 hour, 3 hours, 1 day, 3 days, and 10 days (falls back to the `Jan 2, 2006` date format — assert with `t.Format` rather than a hardcoded string).
4. No production code changes should be needed; these commands read everything through `db.GetDB()` and `LNPM_STORE`, which `setupTest` already isolates.

## Acceptance criteria

- [ ] `cli.RunStatus` is invoked by at least two integration tests (empty store, populated store with an active link and a current-project lockfile).
- [ ] `cli.RunList` is invoked in all three modes (store, package+projects, current project), including the not-found error path.
- [ ] `cli.RunDoctor` is invoked by at least one integration test and returns nil.
- [ ] `truncate` and `formatTimeAgo` have table-driven unit tests covering every branch (including the >7 days date fallback and rune-safety).
- [ ] `go test ./internal/cli/ -cover` shows `status.go` and `doctor.go` no longer at 0%.
- [ ] All tests pass with `-race` and on Windows.

## Testing

```
go test ./tests/ -run 'TestStatus|TestList|TestDoctor' -v
go test ./internal/cli/ -run 'TestTruncate|TestFormatTimeAgo' -v
go test ./internal/cli/ -cover
go test -race ./tests/ ./internal/cli/
```
