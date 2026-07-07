TITLE: Make publish --push fail when every linked-project push fails
LABELS: bug
---
## Severity

Medium — `lnpm publish --push` reports success (exit code 0) even when it fails to update a single one of the projects it was supposed to update, which breaks CI pipelines and scripts that rely on the exit code.

## Background

`lnpm publish --push` does two things in one command: it publishes the current package to the content-addressed store, then immediately pushes the update out to every project that has previously linked it. The push-to-many-projects logic already exists as a standalone command, `lnpm push`, which publishes and pushes in the same way and is implemented with the correct exit-code behavior — it returns an error if any linked project failed to update. `publish --push` reuses the same per-project push logic (`pushToProject`) but has its own separate loop for iterating over projects and reporting results.

## Problem

`pushToLinkedProjects` (used by `publish --push`) runs the push to each linked project in parallel, collects each result, and prints a `✓`/`✗` line per project — but the `successCount` variable it maintains is never checked. The function always executes `return nil` at the end, regardless of how many (or how few) of the pushes actually succeeded. This is inconsistent with `lnpm push`'s own `pushToAllProjects`, which performs the identical count-and-compare and returns a real error when `successCount < len(projects)`.

Concrete failure scenario: a package is linked into three consumer projects. One of those projects has since been deleted from disk, or its `.lnpm/` directory has been manually removed. The developer runs `lnpm publish --push`. The publish itself succeeds, and the push step correctly prints two `✓` lines and one `✗` line for the broken project — but `pushToLinkedProjects` still returns `nil`, so `RunPublish` returns `nil`, and the command exits `0`. A CI job or deploy script that runs `lnpm publish --push && deploy.sh` proceeds as if every consumer were successfully updated, when one was silently left stale.

## Where to look

- `internal/cli/publish.go:291-299` — the results loop in `pushToLinkedProjects`: `successCount` is incremented on each success (line 297) but never compared against `len(projects)`.
- `internal/cli/publish.go:301` — `return nil`, unconditional, regardless of `successCount`.
- `internal/cli/publish.go:246-249` — `RunPublish` calls `pushToLinkedProjects` and already propagates its error correctly (`if err := pushToLinkedProjects(...); err != nil { return err }`); the only missing piece is that `pushToLinkedProjects` never produces a non-nil error for partial failure.
- `internal/cli/push.go:191-193` — `pushToAllProjects`'s equivalent check, to mirror exactly: `if successCount < len(projects) { return fmt.Errorf("push failed for %d of %d project(s)", len(projects)-successCount, len(projects)) }`.
- `internal/cli/push.go:189` — the accompanying summary line `fmt.Printf("\nPushed to %d/%d projects\n", successCount, len(projects))`, also absent from `pushToLinkedProjects` and worth adding for consistency of output between the two commands.

## How to fix

1. In `internal/cli/publish.go`'s `pushToLinkedProjects`, after the results loop (after line 299, before the current `return nil` at line 301), add the same summary line used by `push.go:189`: `fmt.Printf("\nPushed to %d/%d projects\n", successCount, len(projects))`.
2. Replace the unconditional `return nil` with the same check used in `internal/cli/push.go:191-193`: `if successCount < len(projects) { return fmt.Errorf("push failed for %d of %d project(s)", len(projects)-successCount, len(projects)) }`, followed by `return nil` for the all-succeeded case.
3. No changes are needed in `RunPublish` — it already returns whatever error `pushToLinkedProjects` produces.

## Acceptance criteria

- [ ] `lnpm publish --push` exits non-zero when at least one linked project fails to push, with an error message stating how many of how many projects failed.
- [ ] `lnpm publish --push` exits zero when all linked projects push successfully (unchanged from current behavior).
- [ ] `lnpm publish --push` with zero linked projects still exits zero (the existing `len(projects) == 0` early return at `publish.go:262-265` is unaffected).
- [ ] The success/failure exit-code behavior of `publish --push` now matches `lnpm push`'s behavior exactly.

## Testing

Add an integration test to `tests/publish_test.go` using the `setupTest` helper (see `tests/helpers_test.go`). No existing test forces a single-project push failure, so drive it through `pushToProject`'s one failure path — `linker.Link` fails when it cannot create files under the target project's `.lnpm/<pkg>` directory.

Test outline:
1. `env := setupTest(t)`; `pkgDir := env.simplePkg("push-fail-pkg")`.
2. Create two consumer projects and add the package to both: `projA := env.newProject("project-a")`, `projB := env.newProject("project-b")`, `env.addPkg(projA, "push-fail-pkg", false, false)`, `env.addPkg(projB, "push-fail-pkg", false, false)`.
3. Break `projB` so `linker.Link` fails for it during the push: replace `projB`'s `.lnpm` directory with a regular (non-directory) file at the same path, e.g. `os.RemoveAll(filepath.Join(projB, ".lnpm"))` then `os.WriteFile(filepath.Join(projB, ".lnpm"), []byte("blocked"), 0644)`, so any attempt to create `.lnpm/push-fail-pkg` inside it fails.
4. Modify the package source (e.g. edit a file under `pkgDir` via `env.writeFile`) and republish with push from the package directory: `env.chdir(pkgDir)`, then `err := cli.RunPublish(true, false, true, true)` (push=true, skip hooks/validation to keep the test focused).
5. Assert `err` is non-nil and its message mentions the failed project count (e.g. `strings.Contains(err.Error(), "push failed for 1 of 2")`).
6. Assert the healthy project (`projA`) still received the update, e.g. via `env.AssertLinkedFileContent` on the changed file, even though the overall command reported failure.

Run:

```
go test ./tests/ -run TestPublish -v
```
