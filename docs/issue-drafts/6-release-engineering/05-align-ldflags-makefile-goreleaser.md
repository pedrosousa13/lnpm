TITLE: Align build-time ldflags between Makefile and GoReleaser
LABELS: ci
---
## Severity

Low — no functional breakage, but dev and release builds report differently-shaped version strings, and two build-time variables are wired in and then thrown away.

## Background

Go binaries can have variables set at build time via `-ldflags -X pkg.Var=value`, without changing source code. Both this project's `Makefile` and its GoReleaser config (`.goreleaser.yaml`, the tool that builds release binaries) use this to inject a version string into `main.Version` at `cmd/lnpm/main.go:11`, which the CLI then prints via `lnpm --version`. `main.go` additionally declares `Commit` and `Date` variables (lines 12-13) meant to be injected the same way.

## Problem

The two build paths inject different things in different shapes:

- `Makefile:4-5` sets `VERSION` from `git describe --tags --always --dirty` (e.g. `v1.11.0-2-gabc1234-dirty`, v-prefixed, including commit/dirty suffix) and injects only `main.Version`.
- `.goreleaser.yaml:23-27` injects three variables from GoReleaser's own template context: `main.Version` (`{{.Version}}`, no `v` prefix), `main.Commit`, and `main.Date`.

So a developer running `make build` and a real release binary produce `--version` output in two different shapes for what should be the same concept, and any tooling or script parsing that output has to handle both. Meanwhile `Commit` and `Date` are populated in release binaries but never read anywhere — `internal/cli/root.go:20` (and the equivalent template set in `init()` at line 67) only ever prints `Version` — so the values are computed and injected for no observable benefit, and a support conversation about "which commit is this binary from" has no way to be answered from `--version` output alone.

## Where to look

- `Makefile:4-5` — `VERSION` derivation and `LDFLAGS`, injecting only `main.Version`.
- `.goreleaser.yaml:23-27` — `ldflags:` injecting `main.Version`, `main.Commit`, `main.Date`.
- `cmd/lnpm/main.go:10-14` — declaration of `Version`, `Commit`, `Date`.
- `internal/cli/root.go:20` — `SetVersionTemplate` call that only formats `Version`.

## How to fix

1. Decide the canonical version format (with or without the `v` prefix) and make both build paths produce it consistently.
2. Update `Makefile:4-5` to also derive and inject `Commit` (e.g. `git rev-parse --short HEAD`) and `Date` (e.g. `date -u +%Y-%m-%dT%H:%M:%SZ`), matching the variables GoReleaser already injects.
3. Either surface `Commit`/`Date` in `--version` output by updating the version template in `internal/cli/root.go` (both the `SetVersion` function and the `init()` call) to include them, or, if they add no value, remove the unused `Commit`/`Date` vars from `cmd/lnpm/main.go` and their ldflags from `.goreleaser.yaml` and the updated `Makefile`.
4. Verify with a local build (`make build`) and a GoReleaser snapshot build (`goreleaser release --snapshot --clean`) that `--version` output is consistent in shape between the two.

## Acceptance criteria

- [ ] `Makefile` and `.goreleaser.yaml` inject the same set of build-time variables, in a consistent version format.
- [ ] `lnpm --version` output shape matches between a `make build` binary and a `goreleaser` snapshot binary (aside from the actual version number differing).
- [ ] `Commit` and `Date` are either shown in `--version` output or removed entirely, with no dead injected-but-unused variables remaining.

## Testing

- Run `make build && ./lnpm --version` and inspect the output.
- Run `goreleaser release --snapshot --clean`, then run one of the produced binaries with `--version`.
- Compare the two outputs for consistent shape/format.
