TITLE: Fix nil-pointer crash in retreat when lnpm.lock is corrupt
LABELS: bug
---
## Severity

High — `lnpm retreat` crashes with a runtime panic instead of cleaning up, and a crash is the exact moment a user reaches for `retreat`.

## Background

`lnpm retreat` undoes everything `lnpm` did to a project: it removes the `node_modules` symlinks, restores original dependencies in `package.json`, deletes the `.lnpm/` directory, and deletes `lnpm.lock`. It starts by loading `lnpm.lock` to learn which packages were linked. The loader, `lockfile.Load`, returns an empty lock file when the lock is simply absent, but returns `(nil, error)` when the file exists but cannot be parsed as YAML (for example, it was truncated or hand-edited).

## Problem

`retreat` guards the load result with `if err != nil || len(lock.List()) == 0`. Because `||` short-circuits, `lock.List()` is not called when `err != nil`, so that line is safe. But the body of that `if` only returns early when the `.lnpm/` directory does **not** exist. If the lock is corrupt (`lock == nil`) **and** a `.lnpm/` directory is present, execution falls through the guard and later calls `lock.List()` on the nil pointer — once in the preview path and once in the `--force` path — causing a nil-pointer panic.

Reproduction: in a project that has a `.lnpm/` directory, replace `lnpm.lock` with unparseable content (for example, truncate it mid-line), then run `lnpm retreat`. It panics instead of reporting the problem or cleaning up.

## Where to look

- `internal/cli/retreat.go:24` — `lock, err := lockfile.Load(cwd)`
- `internal/cli/retreat.go:25` — `if err != nil || len(lock.List()) == 0 {` guard that does not stop execution when the lock is corrupt but `.lnpm/` exists
- `internal/cli/retreat.go:41` — `linkedPkgs := lock.List()` in the preview (non-force) path, panics when `lock` is nil
- `internal/cli/retreat.go:76` — `linkedPkgs := lock.List()` in the `--force` path, panics when `lock` is nil
- `pkg/lockfile/lockfile.go:33-51` — `Load` returns `(nil, error)` on a YAML parse failure (line 49-50) but an empty `*LockFile` when the file is missing (line 38-43)

## How to fix

Decide on one of two behaviors and apply it right after the load at `internal/cli/retreat.go:24`, then document the choice in a code comment:

1. **Abort with a clear error (recommended).** If `err != nil`, return `fmt.Errorf("lnpm.lock is corrupt: %w", err)` before touching anything. This is consistent with `RunRemove`, which returns `fmt.Errorf("failed to load lock file: %w", err)` at `internal/cli/remove.go:32-34`. The user can then inspect or delete the lock and re-run.
2. **Substitute an empty lock file.** If `err != nil`, set `lock = &lockfile.LockFile{Version: 1, Packages: map[string]lockfile.Package{}}` so the rest of `retreat` runs and still deletes `.lnpm/` and the corrupt lock. This restores nothing from the lock, but leaves the project clean.

Whichever you pick, ensure `lock` is never nil past the guard.

## Acceptance criteria

- [ ] Running `retreat` (preview and `--force`) against a project with a corrupt `lnpm.lock` and an existing `.lnpm/` directory does not panic.
- [ ] The chosen behavior (abort vs. empty lock) is implemented in both the preview and `--force` paths and documented with a code comment.
- [ ] Existing `retreat` behavior for a valid lock file and for a missing lock file is unchanged.

## Testing

Add an integration test to `tests/retreat_test.go` using the `setupTest` helper (see `tests/helpers_test.go`).

Test outline:
1. `te := setupTest(t)`.
2. Publish a package and add it to a project so a real `.lnpm/` directory and `lnpm.lock` exist — the `publishAndAdd` helper does this and returns `(pkgDir, projectDir)`.
3. `te.chdir(projectDir)`, then overwrite `lnpm.lock` with corrupt content: `te.writeFile(filepath.Join(projectDir, "lnpm.lock"), "version: 1\npackages: {not valid yaml")`.
4. Call `cli.RunRetreat(true, false)` and assert it does not panic. If you chose the abort behavior, assert it returns a non-nil error whose message mentions corruption; if you chose the empty-lock behavior, assert it returns `nil` and that `.lnpm/` no longer exists via `te.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)`.

Run:

```
go test ./tests/ -run TestRetreat -v
```
