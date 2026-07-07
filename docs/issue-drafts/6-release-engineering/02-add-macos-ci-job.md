TITLE: Add a macOS CI job to test the darwin binaries we ship
LABELS: ci, tests
---
## Severity

Medium — we publish macOS binaries but never run the test suite on macOS, so darwin-specific bugs can ship undetected.

## Background

CI is defined in `.github/workflows/ci.yaml`. Each job declares `runs-on:` to pick a GitHub-hosted runner OS. Today CI runs the test suite on `ubuntu-latest` and `windows-latest` only. Meanwhile GoReleaser (`.goreleaser.yaml`, the config that builds and publishes release archives) is configured to build `darwin` (macOS) binaries for both amd64 and arm64. So we ship a platform we never test. The test suite also exercises real npm behavior end-to-end, which is why the existing jobs install Node.js via `actions/setup-node`.

## Problem

A bug that only manifests on macOS — for example a filesystem path, hard-link, or case-sensitivity difference — would pass all of CI and still be shipped in the darwin release binaries. Given that lnpm's core mechanism is hard-linking files into local projects, macOS-specific filesystem behavior is exactly the kind of thing that needs coverage.

## Where to look

- `.github/workflows/ci.yaml:19-101` — the job definitions; only `ubuntu-latest` (line 22) and `windows-latest` (line 78) are exercised, no macOS.
- `.github/workflows/ci.yaml:33-36` — the `actions/setup-node` step pattern (`node-version: "20"`) that a macOS job must replicate, because the e2e suite needs Node.
- `.github/workflows/ci.yaml:42,45` — the Linux test steps run with `-race` (the race detector).
- `.github/workflows/ci.yaml:98,101` — the Windows test steps also run with `-race`.
- `.goreleaser.yaml:16-18` — `goos:` includes `darwin`, which is the platform we ship but do not test.

## How to fix

1. Add a `test-macos` job to `ci.yaml` that mirrors the Windows job but sets `runs-on: macos-latest`. (Alternatively, refactor the Linux/Windows/macOS jobs into a single matrix job with `strategy.matrix.os: [ubuntu-latest, windows-latest, macos-latest]` — cleaner but a larger change.)
2. Include the `actions/setup-node` step (mirroring lines 33-36) so the e2e suite has Node available.
3. Keep the race detector on: run `go test -v -race ./internal/...` and `go test -v -race ./tests/...`, matching the existing jobs.
4. Add the new job to the Build job's `needs:` list (`.github/workflows/ci.yaml:106`) so a macOS failure blocks the build.

## Acceptance criteria

- [ ] CI runs the unit tests and the workspace/e2e tests on `macos-latest`.
- [ ] The macOS job installs Node before running the e2e suite.
- [ ] The macOS job runs with the race detector enabled.
- [ ] The Build job depends on the macOS job passing.
- [ ] All jobs pass on a PR.

## Testing

- Push a branch and confirm the new macOS job appears and passes in the Actions run.
- Confirm the e2e tests (which shell out to npm) succeed on the macOS runner.
- Optionally dry-run locally with `act` (note: `act` does not provide real macOS runners, so a real branch push is the reliable check).
