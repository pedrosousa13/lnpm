TITLE: Fix the no-op e2e coverage measurement and add Node setup to the release job
LABELS: ci, tests
---
## Severity

Medium — the coverage percentage reported on every PR materially understates real test coverage, and the release job silently depends on an environment detail (preinstalled Node) that isn't guaranteed.

## Background

`tests/` and `tests/e2e/` in this repo hold integration and end-to-end tests — they call into code that lives in other packages (`internal/...` etc.) rather than containing testable code of their own. When `go test -coverprofile=...` runs against a package like that without also being told `-coverpkg=./...`, Go only measures coverage of the package under test itself; since these test packages have no statements of their own to cover, Go reports `[no statements]` and the resulting coverage file carries no real data. Codecov is the service CI uploads `coverage.out` to, which then annotates PRs with a coverage percentage. Separately, the e2e suite shells out to `npm` to exercise real npm project behavior, so any job running it needs Node.js installed via `actions/setup-node`.

## Problem

Two related gaps:

**(a) No-op coverage.** `.github/workflows/ci.yaml:45` runs `go test -v -race -coverprofile=coverage-workspace.out ./tests/...` with no `-coverpkg` flag. Running this locally confirms it: the test output reports `coverage: [no statements]` for both `tests` and `tests/e2e`, and the resulting `coverage-workspace.out` contains only the single header line `mode: set` — no per-line coverage data at all. That empty file is merged with the unit-test coverage and uploaded to Codecov (lines 47-55), so the number shown on every PR reflects only the `./internal/...` unit tests, not the substantial behavior exercised through the integration/e2e suite. Re-running the full suite locally with `-coverpkg=./...` shows true overall coverage is approximately 61.5% — a very different (and more accurate) number than whatever partial figure Codecov currently displays.

**(b) Implicit Node dependency in the release job.** `.github/workflows/release-please.yaml:41-42` runs `go test -v -race ./...`, which includes the e2e tests that require `npm`. Unlike `ci.yaml` (which explicitly installs Node at lines 33-36), `release-please.yaml` has no `actions/setup-node` step. This currently works only because GitHub's `ubuntu-latest` runner image happens to preinstall Node — an implementation detail, not a guarantee. If that default ever changes, the release job's test step fails during an actual release cut, blocking shipping at the worst possible time.

## Where to look

- `.github/workflows/ci.yaml:42` — unit test step (`./internal/...`), missing `-coverpkg`.
- `.github/workflows/ci.yaml:45` — workspace/e2e test step (`./tests/...`), missing `-coverpkg`, produces an empty coverage profile.
- `.github/workflows/ci.yaml:33-36` — the `actions/setup-node@v4` pattern to replicate in the release workflow.
- `.github/workflows/release-please.yaml:41-42` — `go test -v -race ./...` with no Node setup step anywhere in the job.

## How to fix

1. Add `-coverpkg=./...` to the unit test step at `ci.yaml:42`.
2. Add `-coverpkg=./...` to the workspace/e2e test step at `ci.yaml:45`.
3. Re-run both commands locally to confirm neither profile reports `[no statements]` and that the merged `coverage.out` reflects whole-codebase coverage.
4. Add an `actions/setup-node@v4` step to `release-please.yaml`, mirroring `ci.yaml:33-36` (`node-version: "20"`), placed before the `go test` step at line 41.

## Acceptance criteria

- [ ] Both test steps in `ci.yaml` (`:42` and `:45`) include `-coverpkg=./...`.
- [ ] The coverage percentage uploaded to Codecov reflects whole-codebase coverage (~61.5%), not just `./internal/...`.
- [ ] `release-please.yaml` installs Node before running `go test ./...`.
- [ ] CI and the release workflow both pass on a PR/tag after the change.

## Testing

- Locally run `go test -race -coverpkg=./... -coverprofile=coverage-unit.out ./internal/...` and `go test -race -coverpkg=./... -coverprofile=coverage-workspace.out ./tests/...`; confirm neither reports `[no statements]`.
- Merge the two profiles as CI does and run `go tool cover -func=coverage.out | tail -1`; confirm the total is near 61%, not the smaller pre-fix number.
- Push a branch and confirm the Codecov comment/check on the PR reflects the higher, accurate percentage.
- For the release job change, verify by inspection that `setup-node` is present and correctly ordered before the test step; since this job only runs on a release-please-created release, there is no cheap way to trigger it directly on a feature branch.
