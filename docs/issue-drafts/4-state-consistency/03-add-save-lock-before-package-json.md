TITLE: Save the lock file before rewriting package.json in add
LABELS: bug
---
## Severity

Medium — a failure partway through `add` can leave `package.json` pointing at `file:.lnpm/...` with no record of the original version, so a later `remove`/`retreat` deletes the real dependency instead of restoring it.

## Background

`lnpm add` records the user's original dependency specifier (for example `^1.2.3`) so that `remove` and `retreat` can put it back later. That original version is captured while rewriting `package.json` and is stored in `lnpm.lock`. `remove`/`retreat` read `OriginalVersion` from the lock: if it is present they restore it, and if it is empty they delete the dependency from `package.json` entirely.

## Problem

In both the single-package and multi-package add paths, `package.json` is rewritten (which is where the original version is captured) **before** the lock file is saved and before the database records are written. If any step after the `package.json` rewrite fails — saving the lock, registering the project, or recording the link — the function returns early. At that point `package.json` already says `file:.lnpm/<pkg>`, but no lock entry with `OriginalVersion` exists. A subsequent `remove` or `retreat` finds an empty (or missing) `OriginalVersion` and deletes the dependency rather than restoring `^1.2.3`, permanently losing the user's specifier.

Reproduction shape: force a failure in `InsertProject`/`InsertLink` (for example, a database error) after `updatePackageJSON` has run. `package.json` is left with the lnpm reference and no lock record captures the original version.

## Where to look

Single-package path (`runAddSingle`):
- `internal/cli/add.go:327` — `originalVersion, err = updatePackageJSON(...)` rewrites `package.json` first
- `internal/cli/add.go:346` — `lock.Save(cwd)` happens after
- `internal/cli/add.go:356` — `database.InsertProject(proj)` returns early on failure
- `internal/cli/add.go:371` — `database.InsertLink(dbLink)` returns early on failure

Multi-package path (`RunAddMultiple`):
- `internal/cli/add.go:155-164` — `package.json` updated for each package
- `internal/cli/add.go:172-174` — `database.InsertProject` returns early on failure, before the lock is saved
- `internal/cli/add.go:213` — `lock.Save(cwd)` happens only after the database work

## How to fix

Pick one strategy and apply it to both paths:

1. **Save the lock before touching `package.json` (recommended).** Compute the original version and persist it in the lock file first, then rewrite `package.json`. Because the original version must be read out of the current `package.json` (that is what `updatePackageJSON` returns), this likely means splitting `updatePackageJSON` into "read current version" and "write lnpm reference" so the read result can be saved to the lock before the write. Order: read original version → `lock.Add(...)` + `lock.Save` → write `package.json` → database records. That way a downstream failure still leaves a lock entry that can restore the original version.
2. **Roll back `package.json` on downstream failure.** Keep the current order, but if `lock.Save`, `InsertProject`, or `InsertLink` fails, restore the original `package.json` content before returning (write the captured original version back, using the same shape as `restorePackageJSON` in `internal/cli/remove.go:136`).

Whichever strategy is chosen, apply it consistently to both `runAddSingle` and `RunAddMultiple`, and add a comment explaining the ordering constraint so it is not accidentally reordered later.

## Acceptance criteria

- [ ] After a simulated failure of a step following the `package.json` rewrite, either (a) the lock file already contains an entry with the correct `OriginalVersion`, or (b) `package.json` has been rolled back to its original specifier — depending on the chosen strategy.
- [ ] A later `remove`/`retreat` restores the original version rather than deleting the dependency.
- [ ] Both the single-package and multi-package paths use the same strategy.
- [ ] The happy path (no failure) is unchanged: `package.json` ends with `file:.lnpm/<pkg>` and the lock records the original version.

## Testing

Add an integration test to `tests/original_version_test.go` using the `setupTest` helper (see `tests/helpers_test.go`).

Because injecting a failure into `InsertProject`/`InsertLink` requires a hook, the most practical test verifies the invariant directly: after `add`, the lock file must hold the original version, and the ordering fix must guarantee it is saved no later than the `package.json` rewrite.

Test outline:
1. `te := setupTest(t)`; publish `dep` with `te.simplePkg("dep")`.
2. `projectDir := te.newProject("consumer")`, then write a `package.json` that already depends on `dep` at `^1.0.0` via `te.writePackageJSON(projectDir, map[string]interface{}{"name": "consumer", "version": "1.0.0", "dependencies": map[string]interface{}{"dep": "^1.0.0"}})`.
3. `te.addPkg(projectDir, "dep", false, false)`.
4. Assert the lock records the original version:
   `te.AssertLockfile(projectDir, func(l *lockfile.LockFile){ pkg, _ := l.Get("dep"); if pkg.OriginalVersion != "^1.0.0" { t.Errorf("expected ^1.0.0, got %q", pkg.OriginalVersion) } })`.
5. Then run `cli.RunRemove("dep", false)` and assert `te.AssertPackageJSON(projectDir, "dep", "^1.0.0")` — the original spec is restored, not deleted.

If you also want to exercise the failure path directly, factor the ordering so the lock is saved before `package.json` is written and add a test that removes write permission on `package.json` (or points it at a read-only file) to force the `updatePackageJSON` step to fail, then assert the lock still contains the original version.

Run:

```
go test ./tests/ -run TestOriginalVersion -v
```
