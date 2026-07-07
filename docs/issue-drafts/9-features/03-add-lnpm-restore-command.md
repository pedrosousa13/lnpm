TITLE: Add `lnpm restore` to re-link packages after `retreat`
LABELS: enhancement
---
## Background

`lnpm retreat --force` (`internal/cli/retreat.go`) is meant to be a temporary step before `npm publish`: it restores each dependency's original version in package.json, removes the `node_modules` symlinks, deletes the `.lnpm/` directory, and deletes `lnpm.lock` entirely (`RunRetreat`'s final steps remove the lock file and `.lnpm/`). Once `lnpm.lock` is gone, there is no record of what was linked, so returning to local-linked development means manually re-running `lnpm add <pkg>` for every package that was previously linked, remembering the exact packages and flags used. yalc keeps its lock file around after a similar operation and offers `yalc restore` to reverse it.

## Motivation

As a developer, I run `lnpm retreat --force` right before `npm publish`, then afterward want to go straight back to local-linked development without re-typing every `lnpm add <pkg>` call from memory.

## Proposed behavior

```
$ lnpm retreat --force
...
✓ Retreat complete!

$ lnpm restore
Restoring from lnpm.lock.retreat...
✓ Restored mylib@1.3.0
✓ Restored other-lib@2.0.1
✓ Restore complete!
```

## Implementation sketch

1. In `internal/cli/retreat.go`'s `RunRetreat`, change the lock cleanup so the pre-retreat state survives: instead of only `os.Remove(lockPath)`, rename the lock (`os.Rename(lockPath, lockPath+".retreat")`) so a snapshot of what was linked is preserved. Add a small helper for the retreat-copy path next to the existing `lockFileName` constant in `pkg/lockfile/lockfile.go` (e.g. a `RetreatPath(projectPath string) string` or a `LoadRetreat`/`SaveRetreat` pair) so the filename isn't hardcoded in two places.
2. Add `restoreCmd` to `internal/cli/commands.go` and register it in `internal/cli/root.go`.
3. Add `RunRestore() error` in a new `internal/cli/restore.go`: load the retreat snapshot, then for each package run the same resolve-and-link path `RunAddMultiple` already uses in `internal/cli/add.go` (`database.GetPackageByName`, `s.GetFiles`, `linker.Link`, `updatePackageJSON`, then `lock.Add`/`lock.Save` writing back to the live `lnpm.lock`). On success, remove the `.retreat` snapshot file.
4. Handle a package whose exact previously-linked version is no longer what's in the store (the store is latest-wins, so an older linked version may have been superseded) the same way `runAddSingle` already reports a version mismatch (`internal/cli/add.go`, the check that compares `pkg.Version` against a requested version) — report it per-package and continue with the rest rather than aborting the whole restore.

## Acceptance criteria

- [ ] `lnpm retreat --force` preserves a snapshot of what was linked instead of discarding it outright.
- [ ] `lnpm restore` re-links every package from that snapshot, recreating `.lnpm/<pkg>`, the `node_modules` symlink, and the `file:.lnpm/<pkg>` package.json entry for each.
- [ ] Running `lnpm restore` with no prior retreat snapshot prints a clear "nothing to restore" message instead of erroring.
- [ ] After a successful restore, the snapshot file is removed and `lnpm.lock` matches the pre-retreat state.
- [ ] A package whose previously-linked version is no longer in the store is reported clearly without aborting the restore of other packages.

## Testing

- Extend `tests/retreat_test.go` to assert the `.retreat` snapshot is written on `retreat --force`.
- New `tests/restore_test.go`: add two packages, `retreat --force`, `restore`, assert `.lnpm/`, `node_modules` symlinks, and package.json dependencies match the pre-retreat state.

## Open questions

- If the user runs `lnpm add` for a different package between `retreat` and `restore`, should `restore` merge that new package into the result, refuse to run, or overwrite it? The simplest option (merge, keeping the newer add) should be validated against what a developer would actually expect.
