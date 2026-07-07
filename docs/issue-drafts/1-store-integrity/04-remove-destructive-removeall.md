TITLE: Remove the destructive RemoveAll before rename that can bless a partial store entry as complete
LABELS: bug
---
## Severity

Low — narrow trigger window, but when hit it turns a complete store entry into a partial one that lnpm then treats as valid.

## Background

lnpm stores packages at `~/.lnpm/store/<package>/<hash>/`, where the directory name is a hash of the contents. To avoid leaving a half-written package behind if publishing is interrupted, `store.Store` writes files into a temporary directory first and then atomically renames it into the final `<hash>` location. Renaming a fully-built directory into place is the standard way to make a directory appear "all at once" — readers either see the old state or the complete new state, never a partial one.

Only one `lnpm` process can publish at a time: opening the bbolt database (`internal/db/db.go:116`, `bolt.Open(..., &bolt.Options{Timeout: 1 * time.Second})`) takes an exclusive OS file lock on the database, so a second concurrent `lnpm` process blocks and times out rather than publishing in parallel. That matters for the reasoning below.

## Problem

Just before the atomic rename, `store.Store` runs `os.RemoveAll(finalPath)` — deleting whatever already exists at the destination — and ignores its error. Then it renames the temp dir into place. If the destination already existed (a previous store of the same hash) and the `RemoveAll` only *partially* deletes it (interrupted, or a permission/ENOTEMPTY situation), the subsequent `os.Rename` fails with `ENOTEMPTY` because the destination still contains files. The error path then checks `if s.Exists(name, hash) { return finalPath, nil }` and returns *success* — but `finalPath` is now the partially-deleted directory. Since `Exists` is only a directory stat, the mangled entry is treated as a valid, complete package forever after.

Concrete failure scenario: hash `H` is already in the store. A re-publish of the same content reaches the finalize step, `RemoveAll(finalPath)` starts deleting the existing `H/` directory but is interrupted after removing some files, `Rename` then fails `ENOTEMPTY`, and the fallback returns `finalPath` as success. The store now holds a `H/` directory missing files. A future `lnpm add` links this truncated package into a consumer.

Because the bbolt lock already prevents two publishes from racing, there is no concurrency scenario that *needs* the destination to be cleared first — the pre-rename `RemoveAll` is pure downside.

## Where to look

- `internal/store/store.go:272-274` — `_ = os.RemoveAll(finalPath)` (error ignored) immediately followed by `os.Rename(destPath, finalPath)`.
- `internal/store/store.go:275-279` — the fallback: on rename failure, `if s.Exists(name, hash) { return finalPath, nil }` returns success pointing at the possibly-partial destination.
- `internal/store/store.go:62-67` — `Exists` is a bare `os.Stat`, so it cannot tell a complete entry from a partial one.
- `internal/store/store.go:80-83` — the earlier short-circuit already returns early when `finalPath` exists, so by the time execution reaches line 272 the destination is *not* expected to exist under normal flow.
- `internal/db/db.go:116` — the bbolt exclusive-lock open that serializes publishes across processes.

## How to fix

1. Delete the `os.RemoveAll(finalPath)` at `store.go:273`. Never destroy an existing final entry: the earlier `Exists` short-circuit at lines 80-83 already returns before this point when a complete entry exists, so a healthy re-publish never reaches the rename with a populated destination.
2. Keep the atomic `os.Rename(destPath, finalPath)`. If it fails, keep the existing fallback of returning the existing path *only* when `Exists` is true — but now that fallback is only reached when some other process/state created `finalPath` between the early check and the rename, which is the benign "someone else already stored this hash" case, not a self-inflicted partial delete.
3. Leave the temp-dir cleanup (`committed` flag + deferred `os.RemoveAll(destPath)`) as is; that removes only the *temp* dir on failure, which is correct.

## Acceptance criteria

- [ ] `store.Store` no longer calls `os.RemoveAll(finalPath)` anywhere on the success/finalize path.
- [ ] A re-publish of an already-stored hash still returns the existing path without error (via the early `Exists` short-circuit) and does not touch the existing directory.
- [ ] If `os.Rename` fails for a reason other than "destination already complete", `Store` returns an error rather than silently reporting success on a partial directory.
- [ ] `go test ./...` passes, including the existing atomicity tests.

## Testing

From the repo root:

```
go test ./internal/store/...
go test ./...
```

There is an existing `internal/store/atomic_test.go` — add the regression there. Suggested `TestStore_DoesNotDeleteExistingFinalOnRepublish`: store a package, capture the set of files in the final dir, store the same name+hash again, and assert the final directory's contents are unchanged and complete (and that the store returned no error). The existing `TestStore_ReadOnlyDestination` in `internal/store/store_permissions_test.go` already asserts the early no-op path; keep it passing.
