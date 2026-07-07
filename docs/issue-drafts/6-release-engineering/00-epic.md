TITLE: Release engineering & CI hygiene
LABELS: epic, ci
---
## Overview

lnpm is a Go CLI distributed as prebuilt binaries. Its release and CI machinery has several pieces of drift and rot: the pinned Go toolchain is end-of-life, dependencies are years behind, the release binaries are only tested on Linux and Windows despite shipping macOS builds, and the GoReleaser config uses fields that upstream has deprecated and will eventually remove. There are also softer problems: the lint gate never blocks pre-existing issues, the integration/e2e test coverage numbers reported to Codecov are effectively empty, and build metadata (commit/date) is injected but never shown to users.

This epic groups the release-engineering and CI cleanup work into small, independently shippable fixes.

## Why this matters

Release pipelines fail silently until the moment you need them. A deprecated GoReleaser field or an EOL toolchain does not surface on a normal PR — it surfaces the day you cut a release, when it is most painful. An unsupported Go version means release binaries ship without the latest standard-library security fixes. Stale dependencies accumulate breaking-change debt and unpatched CVEs. And misleading coverage numbers erode trust in CI. Fixing these now keeps the release button reliable and the signal from CI honest.

## Sub-issues (in implementation order)

1. Upgrade the pinned Go toolchain off end-of-life 1.22
2. Add a macOS CI job to test the darwin binaries we ship
3. Fix deprecated GoReleaser fields and pin the GoReleaser version
4. Update all outdated Go dependencies and add automated dependency updates
5. Align build-time ldflags between Makefile and GoReleaser
6. Commit a golangci-lint config and pin the linter version
7. Fix the no-op e2e coverage measurement and add Node setup to the release job

## Definition of done

- [ ] All seven sub-issues are closed.
- [ ] `go.mod` targets a supported Go version and every CI/release `setup-go` step reads it from a single source.
- [ ] CI runs the full test suite (with the race detector) on Linux, Windows, and macOS.
- [ ] `goreleaser check` passes against a pinned major version of GoReleaser with no deprecation warnings.
- [ ] Dependencies are current and an automated updater (Dependabot or Renovate) is configured.
- [ ] Coverage reported to Codecov reflects the whole codebase, not just unit tests.
- [ ] A committed `.golangci.yml` defines the linter set and the linter version is pinned in CI.
