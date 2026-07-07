TITLE: Make lnpm update report network failures instead of "Already up to date"
LABELS: bug
---
## Severity

Medium — an explicit update attempt that fails to reach GitHub reports success and does nothing, so users believe they are current when the check never happened.

## Background

`lnpm update` performs a "fresh" check: it calls the GitHub API to find the latest release, then compares it to the current version. The HTTP request that talks to GitHub is bounded by a timeout. The background check that runs on ordinary commands is intentionally allowed to fail silently (you don't want a flaky network to spam errors on every command). But the *explicit* `lnpm update` command is currently wired the same way, so its failures are also swallowed.

## Problem

Two design choices combine into a silent failure:

1. The request timeout is 500 milliseconds (`requestTimeout = 500 * time.Millisecond`). That is far too tight for the explicit path — a slow connection, a captive-portal Wi-Fi, or a momentary GitHub API hiccup will exceed it routinely.
2. When the fetch fails, `CheckFresh` returns `nil` (no result, no error). The caller chain then treats `nil` as "no update available" and prints "Already up to date" with exit code 0.

So on any network problem, `lnpm update` prints "Already up to date" and exits successfully — even though it never learned what the latest version is. A user on an outdated (possibly vulnerable) version runs the update command, sees a reassuring success message, and stays outdated.

Concrete failure: user is on hostile Wi-Fi that drops the API request; runs `lnpm update`; expected outcome is a clear "failed to check for updates" error with a non-zero exit; actual outcome is "Already up to date", exit 0.

## Where to look

- `internal/update/update.go:20` — `requestTimeout = 500 * time.Millisecond` (shared by both the background and fresh paths).
- `internal/update/update.go:41-63` — `CheckFresh`. Lines 49-50 build the 500ms-bounded context; lines 52-56 return `nil` on any fetch error.
- `internal/cli/update.go:92-97` — `getLatestVersion` calls `CheckFresh` and always returns `(result, nil)`, so the error return of the caller is never populated.
- `internal/cli/update.go:58-66` — `RunUpdate` has an `if err != nil` branch (lines 59-61) that is currently dead because `getLatestVersion` never returns an error; then `if result == nil` (lines 63-66) prints "Already up to date".

## How to fix

1. Give `CheckFresh` a generous timeout for the explicit path. Do not reuse the 500ms constant. Either add a separate constant (e.g. `freshRequestTimeout = 15 * time.Second`) and use it in `CheckFresh`, or add a `context.Context`/timeout parameter. A value in the 10–30s range is appropriate for an interactive command the user is waiting on.
2. Make `CheckFresh` distinguish "up to date" from "couldn't check". Change its signature to `func CheckFresh(currentVersion string) (*Result, error)` and return the fetch error instead of swallowing it. Keep returning `(nil, nil)` only for the genuine dev-build skip on lines 42-44.
3. Update `getLatestVersion` (`internal/cli/update.go:92-97`) to propagate that error: `return update.CheckFresh(currentVersion)`.
4. `RunUpdate`'s existing `if err != nil { return fmt.Errorf("failed to check for updates: %w", err) }` branch (lines 59-61) then does the right thing and produces a non-zero exit. Confirm the `result == nil` branch on lines 63-66 now only triggers for the dev-build case (which is already guarded earlier on lines 51-53, so in practice it becomes unreachable — you may keep it as a defensive default or remove it).
5. Leave the background path (`CheckAsync` / `check`) and its 500ms timeout untouched — silent failure is correct there.

## Acceptance criteria

- [ ] When the GitHub fetch fails during `lnpm update`, the command exits non-zero and prints an error containing "failed to check for updates".
- [ ] The explicit update path uses a timeout of at least 10 seconds.
- [ ] The background check still fails silently and still uses its short timeout.
- [ ] A genuine "already up to date" (fetch succeeded, no newer version) still prints "Already up to date" and exits 0.

## Testing

From the repository root:

```
go build ./...
go test ./internal/update/... ./internal/cli/...
```

`CheckFresh` talks to a hard-coded `api.github.com` URL, which makes it awkward to unit-test directly. Add at minimum a test that `CheckFresh("dev")` returns `(nil, nil)` (the skip case). For the error-propagation behavior, the cleanest fix is to also extract the URL into a package variable (or accept a base URL) so a test can point it at an `httptest.Server` that returns an error/timeout and assert a non-nil error is returned; add this test to a new `internal/update/update_test.go`. Manually verify by running `lnpm update` with networking disabled and confirming a non-zero exit.
