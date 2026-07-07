TITLE: Fix three silently-weakened tests (npm-registry dependency, blanket CI skip, e2e node skip)
LABELS: tests, ci
---
## Severity

Low. No production code is wrong, but three existing tests are quietly weaker than they appear: two can go green without checking their invariant, and one whole suite can silently skip itself in CI.

## Background

Three unrelated hygiene problems share a theme — a test that looks like coverage but can silently stop covering:

1. `TestSymlinkSurvivesNpmInstall` verifies the single most important compatibility invariant lnpm has: running `npm install` in a project does not destroy the `node_modules/<pkg>` symlink lnpm created. To trigger a real install it adds `is-odd` as a dependency and runs `npm install --prefer-offline`, which hits the public npm registry.
2. `TestPublishReadOnlyFiles` verifies that publishing a package containing a read-only (0444) file succeeds — but it is skipped entirely whenever `CI=true`.
3. The e2e suite (`tests/e2e/`) exercises the real compiled binary against real node resolution. Its `TestMain` probes for `node` on `PATH`; when absent, it sets a flag and every test skips.

## Problem

- **(a) Registry-dependent, log-only assertion.** At `tests/symlink_test.go:37-44`, the `npm install` error is only `t.Logf`'d, with a comment saying environmental failures shouldn't fail the test. Consequence: when the registry is flaky, offline, or `is-odd` is unavailable, npm does nothing meaningful, the subsequent symlink assertions pass trivially (the symlink was never threatened), and the invariant "npm install preserves lnpm symlinks" goes unchecked — silently, forever, on any machine without registry access.
- **(b) Blanket CI skip.** At `tests/publish_test.go:214-216`, `TestPublishReadOnlyFiles` skips whenever `CI=true`, so the read-only-file publish path is never verified in CI on any OS. The skip predates a diagnosis: the likely real cause is the test being invalid only under specific conditions (e.g. a root user can open 0444 files for writing, or Windows read-only semantics), not "CI" as such. GitHub Actions Linux runners are not root, so the blanket skip is almost certainly wider than necessary.
- **(c) Whole-suite silent skip.** At `tests/e2e/main_test.go:37-43`, if `node` is not on `PATH`, `TestMain` prints a note and every test skips (each test checks the `nodeAvailable` flag, e.g. `tests/e2e/monorepo_test.go:32-33`). Locally that is the right behavior; in CI it is a trap: if the workflow's node setup breaks, the entire e2e suite turns green-by-skipping and nobody notices.

## Where to look

- `tests/symlink_test.go:16` — `TestSymlinkSurvivesNpmInstall`; the registry install and log-only error handling at lines 26–44.
- `tests/publish_test.go:213-216` — `TestPublishReadOnlyFiles` and its `CI=true` skip; the 0444 file at line 222.
- `tests/e2e/main_test.go:36-43` — `TestMain`'s node probe and `nodeAvailable` flag (declared at `tests/e2e/main_test.go:27-30`).
- `tests/e2e/monorepo_test.go:32-33` — example of the per-test `nodeAvailable` skip the flag feeds.
- `tests/helpers_test.go:72` — `CopyFixture` and the `fixtures/` directory convention, for where to put the vendored tarball in fix (a).

## How to fix

1. **(a) Vendor a tarball and make the install fatal.**
   - Create a tiny throwaway package (name like `lnpm-test-dep`, one `index.js`), run `npm pack` on it once, and commit the resulting `.tgz` under `tests/fixtures/` (e.g. `tests/fixtures/tarballs/lnpm-test-dep-1.0.0.tgz`).
   - In `TestSymlinkSurvivesNpmInstall`, replace the `is-odd` registry dependency with the local tarball: `npm install <absolute path to .tgz>` (installing a file path never touches the registry). Keep `npm_config_audit=false`/`npm_config_fund=false`; audit/fund are also network paths.
   - Change the error handling from `t.Logf` to `t.Fatalf` so a failed install fails the test.
   - Keep exactly one legitimate skip: if `npm` itself is not on `PATH` (`exec.LookPath("npm")`), `t.Skip` — mirroring how the e2e suite treats missing node locally.
2. **(b) Diagnose and narrow the read-only skip.**
   - Reproduce: temporarily remove the skip and run the test in CI (or locally as root: `sudo go test ./tests/ -run TestPublishReadOnlyFiles`). Identify the actual failure — the prime suspect is that root bypasses the 0444 permission check so an assertion about read-only behavior is meaningless, or a Windows-specific mode semantics difference.
   - Replace `if os.Getenv("CI") == "true"` with a predicate matching the real cause, e.g. skip only when `os.Geteuid() == 0` (guarded for Windows, where `Geteuid` returns -1) and/or only on the OS where it genuinely cannot pass. Include the diagnosis in the skip message.
   - If the un-skipped test simply passes in CI, delete the skip entirely — that is the ideal outcome.
3. **(c) Fail hard in CI when node is missing.**
   - In `TestMain` (`tests/e2e/main_test.go:37-43`), when `exec.LookPath("node")` fails AND `os.Getenv("CI") == "true"`, print a clear message ("e2e: node is required in CI but was not found on PATH") and `os.Exit(1)` instead of setting `nodeAvailable = false`.
   - Keep the current skip behavior when `CI` is not `true`, so local runs without node stay friendly.

## Acceptance criteria

- [ ] `TestSymlinkSurvivesNpmInstall` installs a vendored local tarball, never contacts the npm registry, and fails (not logs) when `npm install` fails; it skips only when npm is not installed.
- [ ] The `.tgz` fixture is committed and small (a few hundred bytes).
- [ ] `TestPublishReadOnlyFiles` runs in CI on at least one OS; any remaining skip is scoped to a diagnosed condition and the skip message states the reason.
- [ ] With `CI=true` and node absent, `go test ./tests/e2e/` exits non-zero from `TestMain`; without `CI`, behavior is unchanged (skips).
- [ ] Full suite passes on Linux and Windows CI with `-race`.

## Testing

```
go test ./tests/ -run TestSymlinkSurvivesNpmInstall -v          # with npm installed
go test ./tests/ -run TestPublishReadOnlyFiles -v               # locally
CI=true go test ./tests/ -run TestPublishReadOnlyFiles -v       # verify narrowed skip
CI=true PATH=/usr/bin:/bin go test ./tests/e2e/ -run TestPlainProjectResolution  # with node hidden from PATH: must FAIL, not skip
go test -race ./tests/ ./tests/e2e/
```

(For the node-hidden check, craft a PATH without node but with go and a shell; asserting the non-zero exit from `TestMain` manually once is sufficient.)
