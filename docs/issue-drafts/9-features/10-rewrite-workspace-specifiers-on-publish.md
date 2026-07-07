TITLE: Rewrite workspace:* dependency specifiers on publish
LABELS: bug, enhancement
---
## Background

In an npm/pnpm/yarn workspace, a package can depend on a sibling package using a `workspace:` protocol specifier, e.g. `"@myorg/utils": "workspace:*"`. When `lnpm publish` packs a package, `internal/pack/pack.go`'s `Pack`/`readPackageJSON` read package.json only for `name`, `version`, `main`, and `files` — the `dependencies`/`devDependencies` values are never inspected or rewritten, and `internal/store/store.go`'s `Store` copies package.json into the store byte-for-byte (its only content transformation is `stripLifecycleScripts`, which strips `prepare`/`prepublish` scripts, not dependency specifiers). A search for `workspace:` across `internal/` finds no specifier-rewriting logic at all — the only hit is an unrelated `"failed to detect workspace"` error string in `internal/cli/publish.go`. As a result, a package published with `workspace:*`/`workspace:^`/`workspace:~` dependencies ships that literal, non-npm-installable specifier into every consumer's `.lnpm/<pkg>/package.json`. pnpm and yalc both rewrite `workspace:` specifiers to the sibling's real version at pack/publish time.

## Motivation

As a developer publishing `packages/ui` from a pnpm workspace where `ui`'s package.json has `"@myorg/utils": "workspace:*"`, I run `lnpm add @myorg/ui` in a separate consumer project and then `npm install`, and it fails because npm has no idea what `workspace:*` means outside a workspace.

## Proposed behavior

```
$ lnpm publish
Publishing @myorg/ui@1.4.0 (12 files)...
✓ Published @myorg/ui@1.4.0
```

The package.json stored for `@myorg/ui` (and therefore what's linked into consumers) has `"@myorg/utils": "workspace:*"` rewritten to `"@myorg/utils": "1.4.0"` (or `"^1.4.0"` for `workspace:^`, `"~1.4.0"` for `workspace:~`) — the developer's own package.json on disk in the workspace is left untouched.

## Implementation sketch

1. In `internal/pack/pack.go`, extend what `Pack`/`readPackageJSON` exposes so the caller has access to the raw `dependencies`/`devDependencies` maps, not just `PackageJSON`'s current `Name`/`Version`/`Main`/`Files` fields.
2. Add a rewrite function in `internal/pack` (e.g. `RewriteWorkspaceDeps(pkgDir string, rawPkgJSON map[string]interface{}) error`) that calls `workspace.Detect` (`internal/workspace/workspace.go`) to find the workspace root, then `ws.ListPackages()` to get every sibling's name and version. For each dependency value starting with `workspace:`, resolve it against the matching sibling: `workspace:*` / `workspace:latest` -> the sibling's exact version; `workspace:^` / `workspace:~` -> the sibling's version prefixed with `^`/`~`.
3. Call this rewrite step from `internal/cli/publish.go`'s `publishSingle`, right after `pack.Pack(pkgPath)` returns `pkgJSON`/`files` but before `pack.HashFiles(files)` — the rewritten package.json must be part of what gets hashed and stored, not the on-disk original, otherwise the content hash and the file actually installed by consumers would disagree.
4. Since `internal/store/store.go`'s `Store` currently clones package.json from the source tree via reflink/hardlink/copy, apply the rewrite either before that copy happens (so the copied bytes are already correct) or as a second pass inside `Store()`, following the same pattern `stripLifecycleScripts` already uses to modify the stored package.json in place.

## Acceptance criteria

- [ ] Publishing a workspace package with a `workspace:*`/`workspace:^`/`workspace:~` dependency stores a package.json with that specifier resolved to the sibling's real version.
- [ ] The resolved version is reflected in both the content hash (`pack.HashFiles`) and what gets linked into consumers via `lnpm add`.
- [ ] A package with no `workspace:` dependencies is unaffected (no unnecessary rewrite, no behavior change).
- [ ] The developer's own package.json in the workspace is never modified by this rewrite.
- [ ] Publishing a `workspace:` dependency on a sibling that isn't part of the detected workspace produces a clear error rather than shipping the literal unresolved specifier.

## Testing

- Extend `tests/workspace_test.go` with a case publishing a workspace fixture where one package depends on a sibling via `workspace:*`, asserting the stored/linked package.json contains the resolved version.
- e2e coverage in the style of `tests/e2e/monorepo_test.go`: publish two workspace packages with a `workspace:` dependency between them, add the consumer package elsewhere, and verify the resulting package.json is installable.

## Open questions

None — scope is matching pnpm/yalc's existing rewrite-to-version behavior, not inventing new semantics.
