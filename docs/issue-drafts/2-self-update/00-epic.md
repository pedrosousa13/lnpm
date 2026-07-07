TITLE: Fix the self-update and install chain so users keep receiving updates
LABELS: epic, bug
---
## Overview

`lnpm` is a command-line tool distributed as a compiled binary. Users get it in three ways: a `curl | sh` installer script (`install.sh`), a PowerShell installer (`install.ps1`), or `go install`. Once installed, the tool can update itself: `lnpm update` asks the GitHub API for the newest release, downloads the release archive, checks its contents against a published checksum file, and swaps the running binary for the new one. A background check also runs on ordinary commands and prints a one-line "Update available" notice when a newer version exists.

This whole chain is currently broken in several independent ways. The most serious defect is live in released versions today: the version comparison uses plain text (alphabetical) ordering, so a user on `v1.9.0` is told they are already up to date even though `v1.10.0` and `v1.11.0` exist. Other defects mean `go install` users can never self-update, transient network failures are silently reported as "up to date", the update can fail on common Linux setups where `/tmp` is a separate filesystem, and the installer scripts run downloaded binaries without verifying integrity.

Taken together, a large fraction of users silently stop receiving updates — including security fixes — with no error and no indication anything is wrong.

## Why this matters

- Updates that never arrive include bug fixes and security fixes. A user stuck on an old version has no way to know they are missing them.
- Every failure mode here is *silent*: the tool reports success ("Already up to date") while doing nothing. Silent failures are the hardest kind for users to notice or report.
- The install scripts execute a downloaded binary with no integrity check, even though the release already publishes the checksums needed to verify it and the self-updater already uses them.

## Sub-issues

Implement in this order (earlier fixes unblock or de-risk later ones):

1. Compare versions with a semver library instead of text ordering
2. Report a real version for `go install` builds so they can self-update
3. Make `lnpm update` report network failures instead of "Already up to date"
4. Stage the updated binary next to the target to avoid cross-filesystem rename failures
5. Verify release checksums in the install scripts before running the binary
6. Clean up updater temp directories and fix the go-install path detection

## Definition of done

- [ ] A user on an older release (e.g. `v1.9.0`) running `lnpm update` is correctly told a newer version is available and can install it.
- [ ] A binary produced by `go install ...@latest` reports a real version from `lnpm --version` and can self-update.
- [ ] A network failure during `lnpm update` produces a non-zero exit and a clear error, never "Already up to date".
- [ ] `lnpm update` succeeds on systems where the system temp directory is on a different filesystem from the install location.
- [ ] Both installer scripts verify the downloaded archive against the published checksums and abort on mismatch.
- [ ] Updater temp directories and archives are removed on success as well as failure.
- [ ] All existing tests pass (`go test ./...`) and new tests cover the version comparison and installer verification logic.
