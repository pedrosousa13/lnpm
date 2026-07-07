TITLE: Stop concurrent lnpm invocations from failing with a cryptic database timeout
LABELS: bug
---
## Severity

Medium — parallel lnpm usage (for example under a monorepo task runner) crashes with an unhelpful error instead of working or failing clearly.

## Background

lnpm stores all package/link metadata in a single bbolt database file at `<store>/lnpm.db`. bbolt uses an OS file lock (`flock`) to guarantee only one process has the database open at a time — this is the only thing serializing concurrent lnpm processes against the shared store, and it must stay in place. `internal/db.GetDB()` opens the database once per process (a `sync.Once` singleton) and keeps it open for the process's entire lifetime; it is never explicitly closed.

## Problem

`bolt.Open` is called with `Timeout: 1 * time.Second`. If another lnpm process already holds the lock (for example a long-running `publish` or `push`), any second process trying to open the database waits at most one second, then fails outright with `failed to open database: timeout`. This is common when a task runner like turbo or npm workspaces runs `lnpm push` or `lnpm add` in parallel across multiple packages — the first invocation to grab the lock wins, and every other concurrent invocation dies within a second.

The underlying serialization (the flock) is correct and necessary; the problem is purely the short timeout and the unhelpful error message when it's hit.

## Where to look

- `internal/db/db.go:116` — `bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})`.
- `internal/db/db.go:100-118` — `initDB`, called once via `sync.Once` from `GetDB` (around `internal/db/db.go:90-97`).

## How to fix

1. Increase the `bolt.Options.Timeout` substantially (e.g. 30-60 seconds) so that ordinary sequential operations from other lnpm processes have time to finish, without changing the fact that the flock still fully serializes access.
2. When `bolt.Open` still returns a timeout error after waiting, wrap it with a clearer message, e.g. `fmt.Errorf("another lnpm process appears to be running (database is locked): %w", err)`, so users understand what happened instead of seeing a bare "failed to open database: timeout".
3. Do not remove or weaken the flock itself — it is the only cross-process guard protecting the store and database from concurrent corruption.

## Acceptance criteria

- [ ] Two lnpm processes started a few seconds apart against the same store both succeed (the second waits for the first rather than failing after 1s).
- [ ] A process that genuinely cannot acquire the lock within the (now longer) timeout gets a message identifying that another lnpm process is likely running, not a bare "timeout" error.
- [ ] No change to single-process behavior or to the underlying flock semantics.

## Testing

Add a test in `internal/db/db_test.go` (or extend the existing db tests) that holds the bbolt database open in one goroutine/process-like handle, attempts to open it again with a short simulated timeout, and asserts the returned error message mentions another lnpm process. Since the production timeout will be long, consider making the timeout a package-level var or parameter that the test can override to keep the test fast.

```
go test ./internal/db/...
go test ./...
```
