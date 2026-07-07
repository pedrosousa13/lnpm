TITLE: Close the test-coverage gaps in the riskiest untested code paths
LABELS: epic, tests
---
## Overview

lnpm has three test layers: per-package unit tests (`internal/*/`), an in-process integration suite (`tests/*.go`, which calls `cli.RunX` functions directly after `os.Chdir`-ing into temp packages), and a real-binary e2e suite (`tests/e2e/`, which builds lnpm once in `TestMain` and runs it against real node fixtures). Together they run about 90 integration test functions on Linux and Windows with `-race` in CI.

Despite that, true whole-program coverage (`go test ./... -coverpkg=./...`) sits around 61.5%, and the uncovered 38% is not random: it clusters in a handful of specific areas. The self-update flow — the single most dangerous code path in the tool, since it downloads and replaces the running binary — is almost entirely untested. Three user-facing commands (`status`, `list`, `doctor`) are never invoked by any test. Config persistence, link failure/partial-state handling, and multi-process store contention have no coverage at all. A few existing tests are also silently weaker than they look (assertions downgraded to logs, blanket CI skips).

## Why this matters

Untested code in these areas fails in the worst possible ways. A regression in the updater can brick every user's installation on their next `lnpm update` — and nothing in CI would notice. A regression in `status`/`list`/`doctor` or config persistence ships silently because no test even calls those functions. Two lnpm processes racing on the shared store (a completely normal workflow: `lnpm push` in one terminal, `lnpm add` in another) has never been exercised, so lock-contention behavior is unknown. And the hygiene issues mean some tests that appear green are not actually checking their invariant.

Each sub-issue below is independently implementable; they are ordered by risk, highest first.

## Sub-issues

1. Add tests for the self-update flow (download, checksum, extraction, cache)
2. Add tests for the status, list, and doctor commands
3. Add tests for config subcommands and config persistence
4. Add a real multi-process store-contention e2e test
5. Add tests for link failure paths and copy fallbacks
6. Fix three silently-weakened tests (npm-registry dependency, blanket CI skip, e2e node skip)

## Definition of done

- The self-update pure functions, archive extraction (including path-traversal entries), download/checksum verification (against a local `httptest.Server`), and version-cache round-trip are all covered by unit tests.
- `cli.RunStatus`, `cli.RunList` (all flag combinations), and `cli.RunDoctor` are each invoked by at least one integration test, and `formatTimeAgo`/`truncate` have unit tests.
- `config.SaveConfig` is verified by a save→load round-trip, and the `config` subcommand get/set/invalid-key paths are tested.
- An e2e test runs two real lnpm processes concurrently against one shared store and asserts a sane outcome (both succeed, or the loser fails with a clear lock error).
- `Linker.Link` is tested with a mid-link store failure (error surfaced, retry succeeds) and the copy fallback paths are exercised.
- The three weakened tests actually enforce their invariants: symlink-survival uses a vendored tarball and fails on error, the read-only publish skip is narrowed to its real cause, and the e2e suite fails hard when `CI=true` and node is missing.
- Whole-program coverage (`go test ./... -coverpkg=./...`) measurably increases from the ~61.5% baseline.
- All tests pass on Linux and Windows with `-race`.
