TITLE: Clean up updater temp directories and fix the go-install path detection
LABELS: bug
---
## Severity

Low — neither defect breaks an update outright: one leaks disk space over time, the other only changes which update method is chosen for an unusual binary layout.

## Background

`lnpm update` downloads the new release archive into a temporary directory, verifies its checksum, extracts the binary, and then replaces the running binary with it. Separately, `lnpm` decides how to update itself based on how it was originally installed: binaries installed via `go install` are updated by re-running `go install ...@latest`, while binaries installed via the shell/PowerShell installer scripts are updated by downloading and swapping the binary directly. That decision is made by `wasInstalledViaGo`, which checks whether the running binary's path sits inside a Go bin directory (`GOBIN`, `GOPATH/bin`, or `$HOME/go/bin`).

## Problem

**(a) Temp directory leaked on every successful update.** `downloadBinary` creates a temp directory with `os.MkdirTemp("", "lnpm-update-")` and removes it with `os.RemoveAll(tmpDir)` on every failure path (download failure, checksum failure, extraction failure). On success, it returns only the path to the extracted binary inside that directory. The caller, `installLatestViaBinary`, defers `os.Remove(newBin)` — removing just the single extracted binary file — but never removes the temp directory itself or the original downloaded archive (`.tar.gz`/`.zip`) still sitting next to it. Every successful `lnpm update` leaves behind a directory in the system temp location containing a multi-megabyte archive that is never cleaned up.

**(b) `wasInstalledViaGo` prefix check lacks a path-separator boundary.** The GOPATH/bin (and GOBIN, and default `$HOME/go/bin`) checks use `strings.HasPrefix(binPath, gopathBin)` to decide whether the binary lives under that directory. `strings.HasPrefix` matches on raw bytes, not path components, so a binary at `/home/u/go/bin-other/lnpm` would be reported as "installed via go install" because the string `/home/u/go/bin-other/lnpm` starts with the string `/home/u/go/bin`, even though `bin-other` is a sibling directory, not a subdirectory of `bin`. The practical impact is limited to misclassifying which update method to use for a binary that happens to live in a directory whose name starts with the Go bin directory's name.

## Where to look

- `internal/cli/update.go:186-190` — `installLatestViaBinary`: `newBin, err := downloadBinary(...)` followed by `defer func() { _ = os.Remove(newBin) }()`, which only removes the extracted binary, not the temp directory it lives in.
- `internal/cli/update.go:241-274` — `downloadBinary`: creates `tmpDir` at line 241, removes it with `os.RemoveAll(tmpDir)` on the three failure paths (lines 249, 256, 270), but on the success path (`return binaryPath, nil` at line 274) the directory is left on disk.
- `internal/cli/update.go:113-133` — `wasInstalledViaGo`: three `strings.HasPrefix(binPath, ...)` checks (GOBIN at line 114, GOPATH/bin at line 122, default `$HOME/go/bin` at line 130), none of which enforce a path-separator boundary.

## How to fix

**(a)** Have `downloadBinary` also return the temp directory so the caller can own its cleanup, and remove the whole directory instead of just the extracted binary:

1. Change `downloadBinary`'s signature to return the temp directory alongside the binary path, e.g. `func downloadBinary(version, url, filename string) (binaryPath, tmpDir string, err error)`, returning `tmpDir` on every path (including failures, though those already clean up internally).
2. In `installLatestViaBinary`, capture both return values: `newBin, tmpDir, err := downloadBinary(...)`, and replace `defer func() { _ = os.Remove(newBin) }()` with `defer func() { _ = os.RemoveAll(tmpDir) }()`. This removes the archive, the extracted binary, and the directory itself in one call, on both success and any later failure in `installLatestViaBinary`.

**(b)** Require a path-separator boundary after the prefix in all three checks in `wasInstalledViaGo`:

1. Replace each `strings.HasPrefix(binPath, X)` check with `filepath.Dir(binPath) == X` (the binary's containing directory must equal the Go bin directory exactly), which is stricter and clearer than prefix matching for what is actually being tested ("is this binary directly inside that bin directory").
2. Apply this to all three checks: the `GOBIN` check, the `GOPATH/bin` check, and the default `$HOME/go/bin` check.

## Acceptance criteria

- [ ] After a successful `lnpm update`, no `lnpm-update-*` directory remains under the system temp directory.
- [ ] After a failed `lnpm update` (download, checksum, or extraction failure), no `lnpm-update-*` directory remains (existing behavior, preserved).
- [ ] `wasInstalledViaGo` returns `false` for a binary at a path like `<gopath>/bin-other/lnpm` that merely shares a prefix with the Go bin directory.
- [ ] `wasInstalledViaGo` still returns `true` for a binary directly inside `GOBIN`, `GOPATH/bin`, or `$HOME/go/bin`.
- [ ] `go test ./...` passes.

## Testing

From the repo root:

```
go test ./internal/cli/... -run TestWasInstalledViaGo -v
go test ./internal/cli/...
go test ./...
```

Add to `internal/cli/update_test.go`:

- `TestWasInstalledViaGo_RejectsSiblingDirectory`: set `t.Setenv("GOPATH", tmpDir)`, create `tmpDir/bin-other/lnpm` (not `tmpDir/bin/lnpm`), and assert `wasInstalledViaGo()` returns `false` when the current executable path resolves under `bin-other`. Since `wasInstalledViaGo` reads `os.Executable()` rather than taking a path parameter, either refactor it to accept a `binPath string` argument for testability (preferred — update its one call site accordingly) or test the boundary logic via a small extracted helper (e.g. `isUnderBinDir(binPath, binDir string) bool`) that both the fix and the test use directly.
- `TestDownloadBinary_CleansUpTempDirOnSuccess`: this requires network access or a mocked HTTP server; if `downloadBinary` isn't easily testable against a local server, instead add a narrower test around the extracted staging/cleanup helper, or verify via a lightweight integration check that runs `downloadBinary` against an `httptest.Server` serving a small fixture archive and checksum file, then asserts `os.Stat(tmpDir)` returns `os.ErrNotExist` after the caller's cleanup runs.
