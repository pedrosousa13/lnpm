TITLE: Upgrade the pinned Go toolchain off end-of-life 1.22
LABELS: security, ci
---
## Severity

Medium — release binaries are built with an unsupported Go toolchain that no longer receives standard-library security fixes.

## Background

A Go project declares the language/toolchain version it targets in a `go` directive inside `go.mod`. CI workflows (GitHub Actions YAML files under `.github/workflows/`) separately install a Go version via the `actions/setup-go` step. The Go team supports only the two most recent major releases; older ones stop getting security and bug fixes ("end of life", EOL). As of mid-2026 the supported line is Go 1.25/1.26, and Go 1.22 is EOL. This project pins Go 1.22 in `go.mod` and in every CI/release workflow, so the binaries published to GitHub Releases are compiled with an unsupported toolchain.

## Problem

When a security fix lands in the Go standard library (for example in `net/http`, `crypto/tls`, or `archive/*`), it is only backported to supported releases. Because every build path here uses Go 1.22, released lnpm binaries silently miss those fixes. There is no error — the pipeline keeps working — so the drift is invisible until someone audits it or a CVE is reported against the toolchain.

## Where to look

- `go.mod:3` — `go 1.22` directive (the single source of truth we want to point everything at).
- `.github/workflows/ci.yaml:31` — `go-version: "1.22"` in the Test job's setup-go step.
- `.github/workflows/ci.yaml:68` — `go-version: "1.22"` in the Lint job.
- `.github/workflows/ci.yaml:87` — `go-version: "1.22"` in the Test (Windows) job.
- `.github/workflows/ci.yaml:115` — `go-version: "1.22"` in the Build job.
- `.github/workflows/release-please.yaml:39` — `go-version: "1.22"` in the goreleaser job.

## How to fix

1. Bump the `go` directive in `go.mod:3` to a currently supported version (e.g. `go 1.25`). Run `go mod tidy` afterward.
2. In every `setup-go` step listed above, replace the hardcoded `go-version: "1.22"` with `go-version-file: go.mod`. This makes `go.mod` the single source of truth so the version can never drift between files again.
3. Run the full test suite locally and in CI to confirm nothing broke with the newer toolchain: `go test -race ./...`.
4. Confirm a clean build across all target platforms: `make release`.

## Acceptance criteria

- [ ] `go.mod` declares a currently supported Go version.
- [ ] All five `setup-go` steps use `go-version-file: go.mod` instead of a hardcoded version.
- [ ] `go test -race ./...` passes.
- [ ] `make release` produces binaries for all platforms without error.
- [ ] The CI and release workflows pass on a PR.

## Testing

- Run `go build ./...` and `go test -race ./...` locally.
- Run `make release` and confirm all five platform binaries are produced under `bin/`.
- Push a branch and confirm the CI workflow's Test, Lint, Windows, and Build jobs all pass.
- Optionally dry-run the workflows with `act` if it is installed.
