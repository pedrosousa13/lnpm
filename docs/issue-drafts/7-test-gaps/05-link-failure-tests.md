TITLE: Add tests for link failure paths and copy fallbacks
LABELS: tests
---
## Severity

Medium. `Link` is the operation that materializes a package into a user's project; its error paths and its copy fallback are unexercised, so a regression could leave projects with dangling symlinks or half-populated `.lnpm/<pkg>` directories that no test would catch.

## Background

When `lnpm add` links a package, `Linker.Link` (`internal/link/link.go:38`) wipes and recreates `.lnpm/<pkg>` in the project, then materializes every store file into it using a tiered strategy executed by a pool of parallel workers: try a reflink (copy-on-write clone), then a hard link (if the store and project share a filesystem and config allows), and finally queue the file for a plain byte copy. A second parallel pass copies the queued files via `copyFile` (`internal/link/link.go:365`). Only after every file is in place does it create the `node_modules/<pkg>` symlink pointing at `.lnpm/<pkg>`. Worker errors are reported through single-slot error channels (`errChan` at line 79 for the link pass, `errChan2` at line 174 for the copy pass) and returned to the caller.

The publish side (`Store.Store`, `internal/store/store.go:70`) uses the same tiered strategy to materialize files into the store, with its own `copyFile` at `internal/store/store.go:348`.

Existing tests (`internal/link/link_test.go`, `internal/link/link_permissions_test.go`) cover the happy path, scoped packages, permissions, and unlink — always with every store file present and readable.

## Problem

- No test exercises a mid-link worker failure: what `Link` returns when a store file disappears or is unreadable between the caller listing files and the workers materializing them, and what state the project is left in (a partially populated `.lnpm/<pkg>`? a `node_modules` symlink? — by code order the symlink should NOT exist, but nothing pins that).
- No test verifies that a failed `Link` is recoverable — that retrying after the store is fixed succeeds cleanly (the leading `os.RemoveAll` of `.lnpm/<pkg>` at line 53 should make retries clean, but nothing proves it).
- The copy fallback inside `Link` (lines 159–209, including `errChan2`) is never reached by any suite: on dev machines and CI the reflink or hard link almost always succeeds. Note the standalone `copyFile` helper itself does have a direct happy-path unit test (`TestCopyFile`, `internal/link/link_test.go:267`) — what is dead is the fallback loop that calls it and its error handling.
- The publish-side `copyFile` (`internal/store/store.go:348`) has no test at all, direct or indirect (its call site is the copy fallback at `internal/store/store.go:221`, which is equally dead in the suites).

## Where to look

Untested code:

- `internal/link/link.go:38` — `Link`; error channels at lines 79 (`errChan`) and 174 (`errChan2`); copy-fallback loop at lines 159–209; symlink creation last at line 216.
- `internal/link/link.go:53` — the `os.RemoveAll` that should make retries clean.
- `internal/link/link.go:224` — `Unlink` (partially covered; the `.lnpm` cleanup-when-empty branch at lines 241–246 is worth asserting).
- `internal/link/link.go:287` — `determineLinkType`: returns `Copy` when config `link_mode: copy` (lines 290–295) — the lever for forcing copy mode.
- `internal/link/link.go:365` — link-side `copyFile` (happy path already tested; error cases are not).
- `internal/store/store.go:319` — `canUseHardLink`; `internal/store/store.go:348` — store-side `copyFile` (0%); call site at line 221.

Existing tests to mirror:

- `internal/link/link_test.go:12` — `TestLinkAndUnlink`: shows the setup pattern (temp store dir + `[]*pack.FileInfo` with `RelPath`s + real files written into the store).
- `internal/link/link_test.go:267` — `TestCopyFile`: the pattern to replicate for the store-side `copyFile`.
- `internal/link/link_permissions_test.go:13` — more `Link` scenarios in the same style.

## How to fix

1. In `internal/link/link_test.go`, add `TestLinkMissingStoreFile`:
   - Set up a store dir and a `files` slice exactly like `TestLinkAndUnlink`, but delete one of the store files (e.g. `dist/utils.js`) AFTER building the slice and BEFORE calling `Link` — simulating the store changing between listing and linking.
   - Assert `Link` returns a non-nil error that names the failing file.
   - Assert the failure state: `node_modules/<pkg>` symlink does NOT exist (it is only created after all files succeed), and note in an assertion or comment that `.lnpm/<pkg>` may be partially populated.
   - Recreate the missing store file, call `Link` again, assert it succeeds and every file (including the previously missing one) now exists under `.lnpm/<pkg>` — proving the leading `RemoveAll` makes retries clean.
2. Add `TestLinkUnreadableStoreFile` (guard with `runtime.GOOS != "windows"` and skip when running as root, since root ignores 0000): `os.Chmod` one store file to `0000`, assert `Link` errors; restore with `t.Cleanup`. This drives the copy path's open error (`copyFile` line 371) through `errChan2` — reflink and hardlink of an unreadable-but-present file typically still succeed on some filesystems, so if this proves flaky across platforms, keep only the deletion-based test and say so in a comment.
3. Add `TestLinkCopyMode` to exercise the copy decision from config: write a temp config file containing `link_mode: copy`, `t.Setenv("LNPM_CONFIG", path)`, then `Link` a package and assert success plus correct file contents in `.lnpm/<pkg>`. Two caveats to handle in the test: (a) `config.LoadConfig` caches via `sync.Once`, so this test must not run after another test in the same package has already loaded config with a different `LNPM_CONFIG` — verify ordering or read the returned `LinkType`; (b) even in copy mode, reflink is attempted first (line 102) and succeeds on APFS/Btrfs/XFS, so on macOS the bytes may be reflinked rather than copied — assert on outcomes (files exist, content matches, `Link` returned `Copy` type) rather than on which syscall ran.
4. In `internal/store/` (e.g. a new `store_copy_test.go`), mirror `TestCopyFile` for the store-side `copyFile` at `store.go:348`: happy path preserving the passed mode (create with 0755, assert 0755 on the destination on non-Windows), missing source error, and destination-directory-missing error. A full forced-fallback test of `Store.Store` would require a cross-filesystem setup, which is not portable — the direct unit test is the practical target; note this in a comment.
5. While in `Unlink` territory, add an assertion (new test or extend `TestLinkAndUnlink`) that unlinking the last package removes the now-empty `.lnpm` directory (lines 241–246).

## Acceptance criteria

- [ ] A test proves `Link` surfaces an error when a store file is missing, leaves no `node_modules/<pkg>` symlink behind, and succeeds on retry once the store is repaired.
- [ ] The copy fallback inside `Link` (the `filesToCopy`/`errChan2` path) is exercised by at least one test, via file deletion and/or copy mode.
- [ ] `internal/store`'s `copyFile` has direct unit tests (happy path with mode preservation, missing source).
- [ ] The empty-`.lnpm`-cleanup branch of `Unlink` is asserted.
- [ ] Platform-specific tests are correctly guarded (`runtime.GOOS`, root check) and everything passes on Linux, macOS, and Windows with `-race`.
- [ ] `go test ./internal/link/ -cover` and `go test ./internal/store/ -cover` both rise measurably.

## Testing

```
go test ./internal/link/ -cover -v -run 'TestLink|TestUnlink'
go test ./internal/store/ -cover -v -run TestCopyFile
go test -race ./internal/link/ ./internal/store/
```
