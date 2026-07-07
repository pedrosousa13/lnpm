TITLE: Add `--link` mode for live-updating dependencies without publish
LABELS: enhancement
---
## Background

lnpm currently supports exactly one linking strategy: `lnpm add` copies (using reflink/hardlink/copy, whichever is fastest) the package's published snapshot into `.lnpm/<pkg>/` and points `node_modules/<pkg>` at that copy via a symlink (`Link` method in `internal/link/link.go`), with package.json set to `file:.lnpm/<pkg>` (`updatePackageJSON` in `internal/cli/add.go`). Every source edit requires a fresh `lnpm publish` (or `lnpm push`) to propagate to consumers. yalc additionally supports `yalc add --link`, which points the dependency straight at the live source directory via a `link:` protocol entry, so edits in the source show up in the consumer immediately with no publish step.

## Motivation

As a developer actively iterating on `mylib` and `myapp` side by side, I want `myapp`'s `node_modules/mylib` to reflect my live edits to `mylib`'s source instantly, without running `lnpm publish` after every change.

## Proposed behavior

```
$ lnpm add mylib --link
Adding mylib@1.3.0 (linked to source)...
✓ Added mylib@1.3.0
  Link type: link (live source)
```

Editing a file in `mylib`'s source directory is immediately visible through `myapp/node_modules/mylib` with no further lnpm command needed.

## Implementation sketch

1. Add a `--link` bool flag to `addCmd` in `internal/cli/commands.go`, threaded through `RunAddMultiple`/`runAddSingle` in `internal/cli/add.go` alongside the existing `dev`/`pure`/`install` flags.
2. Use the package's recorded source directory: `db.Package.SourcePath` (`internal/db/db.go`) is already captured from `pkgPath` in `finishPublish` (`internal/cli/publish.go`) — reuse that instead of the store's copied files when `--link` is set.
3. In `internal/link/link.go`, add a new `LinkType` constant (e.g. `Live LinkType = "link"`) and a method such as `LinkSource(packageName, sourcePath string) (LinkType, error)` that skips the reflink/hardlink/copy file loop entirely and instead symlinks `.lnpm/<pkg>` directly to `sourcePath`, reusing `createNodeModulesSymlink`'s existing relative-path handling for scoped packages (`internal/link/link.go`).
4. In `internal/cli/add.go`'s `updatePackageJSON`, accept a link-mode flag so it writes `link:.lnpm/<pkg>` instead of `file:.lnpm/<pkg>` when `--link` is used. No change needed to `isLnpmReference` — it already recognizes the `link:.lnpm/` prefix.
5. `internal/cli/retreat.go`'s `RunRetreat` currently only special-cases `file:.lnpm/` when deciding whether a lock entry's `OriginalVersion` is really an lnpm-owned value vs. a real original version; extend that check to also treat `link:.lnpm/` as lnpm-owned so retreat correctly restores the pre-link version instead of trying to "restore" to the lnpm reference itself.

## Acceptance criteria

- [ ] `lnpm add <pkg> --link` symlinks the consumer's linked package directly to the original source directory rather than a store copy.
- [ ] package.json is set to `link:.lnpm/<pkg>` when `--link` is used.
- [ ] Editing a file in the source package is visible through the consumer's `node_modules/<pkg>` without any further lnpm command.
- [ ] `lnpm retreat --force` correctly restores the original dependency version for a package that was added with `--link`.
- [ ] `lnpm remove <pkg>` cleanly removes a `--link`-added package the same way it does a copy-linked one.

## Testing

- Extend `tests/add_test.go` and add a new `tests/link_mode_test.go`: `add --link`, edit a file in the source package outside the store, assert `node_modules` resolves the edit without re-publishing; assert `retreat --force` still restores the original version correctly.

## Open questions

- Live-updating the source means the consumer can see files the source hasn't published (or even committed) yet — a real isolation tradeoff against today's copy-based model, which is deliberately snapshot-based per publish. Worth stating this tradeoff plainly in `--help` text and the README.
- Should `--link` require a prior `lnpm publish` (to have `SourcePath` recorded in the DB), or should it work directly against any local directory the way `npm link`/`yalc add --link` do, bypassing the store/publish step entirely?
