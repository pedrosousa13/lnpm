TITLE: Commit a golangci-lint config and pin the linter version
LABELS: ci
---
## Severity

Low — lint currently catches nothing beyond a PR's own new lines, and its exact rule set can change out from under the project without warning.

## Background

`golangci-lint` is a meta-linter that runs many individual Go static-analysis tools (`govet`, `staticcheck`, `errcheck`, `unused`, etc.) together under one config. Which linters are enabled by default, and their exact behavior, can change between `golangci-lint` releases. A `.golangci.yml` file at the repo root lets a project pin which linters run and how, independent of upstream defaults. This repo has no such file anywhere, and CI installs `version: latest` for the linter action, plus sets `only-new-issues: true` on the one place it runs with that flag.

## Problem

Two compounding issues:

1. `only-new-issues: true` (CI Lint job) means golangci-lint only fails a PR for problems introduced by that PR's diff — any pre-existing issue in the codebase is permanently invisible to CI and will never block a merge, even if someone touches the surrounding code.
2. With no committed `.golangci.yml` and `version: latest` in both `.github/workflows/ci.yaml` and `.github/workflows/release-please.yaml`, the exact linters that run are decided by whatever golangci-lint version happens to be current at the moment a workflow executes. If upstream enables a new default linter, a PR that changed nothing lint-relevant can suddenly start failing (or, conversely, a linter that used to be enabled could be dropped, silently reducing coverage) — with no corresponding change in this repository to explain it.

## Where to look

- `.github/workflows/ci.yaml:70-74` — Lint job step: `version: latest` and `only-new-issues: true`, no config file referenced.
- `.github/workflows/release-please.yaml:44-47` — the release job's own golangci-lint step, also on `version: latest`, no config file.
- Repo root — no `.golangci.yml` (or `.golangci.yaml`) exists.

## How to fix

1. Run `golangci-lint run ./...` locally against the current codebase to see what a real config would surface.
2. Commit a `.golangci.yml` at the repo root enabling a sensible baseline set of linters (e.g. `govet`, `staticcheck`, `errcheck`, `ineffassign`, `revive`, `unused`).
3. Fix any findings that surface under that config, or explicitly exclude/suppress specific unfixable ones with a comment explaining why.
4. Pin the golangci-lint version in both `.github/workflows/ci.yaml:73` and `.github/workflows/release-please.yaml:47` to a specific version instead of `latest`.
5. Once the codebase is clean against the pinned config, remove `only-new-issues: true` from `.github/workflows/ci.yaml:74` so the full ruleset gates every PR, not just its new lines.

## Acceptance criteria

- [ ] `.golangci.yml` is committed at the repo root with an explicit linter set.
- [ ] golangci-lint version is pinned (not `latest`) in both `ci.yaml` and `release-please.yaml`.
- [ ] `golangci-lint run ./...` passes cleanly against the pinned config and the current codebase.
- [ ] `only-new-issues: true` is removed from the CI Lint job (or kept with an explicit justification recorded).

## Testing

- Run `golangci-lint run ./...` locally with the new config; it must exit 0.
- Push a branch and confirm the Lint job in `.github/workflows/ci.yaml` passes.
- Temporarily introduce a deliberate lint violation (e.g. an unused variable) in a scratch commit and confirm the pinned config catches it, then revert — this confirms the gate is actually strict rather than just present.
