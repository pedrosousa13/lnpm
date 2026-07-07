TITLE: Add version history and rollback to a previous published version
LABELS: enhancement, help wanted
---
## Background

lnpm's store is latest-wins: `internal/db/db.go`'s `InsertPackage` overwrites a package's single database record every time it's published under the same name, and `internal/cli/add.go`'s `runAddSingle` states this directly in a comment ("the store keeps the latest published version per package (latest-wins), so a requested version must match what's stored", lines ~265-266), enforced by the version-mismatch error a few lines later (~274-276). There is currently no way to list, pin to, or roll back to a previously published version once a newer one has replaced it in the database — even though the actual file bytes for an old version may still physically exist under `~/.lnpm/store/<name>/<oldhash>/` (`internal/store/store.go`'s `PackagePath`), the database record pointing at that hash was overwritten and is gone.

## Motivation

As a developer, I published a breaking change to `mylib` and pushed it to all consumers; one consumer app broke. I want to roll that one consumer back to the previous working version of `mylib` without re-publishing the old source from a git checkout, and I want a way to see what versions are actually available to roll back to.

## Proposed behavior

```
$ lnpm list mylib --versions
mylib versions:
  a1b2c3d4  1.3.0  published 2 hours ago
  9f8e7d6c  1.2.0  published 3 days ago  (currently linked in myapp)

$ lnpm add mylib@a1b2c3d4
Adding mylib@1.3.0 (hash: a1b2c3d4)...
✓ Added mylib@1.3.0
```

`database.GetPackageByHash` already exists in `internal/db/db.go`, so a content hash is already a valid, addressable identifier — this feature is mainly about keeping more than one version's record around long enough to look up and about surfacing that history to the user.

## Implementation sketch

1. Database schema: `Package` records in `internal/db/db.go` need to stop being overwritten in place per name. Change `InsertPackage` to always insert a new record (or add a distinct `InsertPackageVersion`) keyed by `(name, hash)`, and change what `bucketPackagesByName` stores — either an ordered list of IDs, or leave "resolve latest" to scanning by `CreatedAt`/`UpdatedAt` — instead of a single ID that gets overwritten on every publish.
2. `GetPackageByName` (`internal/db/db.go`) must keep returning "the latest" version for existing callers (`runAddSingle` in `internal/cli/add.go`, `publishSingle` in `internal/cli/publish.go`) so unversioned `lnpm add mylib` behavior is unchanged. Add a new `GetPackageVersions(name string) ([]*Package, error)` for history listing.
3. Add a `--versions` flag to `listCmd` in `internal/cli/commands.go`, and a corresponding path in `RunList` (`internal/cli/status.go`) that calls `GetPackageVersions` and prints hash/version/timestamp per row, reusing the existing table-formatting helpers already in `internal/cli/status.go` (`truncate`, `shortHash`, `formatTimeAgo`).
4. Extend `runAddSingle`'s version-matching logic in `internal/cli/add.go` (the `if version != "" && pkg.Version != version` check) to also accept a content hash as the `@version` selector, looking it up via `database.GetPackageByHash` when the requested string doesn't match the latest `Version`.
5. `internal/cli/gc.go`'s orphan detection currently operates on whatever `ListPackages()` returns (today, one "current" record per name) and treats a package as orphaned when it has zero links. Once multiple versions of the same name can be simultaneously present, decide whether an old-but-currently-unlinked version should be gc-eligible even while the package name itself still has active links elsewhere on some other version — this changes the unit of "orphaned" from per-name to per-version.

## Acceptance criteria

- [ ] `lnpm list <pkg> --versions` lists every retained published version of a package with its hash, semver version, and publish timestamp.
- [ ] `lnpm add <pkg>@<hash>` links a specific historical build by content hash.
- [ ] `lnpm add <pkg>` with no version still resolves to the most recently published version, unchanged from current behavior.
- [ ] `lnpm gc` correctly identifies and (with confirmation) removes historical versions that are no longer linked anywhere, without touching versions still in active use.

## Testing

- Extend `tests/database_test.go` for multi-version storage and `GetPackageVersions`.
- New `tests/version_history_test.go` covering `list --versions` and `add pkg@<hash>` rollback end to end.
- Extend `tests/gc_test.go` for the new per-version orphan semantics.

## Open questions

This needs design before implementation — it's the largest-scope item here. Key undecided points:
- Does every published version stay in the store indefinitely (unbounded space growth), or only the N most recent / until explicitly gc'd?
- How does `lnpm gc --older-than` (`internal/cli/gc.go`) interact with a package name that has multiple live-but-unlinked historical versions — does age apply per version or only to the oldest/newest?
- Should `lnpm add pkg@<hash>` work for any hash ever published, or only ones still referenced by at least one project's `lnpm.lock`?

Recommend resolving these as a short design doc before starting implementation.
