TITLE: Stage the updated binary next to the target to avoid cross-filesystem rename failures
LABELS: bug
---
## Severity

Medium — on common Linux configurations where the system temp directory is a separate filesystem, `lnpm update` can never succeed; it fails every time and restores the old binary.

## Background

To replace itself, the updater downloads and extracts the new binary into a temporary directory created with `os.MkdirTemp("", ...)`. On many systems `/tmp` (the default location for that temp directory) is a `tmpfs` RAM-backed filesystem, which is a *different* filesystem from where the binary is installed (e.g. `/usr/local/bin`). The updater then uses `os.Rename` to move the new binary into place. On Unix, `rename(2)` cannot move a file across filesystem boundaries — it returns the `EXDEV` error, literally "invalid cross-device link". A rename only works within a single filesystem.

## Problem

Because the new binary lives on `tmpfs` (`/tmp`) and the target is on the root or `/usr` filesystem, the final `os.Rename(newBin, binPath)` fails with EXDEV. The updater's error handling then restores the backed-up old binary, so the machine is left working but on the old version. On any system where `/tmp` is a separate mount from the install directory, the update can *never* succeed — it fails identically every time, with the error "failed to install new binary: ... invalid cross-device link".

Concrete failure: user on a distro with `tmpfs` `/tmp` has `lnpm` in `/usr/local/bin`; runs `lnpm update`; download and checksum verification succeed; expected outcome is the binary is replaced; actual outcome is EXDEV, backup restored, still on old version.

## Where to look

- `internal/cli/update.go:241` — `os.MkdirTemp("", "lnpm-update-")` creates the staging directory in the system temp location (often `tmpfs`).
- `internal/cli/update.go:186-190` — `downloadBinary` returns the extracted binary path (`newBin`), which lives under that temp dir.
- `internal/cli/update.go:193-196` — backs up the current binary with `os.Rename(binPath, backup)` (this rename is same-directory, so it works).
- `internal/cli/update.go:199` — `os.Rename(newBin, binPath)`: the cross-device rename that fails with EXDEV.
- `internal/cli/update.go:201-204` — restores the backup on failure, which is why the machine ends up back on the old version.

## How to fix

The reliable fix is to make the final rename a same-filesystem operation by staging the new binary in the *target's* directory before renaming:

1. Compute the target directory once in `installLatestViaBinary`: `destDir := filepath.Dir(binPath)`.
2. After `downloadBinary` returns `newBin` (which is still in the temp dir), move it next to the target using a copy-then-place, since a plain rename would hit the same EXDEV. The simplest robust approach: create a staging file in `destDir` (e.g. via `os.CreateTemp(destDir, ".lnpm-update-*")`), copy the extracted binary's bytes into it, `chmod` it to `0755`, close it, then `os.Rename(stagedPath, binPath)` — this rename is now within one filesystem and succeeds.
3. Keep the existing backup/restore logic (lines 193-213) around this new rename so a failure still restores the old binary. Ensure the staging temp file in `destDir` is removed on any error path (defer an `os.Remove`).
4. `destDir` may not be writable by the user (e.g. `/usr/local/bin` owned by root). That is a pre-existing limitation, not introduced here; if `os.CreateTemp(destDir, ...)` fails with a permission error, return a clear error telling the user to re-run with sufficient privileges rather than falling back to a cross-device rename.

## Acceptance criteria

- [ ] `lnpm update` completes successfully when the system temp directory is on a different filesystem from the install location.
- [ ] The new binary is staged in the same directory as the target before the final rename.
- [ ] On failure, the original binary is still restored (existing backup behavior preserved).
- [ ] Any staging file created next to the target is cleaned up on both success and failure.

## Testing

From the repository root:

```
go build ./...
go test ./internal/cli/...
```

The install logic is not currently unit-tested end to end. Add a focused test for the staging helper: create two things a test can control — an extracted "binary" file in a `t.TempDir()` and a target path in a *different* `t.TempDir()` — and assert the helper places the bytes at the target with mode `0755` and leaves no staging file behind. If you extract the copy-and-rename into a small helper like `installFile(src, dst string) error`, it becomes directly testable. To reproduce the original bug manually on Linux: install `lnpm` under `/usr/local/bin`, confirm `/tmp` is `tmpfs` (`findmnt /tmp`), and run `lnpm update`.
