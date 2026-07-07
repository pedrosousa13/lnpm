TITLE: Preserve permission bits in copy and reflink fallbacks so umask cannot corrupt stored file modes
LABELS: bug
---
## Severity

Low — only manifests under a restrictive umask (common on CI runners and hardened systems), but when it does, the store silently disagrees with its own recorded metadata and consumer packages break.

## Background

lnpm computes a content hash over each package's files that includes each file's permission bits (`Mode.Perm()`), and records the intended mode in both the store's file entries and the database's `FileEntry.Mode` column. When lnpm actually writes bytes to disk — copying into the store, or copying/reflinking into a consumer's `.lnpm/<pkg>` — it does so with `os.OpenFile(dst, flags, mode)`. The `mode` argument to `OpenFile` is not the final permission of the created file: the kernel applies the process's `umask` to it, clearing any bits set in the umask. A common hardened umask is `027` or `077` (many CI images and some Linux distros set this by default).

## Problem

If a package includes an executable file — a bin script published with mode `0755` — and the process creating that file on disk has umask `077` or `027`, `os.OpenFile(dst, os.O_CREATE|..., 0755)` actually creates the file with mode `0700` or `0750`: the group/other execute and read bits are stripped by the umask. The content hash and the database still say `0755` (the hash was computed from the source file's real mode before the umask-affected write happened), so the recorded state and the on-disk state disagree. A consumer that runs the package's bin script (e.g. via `npx` or a `postinstall` hook) gets "permission denied" even though lnpm reports the package as correctly linked.

Concrete failure scenario: a developer runs `lnpm publish` inside a CI job that sets `umask 077` for security. The package includes `bin/cli.js` at mode `0755`. It lands in the store at mode `0700`. Every consumer that later runs `lnpm add` copies or reflinks it forward, inheriting the same wrong mode (reflink preserves the store's on-disk mode, not the originally-intended one). `node_modules/.bin/cli` fails to execute for every consumer, with no error from `lnpm` itself.

## Where to look

- `internal/store/store.go:355` — `copyFile`: `dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)`, the `mode` passed in is subject to umask.
- `internal/link/link.go:377` — `copyFile`: same pattern, `os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())`.
- `internal/fsutil/reflink_linux.go:30` — `tryReflink`: `dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())` before issuing the `FICLONE` ioctl; the destination file's initial mode is also umask-masked, and `FICLONE` clones data blocks only, not permissions, so the masked mode persists after the clone succeeds.
- `internal/pack/pack.go:428` — `HashFiles`: `fmt.Fprintf(h, "%o", f.Mode.Perm())` — the hash is computed from the *intended* mode, which is what will disagree with the umask-masked on-disk mode.

## How to fix

In each of the three locations, explicitly set the final permission after creating the file, bypassing the umask:

1. `internal/store/store.go` — in `copyFile`, after the `io.Copy` succeeds (or right before `return dstFile.Sync()`), call `if err := dstFile.Chmod(mode); err != nil { return err }`.
2. `internal/link/link.go` — in `copyFile`, same pattern: after copying, call `dstFile.Chmod(srcInfo.Mode())` before syncing/returning.
3. `internal/fsutil/reflink_linux.go` — in `tryReflink`, after the `FICLONE` ioctl succeeds (`errno == 0`) and before `dstFile.Sync()`, call `dstFile.Chmod(srcInfo.Mode())` so the clone's permissions match the source regardless of the umask applied when the destination file was created.

`(*os.File).Chmod` sets the exact mode passed to it — it is not subject to umask — so this guarantees the on-disk mode matches what the hash and database record, independent of the calling process's umask.

## Acceptance criteria

- [ ] Under umask `077`, a file published with mode `0755` lands in the store with mode `0755`, not `0700`.
- [ ] Under umask `077`, a file copied into a consumer's `.lnpm/<pkg>` via the non-reflink path has mode `0755`.
- [ ] On Linux, under umask `077`, a file cloned via the `FICLONE` reflink path also has mode `0755`.
- [ ] `go test ./...` passes, including new regression tests for each of the three sites.

## Testing

From the repo root:

```
go test ./internal/store/...
go test ./internal/link/...
go test ./internal/fsutil/...
go test ./...
```

Add regression tests that set a restrictive umask with `syscall.Umask(0077)` (guard with `t.Cleanup` to restore the previous umask — `syscall.Umask` returns the old value) around the operation under test, then assert the resulting file's mode via `os.Stat(path).Mode().Perm()`:

- `internal/store/store_permissions_test.go`: `TestStore_PreservesModeUnderRestrictiveUmask` — store a package containing a `0755` file under umask `0077`, then stat the stored file and assert `Perm() == 0755`.
- `internal/link/link_permissions_test.go`: `TestCopyFile_PreservesModeUnderRestrictiveUmask` — copy a `0755` source file under umask `0077` and assert the destination is `0755`.
- `internal/fsutil/reflink_linux_test.go` (new file, `//go:build linux`): `TestReflink_PreservesModeUnderRestrictiveUmask` — reflink a `0755` source file under umask `0077` on a filesystem that supports `FICLONE` (skip the test if `Reflink` returns "not supported", since CI filesystems may not support it), and assert the destination mode is `0755`.
