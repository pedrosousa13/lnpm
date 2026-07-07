TITLE: Add tests for the self-update flow (download, checksum, extraction, cache)
LABELS: tests, security
---
## Severity

High. `lnpm update` downloads and replaces the running binary — the riskiest code path in the tool — and today it is essentially 0% covered even with the full suite running, so any regression ships blind and can brick users' installations.

## Background

`lnpm update` checks GitHub for the latest release and, if a newer version exists, downloads the release archive for the current OS/arch, verifies its SHA-256 against the release `checksums.txt`, extracts the `lnpm` binary from the tar.gz (or zip on Windows), and atomically swaps it in place of the currently running binary (with a `.bak` backup and rollback on failure). The version check itself lives in `internal/update` and is also run in the background on normal commands, caching its result in `version_cache.json` inside the store directory for 24 hours.

The only pieces of this that are tested today are the two smallest helpers: `findChecksum` and `sha256File` (see `internal/cli/update_test.go`). Everything else — version comparison, URL construction, downloading, checksum verification, archive extraction, and the cache — has zero coverage.

## Problem

None of the following is exercised by any test:

- `compareVersions` — decides whether an update is offered at all. A regression could offer downgrades or stop offering updates.
- `buildDownloadURL` / `buildChecksumsURL` — a typo in the URL format silently breaks updates for one OS/arch combination.
- `downloadToFile` / `verifyChecksum` — the security boundary. If checksum verification regresses, a tampered or corrupted binary gets installed.
- `extractTarGz` / `extractZip` — extract archive entries by name. These currently neutralize path-traversal entry names (e.g. `../../evil/lnpm`) by taking `filepath.Base(...)` before writing; nothing pins that behavior, so a refactor could silently reintroduce a zip-slip vulnerability.
- `CheckFresh` / `check` / `loadCache` / `saveCache` / `fetchLatestVersion` — the check-and-cache machinery. A cache bug could hammer the GitHub API on every command or pin users to a stale "latest" forever.

The blockers are two hardcoded URLs: `fetchLatestVersion` builds `https://api.github.com/...` inline, and `buildDownloadURL`/`buildChecksumsURL` build `https://github.com/...` inline, so nothing can be pointed at a local test server without a small refactor.

## Where to look

Untested code:

- `internal/cli/update.go:49` — `RunUpdate`, the command entry point (rejects dev builds, checks, then installs).
- `internal/cli/update.go:101` — `wasInstalledViaGo` (GOBIN/GOPATH/`~/go/bin` detection).
- `internal/cli/update.go:165` — `installLatestViaBinary` (backup, swap, rollback).
- `internal/cli/update.go:221` — `buildDownloadURL` (`.zip` on windows, `.tar.gz` elsewhere; hardcoded `https://github.com/...` at line 233).
- `internal/cli/update.go:239` — `downloadBinary` (download → verify → extract pipeline).
- `internal/cli/update.go:278` — `downloadToFile` (uses `updateHTTPClient`, a package var declared at line 27).
- `internal/cli/update.go:301` — `buildChecksumsURL` (hardcoded URL at line 303).
- `internal/cli/update.go:308` — `verifyChecksum`.
- `internal/cli/update.go:363` — `extractTarGz` (note the `filepath.Base(header.Name)` at line 388).
- `internal/cli/update.go:407` — `extractZip` (note the `filepath.Base(file.Name)` at line 422).
- `internal/update/update.go:41` — `CheckFresh` (used by `lnpm update`).
- `internal/update/update.go:67` — `CheckAsync` (background check; honors `LNPM_NO_UPDATE_CHECK`).
- `internal/update/update.go:97` — `check` (cache-first logic, 24h interval).
- `internal/update/update.go:121` — `loadCache` / `internal/update/update.go:141` — `saveCache` (`version_cache.json` under `config.GetStorePath()`, which honors the `LNPM_STORE` env var).
- `internal/update/update.go:160` — `fetchLatestVersion` (hardcoded `https://api.github.com/...` at line 161, uses `http.DefaultClient`).
- `internal/update/update.go:187` — `compareVersions`.

Existing tests to mirror:

- `internal/cli/update_test.go:10` — `TestFindChecksum` and `internal/cli/update_test.go:25` — `TestSha256File`. Same package (`package cli`), plain table/inline style. New `internal/cli` tests go in this file or alongside it; new `internal/update` tests go in a new `internal/update/update_test.go`.

## How to fix

1. **Testability refactor (small, no behavior change).** In `internal/update/update.go`, extract the API base into a package-level var, e.g. `var githubAPIBaseURL = "https://api.github.com"`, and use it in `fetchLatestVersion`. In `internal/cli/update.go`, extract the release-download base similarly, e.g. `var releaseBaseURL = "https://github.com/pedrosousa13/lnpm/releases/download"`, used by both `buildDownloadURL` and `buildChecksumsURL`. Tests override these vars (restore with `defer` or `t.Cleanup`). `updateHTTPClient` is already a package var, so no change needed there.
2. **Unit-test the pure functions.**
   - `compareVersions`: table with `{current, latest, wantAvailable}` — equal versions, patch/minor bump, `v` prefix on either side, latest older than current. Note the implementation is a lexical string comparison, so stick to same-width version segments; if you add a case like `1.9.0` vs `1.10.0`, mark it as documenting a known limitation rather than asserting the semver-correct answer.
   - `buildDownloadURL`: assert the returned filename and URL embed the trimmed version and `runtime.GOOS`/`runtime.GOARCH`, and that the extension is `.zip` when `runtime.GOOS == "windows"` and `.tar.gz` otherwise (branch on `runtime.GOOS` in the test).
   - `buildChecksumsURL`: assert `v` prefix trimming and the `checksums.txt` suffix.
3. **Test extraction with crafted archives.** In the test, build in-memory archives with `archive/tar` + `compress/gzip` and `archive/zip`:
   - Archive containing `lnpm` (plus a decoy file): assert the returned path is inside the temp dir and the extracted content matches.
   - Archive containing no `lnpm` entry: assert the "lnpm binary not found in archive" error.
   - **Path-traversal entry**: an entry named `../../evil/lnpm`. Assert extraction returns a path inside `tmpDir` and that nothing was written outside `tmpDir` (check the parent directories). This pins the `filepath.Base` neutralization so a refactor cannot reintroduce zip-slip.
   - Do the same trio for `extractZip`.
4. **Test download + checksum against `httptest.Server`.** Start an `httptest.Server` that serves a fake archive at one path and a goreleaser-style `checksums.txt` (`<hex>  <filename>` lines) at another. Point `releaseBaseURL` at the server:
   - `downloadToFile`: 200 → file written with exact bytes; 404 → error containing the status.
   - `verifyChecksum`: matching checksum → nil; mismatched checksum → error mentioning "mismatch"; filename absent from checksums.txt → "no checksum listed" error.
5. **Test `fetchLatestVersion` and the cache.** Point `githubAPIBaseURL` at an `httptest.Server` returning `{"tag_name": "v1.2.3"}` → assert `"v1.2.3", nil`; return 500 → assert error. For the cache: `t.Setenv("LNPM_STORE", t.TempDir())`, then `saveCache` + `loadCache` round-trip (path is `<store>/version_cache.json`); also `loadCache` on a missing file (nil cache, non-empty path) and on corrupt JSON (nil cache). Test `check` uses a fresh cache (write a cache file with `LastCheck: time.Now()` and assert no HTTP call is made — the test server records hits) and refetches when the cache is older than 24h.
6. **Leave `RunUpdate`/`installLatestViaBinary` binary-swap untested for now** unless it falls out easily — swapping `os.Executable()` is process-global and brittle. Covering steps 2–5 removes almost all of the risk; note in the test file that the swap itself is exercised manually on release.

## Acceptance criteria

- [ ] `compareVersions`, `buildDownloadURL`, and `buildChecksumsURL` have table-driven unit tests.
- [ ] `extractTarGz` and `extractZip` each have tests for: binary found, binary missing, and a path-traversal entry that must not escape the temp dir.
- [ ] `downloadToFile`, `verifyChecksum`, and `fetchLatestVersion` are tested against a local `httptest.Server` (no real network access in tests).
- [ ] `loadCache`/`saveCache` round-trip and corrupt/missing-cache cases are tested under a temp `LNPM_STORE`.
- [ ] The base-URL refactor changes no production behavior (default values identical to the previous hardcoded URLs).
- [ ] `go test ./internal/update/ -cover` reports well above 0% (target: >70%); `go test ./internal/cli/ -cover` rises measurably.
- [ ] Tests pass with `-race` and on Windows (no unix-only assumptions in archive tests).

## Testing

```
go test ./internal/update/ -cover
go test ./internal/cli/ -cover -run 'TestCompareVersions|TestBuildDownloadURL|TestBuildChecksumsURL|TestExtract|TestDownload|TestVerifyChecksum'
go test -race ./internal/update/ ./internal/cli/
go test ./... -coverpkg=./...   # confirm whole-program coverage rises from ~61.5%
```
