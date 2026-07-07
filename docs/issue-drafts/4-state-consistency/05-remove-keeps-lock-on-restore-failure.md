TITLE: Keep the lock entry when remove fails to restore package.json
LABELS: bug
---
## Severity

Medium — a `package.json` restore failure during `remove` permanently loses the user's original dependency specifier and leaves the project in a broken, unrecoverable state.

## Background

`lnpm remove <pkg>` undoes what `lnpm add` did for a single package: it deletes the `node_modules/<pkg>` symlink and the `.lnpm/<pkg>` copy (`linker.Unlink`), rewrites `package.json` to either restore the original dependency specifier (e.g. `^1.2.3`, read from `lnpm.lock`'s `OriginalVersion` field) or delete the dependency entirely if there was no original version, and then removes the package's entry from `lnpm.lock`. The lock entry is the *only* place the original version specifier is stored — once it's gone, nothing in `lnpm` can reconstruct it.

## Problem

The `package.json` restore/removal step and the lock-removal step are not linked: a failure in the first does not stop the second. In `RunRemove`'s per-package loop, if `restorePackageJSON` (or `removeFromPackageJSON`) returns an error, the code only prints a warning — it does not increment the failure counter or `continue`. Execution falls through to `lock.Remove(name)`, which unconditionally drops the package's lock entry, and the lockfile is saved with that entry gone.

Concrete failure scenario: a consumer project's `package.json` has been manually edited into a state that fails to parse as JSON (or the file has become read-only) between `add` and `remove`. The user runs `lnpm remove pkg`. `linker.Unlink("pkg")` succeeds — the `node_modules/pkg` symlink and `.lnpm/pkg` directory are deleted. `restorePackageJSON` then fails because it can't read/parse `package.json`, and only a warning is printed. `lock.Remove("pkg")` still runs, and the lock is saved without the `pkg` entry. The end state: `package.json` still contains `"pkg": "file:.lnpm/pkg"` (a path that no longer exists, since `Unlink` already deleted it), and `lnpm.lock` no longer has any record that `pkg`'s original version was `^1.2.3`. No `lnpm` command — not `remove`, not `retreat` — can recover the original specifier, because the only place it was ever stored has just been erased.

## Where to look

- `internal/cli/remove.go:72-76` — `linker.Unlink(name)` runs first; on failure it already increments `failed` and `continue`s, correctly skipping the rest of the loop body. This is the pattern to reuse for the restore step.
- `internal/cli/remove.go:79-88` — the restore/removal step: on `restorePackageJSON` failure (line 81) or `removeFromPackageJSON` failure (line 86), only `iconWarn()` is printed; neither branch increments `failed` or skips the rest of the loop.
- `internal/cli/remove.go:91` — `lock.Remove(name)` runs unconditionally after the restore/removal step, regardless of whether it succeeded.
- `internal/cli/remove.go:93-98` — the database link deletion also runs unconditionally right after `lock.Remove`.
- `internal/cli/remove.go:104-107` — `lock.Save(cwd)` persists whatever the loop did, including a dropped entry for a package whose `package.json` was never actually fixed.
- `internal/cli/remove.go:128-130` — `if failed > 0 { return fmt.Errorf(...) }` — the existing non-zero-exit mechanism; it only fires if `failed` is incremented, which the restore/removal failure path currently never does.

## How to fix

1. In the `if lockEntry.OriginalVersion != "" { ... } else { ... }` block at `internal/cli/remove.go:79-88`, change both failure branches to report the failure as fatal for that package rather than a warning: use `iconFail()` (matching the `Unlink` failure branch above it) instead of `iconWarn()`.
2. In both branches, after printing the failure, `failed++` and `continue` — mirroring exactly the pattern already used for the `Unlink` failure at lines 73-75. The `continue` skips the rest of the loop body for that package, so `lock.Remove(name)` (line 91) and the database `DeleteLink` call (lines 94-98) are never reached when the `package.json` update failed.
3. This guarantees the lock entry (and its `OriginalVersion`) survives whenever the corresponding `package.json` write did not go through, so a subsequent `remove` retry (after the user fixes whatever made `package.json` unwritable) can still restore the real version.
4. No change is needed to the final `if failed > 0` check (line 128-130) — it already returns a non-zero-exit error once `failed` is incremented.

## Acceptance criteria

- [ ] If `restorePackageJSON` fails for a package during `remove`, that package's entry in `lnpm.lock` (including `OriginalVersion`) is not removed.
- [ ] If `removeFromPackageJSON` fails for a package during `remove`, that package's entry in `lnpm.lock` is not removed.
- [ ] In both failure cases, the package is counted toward `failed` and `RunRemove` returns a non-nil error.
- [ ] In both failure cases, the database link for that package is not deleted (it stays consistent with the retained lock entry).
- [ ] The happy path (successful restore or removal) is unchanged: the lock entry is removed and the lockfile saved as before.

## Testing

Add integration tests to `tests/remove_test.go` using the `setupTest` helper (see `tests/helpers_test.go`).

Test outline:
1. `env := setupTest(t)`.
2. `projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)`; write a `package.json` depending on `restore-fail-pkg` at `^1.0.0` via `env.writePackageJSON`.
3. `env.simplePkg("restore-fail-pkg")`; `env.addPkg(projectDir, "restore-fail-pkg", false, false)`.
4. Corrupt `package.json` so `restorePackageJSON`'s `json.Unmarshal` fails: `env.writeFile(filepath.Join(projectDir, "package.json"), "{not valid json")`.
5. Call `cli.RunRemove("restore-fail-pkg", false)` and assert it returns a non-nil error.
6. Assert the lock entry survives: `env.AssertLockfile(projectDir, func(l *lockfile.LockFile) { pkg, ok := l.Get("restore-fail-pkg"); if !ok { t.Fatal("expected lock entry to survive restore failure") }; if pkg.OriginalVersion != "^1.0.0" { t.Errorf("expected OriginalVersion ^1.0.0, got %q", pkg.OriginalVersion) } })`.
7. Assert the database link also survives: `env.AssertDatabaseLink("restore-fail-pkg", projectDir)`.

Run:

```
go test ./tests/ -run TestRemove -v
```
