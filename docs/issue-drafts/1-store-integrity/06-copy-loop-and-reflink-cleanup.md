TITLE: Harden the copy loop and reflink cleanup: use io.Copy, errors.Is, and single-close/unlink-on-failure
LABELS: bug
---
## Severity

Low — both defects are robustness issues rather than data-corruption bugs on the common path: the hand-rolled copy loop happens to work, and the reflink cleanup gap only leaves a stray file behind or masks which code path actually succeeded.

## Background

lnpm copies files in a few different places when populating the store and linking packages into consumer projects. `internal/store/store.go` already does this the idiomatic way, using `io.Copy` to stream bytes from source to destination. `internal/link/link.go` has its own separate `copyFile` that instead hand-rolls the read/write loop. On Linux, `internal/fsutil/reflink_linux.go` additionally supports a copy-on-write clone via the `FICLONE` ioctl, with a fallback to a regular copy when the filesystem doesn't support it.

## Problem

Two independent robustness gaps:

**(a) Hand-rolled copy loop with string-compared EOF.** `internal/link/link.go`'s `copyFile` reads into a manual buffer and loop, and detects end-of-file by comparing `err.Error() == "EOF"` instead of `errors.Is(err, io.EOF)`. Comparing error strings is fragile: it only works because the standard library's `io.EOF` happens to format as the literal text `"EOF"` today, but any wrapped error (e.g. from a future `os.File` change, or a different `io.Reader` implementation used later) would never match this string check even though it correctly signals a wrapped EOF, and the function would return that non-nil error as a real I/O failure instead of finishing successfully. The loop also duplicates `io.Copy`, which already exists, is well-tested, and is used correctly two lines away in `internal/store/store.go`.

**(b) Reflink cleanup leaves a stray file and double-closes.** In `internal/fsutil/reflink_linux.go`'s `tryReflink`, if the `FICLONE` ioctl succeeds (`errno == 0`) but the subsequent `dstFile.Sync()` fails, the function returns `false` immediately — without removing the partially-cloned `dst` file. The caller (`link.go`'s reflink-then-fallback logic) sees `false`/an error from `Reflink`, falls back to `os.Link` or a regular copy targeting the same `dst` path, and that fallback then fails with `EEXIST` (for `os.Link`) or silently overwrites the leftover clone (a copy opened with `O_TRUNC`). Either way, the failed Sync's leftover file is not cleaned up by the function that created it. Separately, the "clean up on failure" branch (when the ioctl itself fails) calls `dstFile.Close()` explicitly and then relies on the function's `defer dstFile.Close()` to run again on return — an explicit close followed by a deferred close of the same `*os.File`, which is at best redundant and at worst masks a real close error (the deferred close's return value is discarded either way, but the pattern invites bugs if this function is ever refactored to check close errors).

## Where to look

- `internal/link/link.go:364-400` — `copyFile`, the full hand-rolled read/write loop.
- `internal/link/link.go:392` — `if err.Error() == "EOF" {` — string comparison instead of `errors.Is(err, io.EOF)`.
- `internal/store/store.go:361` — `io.Copy(dstFile, srcFile)` in `store.go`'s own `copyFile`, the pattern to imitate.
- `internal/fsutil/reflink_linux.go:48-53` — the success branch: `if errno == 0 { if err := dstFile.Sync(); err != nil { return false } return true }` — returns `false` on Sync failure without removing `dst`.
- `internal/fsutil/reflink_linux.go:55-58` — the ioctl-failure branch: `dstFile.Close()` followed by `_ = os.Remove(dst)`, then `return false`; combined with the deferred `defer dstFile.Close()` at line 34, both the explicit `dstFile.Close()` at line 56 and this deferred close run against the same file handle.

## How to fix

**(a) `internal/link/link.go`:**
1. Replace the manual buffer-and-loop body of `copyFile` with a single `if _, err := io.Copy(dstFile, srcFile); err != nil { return err }`, matching `internal/store/store.go`'s `copyFile` exactly.
2. Remove the now-unused 1MB buffer allocation and the `err.Error() == "EOF"` check entirely — `io.Copy` already treats `io.EOF` from the reader as a clean end-of-stream and returns `nil`.
3. Keep everything else in the function (the `os.Stat` for source mode, `os.OpenFile` for the destination, the `Sync()` at the end) unchanged.

**(b) `internal/fsutil/reflink_linux.go`:**
1. Restructure `tryReflink` so there is exactly one cleanup path instead of two: track success with a boolean and use a single `defer` that closes `dstFile` once and removes `dst` if the function did not succeed. For example, defer a closure that runs `dstFile.Close()` and, if a `success` flag is still false, `os.Remove(dst)` — then remove the explicit `dstFile.Close()` calls at the ioctl-failure branch and drop the separate `defer dstFile.Close()` at line 34.
2. In the `errno == 0` branch, if `dstFile.Sync()` fails, do not just `return false` — let the deferred cleanup remove the half-cloned `dst` (since `success` remains false at that point), so the caller never finds a stray file at `dst` after a failed reflink attempt.
3. Only set `success = true` (skipping the removal) once `Sync()` returns `nil`.

## Acceptance criteria

- [ ] `internal/link/link.go`'s `copyFile` uses `io.Copy` and contains no manual read/write loop or string-compared EOF check.
- [ ] `internal/fsutil/reflink_linux.go`'s `tryReflink` closes `dstFile` exactly once on every return path.
- [ ] If `FICLONE` succeeds but `Sync()` fails, `dst` does not exist on disk after `tryReflink` returns `false`.
- [ ] If the `FICLONE` ioctl itself fails, `dst` does not exist on disk after `tryReflink` returns `false` (existing behavior, preserved).
- [ ] `go test ./...` passes.

## Testing

From the repo root:

```
go test ./internal/link/...
go test ./internal/fsutil/...
go test ./...
```

- `internal/link/link_test.go` already has `TestCopyFile` — extend it or add `TestCopyFile_LargeFile` with a source file larger than typical buffer sizes (a few MB of generated data) to exercise the `io.Copy`-based rewrite end to end and confirm byte-for-byte equality (compare via `sha256` or direct `bytes.Equal` on the read-back contents).
- Add `internal/fsutil/reflink_linux_test.go` (new file, `//go:build linux`): `TestTryReflink_NoStrayFileOnSyncFailure` — this requires forcing `Sync()` to fail after a successful ioctl, which is hard to trigger directly; if there's no clean way to inject a Sync failure, instead add `TestTryReflink_NoDoubleCloseOnIoctlFailure` that reflinks onto a destination path guaranteed to make the ioctl fail (e.g. across two different temp filesystems, or skip with `t.Skip` if the test environment's filesystem supports `FICLONE` everywhere) and assert `os.Remove(dst)` afterward returns `os.ErrNotExist` (i.e., `tryReflink` already cleaned it up) and that the function returns without panicking or logging a close error.
