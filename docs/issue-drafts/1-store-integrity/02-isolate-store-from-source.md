TITLE: Stop hardlinking source files into the store: isolate stored content from the source tree and consumers
LABELS: bug
---
## Severity

High — the store can be silently corrupted by a routine rebuild, and the corruption propagates into every linked consumer project and persists across future publishes.

## Background

lnpm maintains a content-addressed store at `~/.lnpm/store`. When you `lnpm publish`, it computes a hash over the package's file contents and permission bits, then stores the files at `~/.lnpm/store/<package>/<hash>/`. The directory name *is* a promise: the bytes inside are supposed to hash to `<hash>`. When you `lnpm add <pkg>` in a consumer, lnpm populates `<project>/.lnpm/<pkg>` from that store entry and symlinks `node_modules/<pkg>` at it.

To be fast, lnpm links files instead of copying where it can. A **hardlink** (created by `os.Link`) makes a second directory entry point at the *same underlying file* (the same *inode* — the on-disk object holding a file's bytes). Two hardlinked paths are literally one file: writing through either path changes what the other sees. A **reflink** (copy-on-write clone) is different — it starts sharing data blocks but is logically independent, so writing to one side transparently allocates new blocks and leaves the other untouched.

## Problem

Store population hardlinks the **developer's source files** directly into the store: `os.Link(f.Path, destFile)`, where `f.Path` is the original file in the package's working directory. Then project linking hardlinks the store entry into the consumer: `os.Link(srcPath, dstPath)` from the store into `.lnpm/<pkg>`. The result is that the source file, the store entry for hash H, and every consumer's copy are all the *same inode*.

That breaks the content-addressing promise the moment the source changes in place. Build tools that rewrite files in place — `tsc`, `webpack`, bundlers, and editors that do not save-via-rename — mutate the shared inode. So:

1. Developer publishes package `foo`; store entry `foo/<H>` is hardlinked to their `dist/index.js`.
2. Developer edits and rebuilds; `tsc` overwrites `dist/index.js` in place.
3. The store entry `foo/<H>` now contains the *new* bytes — it no longer hashes to `H`. Every consumer that hardlinked `foo/<H>` also just changed, mid-build, with no lnpm command run.
4. Later the developer re-runs `lnpm publish` on the original content, producing hash `H` again. `Store` checks `s.Exists(name, hash)` — which only stats the directory — sees `foo/<H>` exists, and short-circuits, returning the *corrupted* directory. Consumers get linked to content that does not match `H`. The corruption is now persistent.

This is silent: no error, no warning. On macOS it happens on every publish today because the reflink path is broken and everything falls through to hardlink (see the related reflink issue).

## Where to look

- `internal/store/store.go:156-160` — store population hardlinks the *source* file into the store: `if err := os.Link(f.Path, destFile); err == nil { ... }` where `f.Path` is the source path.
- `internal/store/store.go:104-111` — `useHardLink := s.canUseHardLink(sourceDir)` decides whether hardlinking source→store is attempted.
- `internal/store/store.go:80-83` — the `s.Exists(name, hash)` short-circuit that returns an already-existing (possibly mutated) store dir without re-verifying content.
- `internal/store/store.go:62-67` — `Exists` is only an `os.Stat` on the directory; it does not check contents against the hash.
- `internal/link/link.go:107-119` — project linking hardlinks the store entry into the consumer (`os.Link(srcPath, dstPath)`), which is acceptable *if* the store is isolated from source (see fix).
- `internal/pack/pack.go:428` — the content hash includes `f.Mode.Perm()` and file contents, so any in-place mutation of a hardlinked store entry invalidates the hash.

## How to fix

1. In `store.Store`, **never hardlink the source file into the store.** Populate the store with a reflink-or-copy strategy only: try `fsutil.Reflink(f.Path, destFile)` first (a CoW clone is isolated), and on failure fall back to `copyFile`. Delete the `os.Link(f.Path, destFile)` branch at `store.go:156-160` and the `useHardLink`/`canUseHardLink` machinery that only exists to enable it. This matches how yalc and pnpm populate their stores — the store must own its bytes.
2. Store → project hardlinks (`link.go:107-119`) may **stay**, because once the store is copy-isolated the store entry is a stable, private copy that no build tool writes to. Document this one-way propagation: editing a consumer's linked files in place would still write back to the store entry (they share an inode), so add a short comment noting that consumers must treat linked packages as read-only and that `push` is the supported way to update them. (If you want full isolation on both hops, reflink-or-copy the store→project step too, but that is a larger change and not required to close this issue.)
3. Because the store no longer shares inodes with a mutating source, the `Exists` short-circuit at `store.go:80-83` becomes sound again (a stored hash's bytes can no longer drift). No change to `Exists` is required for this fix, but do not reintroduce any path that lets external writes reach a store entry.
4. Keep the parallel-worker structure in `Store` intact; only the per-file linking decision changes (reflink → copy, dropping hardlink).

## Acceptance criteria

- [ ] After `lnpm publish`, no file under `~/.lnpm/store/<pkg>/<hash>/` shares an inode with the corresponding source file (verify with `stat` / `ls -i`: link counts and inode numbers differ).
- [ ] Overwriting a source file in place after publishing does **not** change the bytes in the store entry.
- [ ] A publish that re-produces an existing hash returns a store directory whose contents still hash to that value.
- [ ] The `os.Link(f.Path, ...)` source→store branch is removed; store population uses only reflink-or-copy.
- [ ] `go test ./...` passes.

## Testing

From the repo root:

```
go test ./internal/store/...
go test ./tests/...
go test ./...
```

Add a regression test in `internal/store/` (mirror the location and style of `internal/store/store_permissions_test.go`). Suggested test `TestStore_DoesNotShareInodeWithSource`: write a source file into `t.TempDir()`, call `store.Store(...)`, then `os.Stat` both the source and the stored file and assert their `Sys().(*syscall.Stat_t).Ino` differ (skip on Windows, following the existing `runtime.GOOS == "windows"` skip pattern). A second test `TestStore_SourceMutationDoesNotAffectStore`: publish, overwrite the source in place with new bytes, and assert the stored file still contains the original bytes. Also exercise the full flow end-to-end via `go test ./tests/...` (see `tests/publish_test.go` and `tests/push_test.go`).
