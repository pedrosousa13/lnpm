TITLE: Update all outdated Go dependencies and add automated dependency updates
LABELS: security, ci
---
## Severity

Medium — several direct dependencies are years behind, which means known bugs and any disclosed CVEs in them ship in every lnpm release until someone happens to notice and upgrade manually.

## Background

A Go module's `go.mod` pins exact versions of its dependencies; `go list -m -u all` shows, for each one, the version in use and the latest version available. Nothing updates these automatically — without a bot like Dependabot or Renovate opening pull requests on a schedule, dependencies only move when a human remembers to run `go get -u`. This project has no such automation, so its dependencies have drifted quietly, in some cases for years.

## Problem

Running `go list -m -u all` today shows every direct dependency is behind, some substantially:

- `github.com/bmatcuk/doublestar/v4` v4.6.1 → v4.10.0
- `github.com/panjf2000/ants/v2` v2.11.4 → v2.12.1
- `github.com/spf13/cobra` v1.8.1 → v1.10.2
- `go.etcd.io/bbolt` v1.3.8 → v1.5.0
- `golang.org/x/sync` v0.11.0 → v0.21.0
- `golang.org/x/sys` v0.4.0 → v0.46.0 (roughly three years behind)

Because nothing is automated, this gap only grows. Any bug fix or security patch released upstream in that window — including in `golang.org/x/sys`, which mediates raw OS syscalls used for file and permission handling — is simply absent from every lnpm binary shipped today. The longer the gap grows, the larger and riskier the eventual catch-up upgrade becomes, since more breaking changes accumulate between the pinned version and latest.

## Where to look

- `go.mod:5-18` — the two `require` blocks pinning every direct and indirect dependency to the outdated versions listed above.
- `.github/` — no `dependabot.yml` or equivalent exists anywhere in the repo, confirming there is no automated update mechanism today.

## How to fix

1. Run `go get -u ./...` to bump direct and indirect dependencies to their latest compatible versions.
2. Run `go mod tidy` to clean up `go.mod`/`go.sum`.
3. Run the full test suite locally: `go test -race ./...`.
4. Push a branch and confirm the CI Test, Test (Windows), Lint, and Build jobs in `.github/workflows/ci.yaml` all still pass with the updated dependencies.
5. Add `.github/dependabot.yml` configuring the `gomod` package ecosystem with a weekly update schedule, so future drift is caught automatically via PRs instead of manual audits.

## Acceptance criteria

- [ ] `go.mod`/`go.sum` are updated so `go list -m -u all` shows no pending updates for direct dependencies.
- [ ] `.github/dependabot.yml` exists, targets the `gomod` ecosystem, and runs on a weekly schedule.
- [ ] `go test -race ./...` passes after the bump.
- [ ] CI (Linux, Windows, Lint, Build) passes on a PR containing the dependency bump.
- [ ] Core commands (`publish`, `add`, `push`, `status`) still behave correctly in a manual smoke test.

## Testing

- Run `go get -u ./... && go mod tidy` locally, then `go build ./...` and `go test -race ./...`.
- Push a branch and confirm every CI job in `.github/workflows/ci.yaml` passes.
- After merging the Dependabot config, confirm GitHub recognizes it (a "Dependency graph" / Dependabot tab entry appears, or the first scheduled PR opens as expected).
