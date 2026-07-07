TITLE: Add dist-tag support (publish --tag, add pkg@tag)
LABELS: enhancement
---
## Background

lnpm's store keeps exactly one version per package name: `internal/db/db.go`'s `InsertPackage` looks up an existing package by name (`bucketPackagesByName`) and, if found, overwrites its `Version`/`ContentHash`/`SourcePath`/etc. in place — there's no history, just "whatever was published most recently under this name." `lnpm add <pkg>` (`runAddSingle` in `internal/cli/add.go`) always resolves via `database.GetPackageByName(name)`, and passing `pkg@version` only succeeds when that version happens to equal what's currently stored — the comment right above that lookup states this plainly ("the store keeps the latest published version per package (latest-wins), so a requested version must match what's stored") and the check a few lines later enforces it. A `--tag` flag on `publish` existed at one point but was removed as dead code in v1.11.0 because nothing ever read it ("The --tag flag was accepted but never used (publishSingle ignored it) — removed"). npm's dist-tags (`latest`, `beta`, `next`, ...) — and yalc's ability to `add pkg@sometag` — let a consumer opt into a named channel instead of always getting "whatever was last published."

## Motivation

As a developer maintaining a library used by several consumer apps, I want to publish an experimental build as `lnpm publish --tag beta` without disturbing the `latest` that other consumers are pinned to, and have those consumers explicitly opt in via `lnpm add mylib@beta`.

## Proposed behavior

```
$ lnpm publish --tag beta
Publishing mylib@2.0.0-beta.1 (tag: beta)...
✓ Published mylib@2.0.0-beta.1 (tag: beta)

$ lnpm add mylib@beta
Adding mylib@2.0.0-beta.1 (tag: beta)...
✓ Added mylib@2.0.0-beta.1

$ lnpm tag mylib beta --delete
✓ Removed tag beta from mylib
```

`lnpm add mylib` (no tag) continues to resolve to whatever is tagged `latest`, unchanged from today's default behavior.

## Implementation sketch

1. Schema change in `internal/db/db.go`: since `InsertPackage` currently keeps one live record per name (overwritten on every publish), supporting tags requires more than one addressable version per name to coexist. Add a new bucket, e.g. `bucketTags`, mapping a composite key (`name` + a separator + `tag`) to a content hash, while `bucketPackagesByName` keeps pointing at whatever is tagged `latest`. `InsertPackage` needs a `tag` parameter (default `"latest"`) and must stop discarding the previous record for a name when a different tag is published — in practice this means package records need to be addressable by `(name, hash)` rather than solely by `name` once more than one version can be live at once (`GetPackageByHash` already exists for hash-based lookup and can be reused/extended).
2. Add `--tag` back to `publishCmd` in `internal/cli/commands.go`, threaded through `RunPublish` -> `publishSingle` -> `finishPublish` (`internal/cli/publish.go`), passing it into the new tag-aware insert path.
3. Extend the version-resolution logic in `internal/cli/add.go` (currently `parsePackageSpec` + the exact-version check in `runAddSingle`): when `pkg@X` is given, first check whether `X` matches a known tag via the new tag bucket before falling back to treating `X` as an exact version/hash.
4. Add a `tagCmd` (`lnpm tag <pkg> <tag>` to set, `lnpm tag <pkg> <tag> --delete` to remove) to `internal/cli/commands.go` and a new `internal/cli/tag.go` with `RunTag`, registered in `internal/cli/root.go`.

## Acceptance criteria

- [ ] `lnpm publish --tag beta` stores a build without moving the `latest` tag.
- [ ] `lnpm add mylib@beta` links the build currently tagged `beta`.
- [ ] `lnpm add mylib` (no tag) is unaffected and keeps resolving to `latest`.
- [ ] `lnpm tag mylib beta` and `lnpm tag mylib beta --delete` manage tags on an already-published package without republishing.
- [ ] `lnpm list --store` shows which tag(s) point at each stored version.

## Testing

- Extend `tests/database_test.go` with coverage for the new tag bucket operations (set, resolve, delete, and the `latest` pointer staying correct).
- New `tests/tag_test.go` covering `publish --tag`, `add pkg@tag`, and `tag`/`tag --delete` on an existing package.

## Open questions

- Today, once `InsertPackage` overwrites the `latest` pointer for a name, the old hash's on-disk files under `~/.lnpm/store/<name>/<oldhash>/` aren't deleted, but they also become unreachable from `ListPackages()` since that record was overwritten rather than kept as a separate entry. Supporting tags cleanly means deciding whether the store needs to retain multiple versions' file trees simultaneously so a `beta` tag and a `latest` tag can point at genuinely different, still-present content — which in turn changes what `lnpm gc` (`internal/cli/gc.go`) considers orphaned, since its current orphan check operates on "packages with zero links" for whatever `ListPackages()` returns, not on a notion of "versions no longer reachable by any tag." This needs a design decision before implementation.
