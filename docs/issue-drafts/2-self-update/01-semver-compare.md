TITLE: Compare versions with a semver library instead of text ordering
LABELS: bug, security
---
## Severity

High — this defect is live in released versions today and causes users to silently miss updates, including security fixes.

## Background

`lnpm` decides whether a newer release exists by comparing the user's current version string against the latest version reported by the GitHub API. Versions follow "semantic versioning" (semver): a `MAJOR.MINOR.PATCH` scheme like `1.9.0` or `1.11.0`, where each of the three numbers is compared numerically, not alphabetically. The comparison currently uses Go's built-in string comparison operator (`>`), which compares text byte by byte rather than numerically.

## Problem

Go's `>` operator on strings compares character by character. It stops at the first differing character and compares those two characters by their byte value. For version strings this gives the wrong answer whenever the numbers have different digit counts:

- Comparing `"1.11.0"` (latest) against `"1.9.0"` (current): the first two characters `1.` are equal; the third characters are `1` (from `1.11.0`) versus `9` (from `1.9.0`). The byte value of `'1'` is less than `'9'`, so the expression `"1.11.0" > "1.9.0"` evaluates to **false**. The tool concludes no update is available.
- The repository has published both `v1.9.0` and `v1.11.0` (plus `v1.10.0`). So a real user on `v1.9.0` running `lnpm update` is told "Already up to date" and never receives `v1.10.x` or `v1.11.x`.
- The same bug breaks `1.2.9` → `1.2.10` (again `'9' > '1'`, so the newer `.10` looks older) and mishandles pre-release suffixes.

Concrete failure: user has `v1.9.0`, latest is `v1.11.0`, expected outcome is "update available", actual outcome is "Already up to date" with exit code 0.

## Where to look

- `internal/update/update.go:187` — `compareVersions(current, latest string)` function.
- `internal/update/update.go:189-190` — strips the leading `v` from each version.
- `internal/update/update.go:200` — the faulty line: `if latestNorm != currentNorm && latestNorm > currentNorm {`. This is the byte-wise string comparison.
- `internal/update/update.go:198-199` — an existing comment already admits "Simple string comparison works for semver if same length ... For proper comparison we'd need a semver library".

## How to fix

1. Add the dependency `golang.org/x/mod/semver` (run `go get golang.org/x/mod/semver`). It is the standard library-adjacent module the Go toolchain itself uses for semver comparison.
2. `semver.Compare` requires each input to have a leading `v` (e.g. `v1.9.0`) and returns `-1`, `0`, or `+1`. In `compareVersions`, keep the raw `current` and `latest` strings for display, but build canonical inputs for the comparison. Since the code already strips the `v` prefix into `currentNorm`/`latestNorm`, the simplest fix is to compare with the prefix re-added:
   ```go
   updateAvailable := semver.Compare("v"+latestNorm, "v"+currentNorm) > 0
   ```
   Set `result.UpdateAvailable = updateAvailable`.
3. Remove the now-inaccurate comment on lines 198-199.
4. Note: `semver.Compare` returns `0` if either input is not valid semver, which is a safe default (it will not claim a spurious update). No extra guarding is required, but keep the existing behavior where `current == "dev"` is handled earlier (in `CheckFresh` and `CheckAsync`) so it never reaches this function.

## Acceptance criteria

- [ ] `compareVersions("1.9.0", "1.11.0")` reports an update is available.
- [ ] `compareVersions("1.2.9", "1.2.10")` reports an update is available.
- [ ] `compareVersions("1.11.0", "1.11.0")` reports no update.
- [ ] `compareVersions("1.11.0", "1.9.0")` (current newer than latest) reports no update.
- [ ] Leading `v` on either input is handled (`v1.9.0` and `1.9.0` behave identically).
- [ ] `golang.org/x/mod` appears in `go.mod` and `go.sum`.

## Testing

From the repository root:

```
go get golang.org/x/mod/semver
go build ./...
go test ./internal/update/...
```

There is currently no test file for this package. Add `internal/update/update_test.go` with a table-driven `TestCompareVersions` covering: `1.9.0`→`1.11.0` (update), `1.2.9`→`1.2.10` (update), equal versions (no update), current-newer-than-latest (no update), and `v`-prefixed vs. bare inputs. Follow the table-driven style already used in `internal/cli/update_test.go`.
