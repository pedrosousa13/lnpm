TITLE: Verify release checksums in the install scripts before running the binary
LABELS: security
---
## Severity

Medium — the install scripts download and run a binary with no integrity check, so a corrupted or tampered download is executed without warning. This is a hardening fix, not a live exploit.

## Background

Users install `lnpm` by piping a script into their shell: `curl -fsSL .../install.sh | sh` on Unix, or `irm .../install.ps1 | iex` on Windows. Each script downloads the release archive (`.tar.gz` or `.zip`) from GitHub, extracts it, and copies the binary onto the user's `PATH`. Every GitHub release also publishes a `checksums.txt` file listing the SHA-256 hash of each archive. A SHA-256 checksum is a fixed-length fingerprint of a file's bytes; if even one byte changes, the fingerprint changes, so comparing the downloaded file's computed hash against the published one detects corruption or tampering. The self-updater built into `lnpm` already does this verification; the install scripts do not.

## Problem

Both installers extract and run the downloaded archive with no integrity check whatsoever. If the download is corrupted (truncated transfer, a partially-uploaded release asset) or altered in transit (a TLS-intercepting corporate proxy, a man-in-the-middle on a compromised network), the installer will happily extract and place a bad or malicious binary on the user's `PATH` and tell them installation succeeded.

Concrete failure: a proxy rewrites the downloaded tarball; `install.sh` extracts it and copies the altered binary to `~/.local/bin/lnpm`; the user runs the compromised binary. Expected outcome: the installer detects the hash mismatch and aborts. Actual outcome: "Installation complete!".

Honest scope note: `checksums.txt` is fetched from the same GitHub release as the archive (same origin). Verifying against it protects against download corruption and in-transit tampering of the *archive alone*, but not against a fully compromised release channel where an attacker could replace both the archive and the checksums file. This fix meaningfully raises the bar (it matches what the self-updater already enforces) without claiming to defend against a compromised publisher.

## Where to look

- `install.sh:98` — builds `URL`, the archive download URL. The release's `checksums.txt` lives at the sibling URL `https://github.com/${REPO}/releases/download/v${VERSION}/checksums.txt`.
- `install.sh:106-111` — downloads the archive into `$TMP_DIR/$FILENAME`.
- `install.sh:114-120` — extracts the archive with no verification between download and extraction.
- `install.ps1:52-53` — builds `$Filename` and `$Url`.
- `install.ps1:61-65` — downloads (`Invoke-WebRequest`) then immediately extracts (`Expand-Archive`), with no verification.
- `internal/cli/update.go:300-331` — the existing checksum logic in the Go updater (`buildChecksumsURL`, `verifyChecksum`) to mirror. The checksum file format is goreleaser's `"<hex>  <filename>"`, one entry per line (see `findChecksum` at `internal/cli/update.go:350-360`).

## How to fix

For `install.sh` (POSIX `sh`), between the download step (ends line 111) and the extract step (line 114):

1. Download `checksums.txt` for the release into `$TMP_DIR` using the same curl/wget branch pattern already used for the archive.
2. Compute and compare the SHA-256. Prefer a `-c` style verification: extract the expected line for `$FILENAME` from `checksums.txt` and pipe it to `sha256sum -c -` (Linux) or `shasum -a 256 -c -` (macOS, which lacks `sha256sum`). Detect which tool is available with `command -v`, mirroring how the script already probes for `curl`/`wget`. If neither hashing tool exists, `error` out rather than installing unverified.
3. On mismatch, call the existing `error()` helper (which prints and exits non-zero) *before* extracting. Only extract after verification passes.

For `install.ps1`, between the download (line 62) and extract (line 65):

1. Download `checksums.txt` with `Invoke-WebRequest` into `$TmpDir`.
2. Compute the archive hash with `Get-FileHash -Algorithm SHA256 $ZipPath` and read its `.Hash`.
3. Parse `checksums.txt` for the line ending in `$Filename`, take the first whitespace-separated field as the expected hash, and compare case-insensitively (`-ieq`) to the computed hash.
4. On mismatch or a missing entry, call `Write-Err` (which exits non-zero) before `Expand-Archive`.

Keep the existing `trap`/`finally` temp-directory cleanup in both scripts so the downloaded files are removed regardless of outcome.

## Acceptance criteria

- [ ] `install.sh` downloads `checksums.txt` and verifies the archive's SHA-256 before extracting; it aborts non-zero on mismatch or missing entry.
- [ ] `install.sh` works on both Linux (`sha256sum`) and macOS (`shasum -a 256`), and errors clearly if no hashing tool is available.
- [ ] `install.ps1` verifies the archive with `Get-FileHash` before extracting and aborts on mismatch.
- [ ] A successful install prints an explicit confirmation that the checksum was verified (matching the updater's "Checksum verified" message tone).
- [ ] Temp files are still cleaned up on all paths.

## Testing

The scripts are shell/PowerShell, not Go, so there is no `go test` coverage. Verify manually:

```
sh install.sh          # from repo root; should print a checksum-verified line then succeed
```

Then test the failure path by pointing the script at a mismatching checksum (e.g. temporarily edit the downloaded `checksums.txt` in the temp dir, or run against a locally served archive whose bytes differ) and confirm it aborts with a non-zero exit and does not place a binary on `PATH`. Run `install.sh` through `shellcheck install.sh` to catch POSIX portability mistakes. On Windows, run `install.ps1` and confirm both the success and tampered-file paths behave correctly.
