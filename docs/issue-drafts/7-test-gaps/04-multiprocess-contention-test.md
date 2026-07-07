TITLE: Add a real multi-process store-contention e2e test
LABELS: tests
---
## Severity

Medium. Two lnpm processes touching the shared store at once is a completely normal workflow (e.g. `lnpm push` in one terminal while `lnpm add` runs in another), and today no test anywhere exercises it — the behavior under lock contention is unknown.

## Background

All lnpm state lives in a single bbolt database inside the store directory. bbolt takes an exclusive file lock on open, and lnpm opens it with only a 1-second lock timeout (`bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})` at `internal/db/db.go:116`). If a second process cannot acquire the lock within that second, `bolt.Open` fails and the command errors out.

The in-process integration suite (`tests/`) cannot test this: it calls `cli.RunX` functions inside one process that shares one database handle, and its concurrency tests are permanently skipped because the whole suite drives working directories through `os.Chdir`, which is process-global and not goroutine-safe. The e2e suite (`tests/e2e/`) runs the real compiled binary, but it deliberately gives every test its own private store precisely to avoid lock contention between parallel tests — so contention is never exercised there either.

## Problem

Nothing verifies what happens when two real lnpm processes contend for the store lock:

- Do sequential-but-overlapping commands both succeed (one waits up to 1s and wins the lock)?
- When the timeout is exceeded, does the loser fail with a clear, actionable error — or a raw bbolt timeout message, or worse, corrupted/partial state?

Additionally, the two in-process concurrency tests are dead weight: `TestAddConcurrentSameProject` (`tests/add_test.go:154`) is skipped with "concurrent package.json writes cause race conditions, not realistic usage" and `TestAddConcurrentDifferentProjects` (`tests/add_test.go:177`) is skipped with "os.Chdir is not goroutine-safe, test creates artificial race condition". The second one describes a real scenario (different projects adding the same package concurrently) that is only untestable because the `RunX` functions take no working-directory parameter and rely on `os.Getwd()`.

## Where to look

- `internal/db/db.go:116` — the bbolt open with the 1s lock `Timeout` (inside the code path under `GetDB`, `internal/db/db.go:88`).
- `tests/add_test.go:154-155` and `tests/add_test.go:177-178` — the two permanently skipped in-process concurrency tests.
- `tests/e2e/helpers_test.go:14-24` — `newStore` and its comment explaining the per-test store exists exactly because of the exclusive lock and 1s timeout.
- `tests/e2e/helpers_test.go:32` — `runLNPM`: runs the built binary with `cmd.Dir` and env `LNPM_STORE`/`LNPM_CONFIG`; note it calls `t.Fatalf` on any non-zero exit, so the contention test needs a variant that returns the error instead.
- `tests/e2e/main_test.go:36` — `TestMain`, which builds the binary once; new e2e tests get `lnpmBin` for free.
- `tests/e2e/depth_test.go:98` — `TestMultiConsumerPushFanout`, a good structural example of an e2e test creating packages/projects with `writeFile` and driving them with `runLNPM`.

## How to fix

1. In `tests/e2e/helpers_test.go`, add a non-fatal runner, e.g. `runLNPMErr(t, store, dir string, args ...string) (string, error)` — identical to `runLNPM` but returning the combined output and error instead of failing the test.
2. Add `tests/e2e/contention_test.go` with `TestConcurrentProcessesSharedStore`:
   - Create ONE shared store (`newStore(t)`), deliberately shared — add a comment explaining this test is the exception to the per-test-store rule.
   - Create two independent package dirs (repo A and repo B) with minimal `package.json` + `index.js`, plus one consumer project. Do NOT mark the test `t.Parallel()` (it must not fight other tests for CPU-timing determinism, and it owns its store anyway).
   - Warm up: publish repo A once so the consumer can add it; `runLNPM(... "add", "pkg-a")` in the consumer.
   - Contention phase: in two goroutines, simultaneously run real processes against the shared store — e.g. goroutine 1 loops `lnpm publish` in repo A (or `lnpm push`) a few times while goroutine 2 loops `lnpm add pkg-b`/`lnpm publish` in repo B — using `runLNPMErr`. Synchronize the start with a channel/`sync.WaitGroup` so the processes genuinely overlap.
   - Assert the outcome: every invocation either succeeds, or fails with an error output that clearly indicates lock contention (assert the output mentions the database/store being locked or in use — pin whatever the binary actually prints today, e.g. bbolt's "timeout" text). Any other failure mode fails the test.
   - Afterward, run one final `runLNPM(... "status")` (or re-`add`) to assert the store is still healthy after contention — no corrupted database.
3. If the current loser-error turns out to be a raw `timeout` from bbolt with no guidance, keep the test pinning that output and file the UX improvement separately — this issue only adds the test.
4. **Longer-term (optional follow-up, not required to close this issue):** thread an explicit working-directory parameter through the `cli.RunX` functions (defaulting to `os.Getwd()`), which would remove the `os.Chdir` limitation and allow un-skipping `TestAddConcurrentDifferentProjects` at `tests/add_test.go:177`. If you do this, un-skip that test; otherwise update its skip message to point at this e2e test as the current coverage for concurrent use.

## Acceptance criteria

- [ ] A new e2e test launches at least two concurrent real `lnpm` processes against one shared store.
- [ ] The test asserts every process either succeeds or fails with an output that clearly indicates lock contention — silent corruption or unexpected errors fail the test.
- [ ] The test verifies the store/database remains usable after the contention phase.
- [ ] The test passes reliably (run it 10x locally; no flakes) on Linux, macOS, and Windows, and under `-race`.
- [ ] The skipped in-process tests at `tests/add_test.go:154` and `:177` are either un-skipped (if the working-dir refactor is done) or their skip messages reference the new e2e coverage.

## Testing

```
go test ./tests/e2e/ -run TestConcurrentProcessesSharedStore -v
go test ./tests/e2e/ -run TestConcurrentProcessesSharedStore -count=10   # flake check
go test -race ./tests/e2e/
```

Note: the e2e suite builds the binary in `TestMain` and skips node-dependent tests when node is absent; this test needs no node, so it should run everywhere Go runs.
