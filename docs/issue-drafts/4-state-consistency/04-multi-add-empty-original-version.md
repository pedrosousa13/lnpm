TITLE: Skip lock and database registration when a package.json update fails in multi-add
LABELS: bug
---
## Severity

Medium — a package.json update failure during multi-package add records a lock entry with an empty original version, which causes a later `remove` to delete the user's real dependency specifier.

## Background

When adding several packages at once, `lnpm` updates `package.json` for each successful package, then records each in `lnpm.lock` and the database. The original version specifier captured during the `package.json` update is stored in the lock's `OriginalVersion` field. On removal, an empty `OriginalVersion` is the signal to delete the dependency from `package.json` entirely rather than restore it.

## Problem

In the multi-package path, if updating `package.json` for one package fails, the loop prints a warning and continues (`continue`). The affected entry keeps its default empty `origVersion`. But the package is still written to the lock file and the database afterward, with `OriginalVersion` empty. Meanwhile `package.json` was never rewritten, so it still holds the user's real specifier such as `"pkg": "^1.2.3"`.

The result is an inconsistent state: `package.json` has the real spec, but the lock says `OriginalVersion` is empty. A later `lnpm remove pkg` sees the empty original version and takes the "remove the dependency entirely" branch, deleting the user's real `^1.2.3` instead of leaving it alone.

## Where to look

- `internal/cli/add.go:156-163` — the per-package `package.json` update loop; on error it warns and `continue`s, leaving `successful[i].origVersion == ""`
- `internal/cli/add.go:181-199` — the follow-up loop that writes every `successful` entry into the lock file via `lock.Add(...)` regardless of whether its `package.json` update failed
- `internal/cli/add.go:206` — `database.InsertLink(dbLink)` records the DB link for the same entries
- `internal/cli/remove.go:79-88` — on removal, an empty `OriginalVersion` triggers `removeFromPackageJSON`, deleting the dependency the user still depends on

## How to fix

When a package's `package.json` update fails in the multi-add loop, do not register that package in the lock file or database, and undo the symlink that was already created for it:

1. Track which packages failed the `package.json` update (for example, mark the `addResult` or collect their indexes).
2. For each failed package, call the linker's unlink to remove the `node_modules` symlink it created — mirror the unlink used elsewhere (`linker.Unlink(name)` as in `internal/cli/remove.go:72`).
3. Skip those packages in the lock/database loop at `internal/cli/add.go:181` so no lock entry or DB link is written for them.
4. Count each such package as a failure: append to `errors` so the final `len(errors) > 0` check at `internal/cli/add.go:235` reports it and the command exits non-zero.

This leaves `package.json` untouched for the failed package (its real specifier intact) and records nothing inconsistent.

## Acceptance criteria

- [ ] When a package's `package.json` update fails during multi-add, no lock entry and no database link are created for that package.
- [ ] The `node_modules` symlink created for that package is removed.
- [ ] The failed package is counted in the error summary and the command exits non-zero.
- [ ] Packages whose `package.json` update succeeded are unaffected and still linked and recorded.

## Testing

Add an integration test to `tests/add_test.go` using the `setupTest` helper (see `tests/helpers_test.go`).

To force a `package.json` update failure for exactly one package while another succeeds, note that `updatePackageJSON` fails when the file cannot be parsed as JSON — but `package.json` is shared, so instead make the whole project's `package.json` valid and drive the failure through file permissions is not per-package. A cleaner approach: since both packages share one `package.json`, test the invariant that a lock entry is never written with an empty `OriginalVersion` while `package.json` still holds a real specifier. If a per-package injection point is added during the fix, prefer that.

Test outline (invariant-focused):
1. `te := setupTest(t)`; publish `pkg-a` and `pkg-b` with `te.simplePkg`.
2. `projectDir := te.newProject("consumer")`; write a `package.json` depending on `pkg-a` at `^1.0.0` and `pkg-b` at `^2.0.0` via `te.writePackageJSON`.
3. Call `cli.RunAddMultiple([]string{"pkg-a", "pkg-b"}, false, false, false)`.
4. Assert both lock entries carry their real original versions via `te.AssertLockfile`: `pkg-a` → `^1.0.0`, `pkg-b` → `^2.0.0` — never empty while `package.json` holds a spec.
5. If the fix introduces a way to force one package's update to fail, add a test asserting that package has no lock entry (`l.Has("pkg-a") == false`), no DB link (`te.AssertDatabaseNoLink("pkg-a", projectDir)`), no symlink (`te.AssertSymlinkMissing(projectDir, "pkg-a")`), and that `RunAddMultiple` returned a non-nil error.

Run:

```
go test ./tests/ -run TestAdd -v
```
