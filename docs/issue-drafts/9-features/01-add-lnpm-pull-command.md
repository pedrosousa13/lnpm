TITLE: Add `lnpm pull` command to sync linked packages from the store
LABELS: enhancement
---
## Background

lnpm links a package into a consumer project with `lnpm add <pkg>`, which copies the package's current store contents into `.lnpm/<pkg>/`, symlinks `node_modules/<pkg>` to it, and rewrites the dependency in package.json to `file:.lnpm/<pkg>`. If the source package is republished later (via `lnpm publish` without `--push`, or by a teammate on another machine sharing the same store), the consumer has no dedicated way to just pick up the new contents — the only option today is re-running `lnpm add <pkg>` from scratch. yalc's equivalent is `yalc update [pkg]`, but lnpm already has a command named `update` (`internal/cli/update.go`) that self-updates the lnpm binary from GitHub releases, so reusing that name for this would collide with an unrelated, already-shipped feature.

## Motivation

As a developer, I have `mylib` linked into `myapp` via `lnpm add mylib`. A teammate (or I, without `--push`) publishes a new version of `mylib` to the shared store. I want one command to run inside `myapp` that refreshes `mylib` to whatever the store currently has, without wondering whether `lnpm update` will instead try to self-update the CLI.

## Proposed behavior

```
$ lnpm pull
Pulling mylib... updated 1.2.0 -> 1.3.0
✓ Pulled 1 package(s)

$ lnpm pull mylib
Pulling mylib... updated 1.2.0 -> 1.3.0
✓ Pulled mylib@1.3.0
```

With no arguments, `lnpm pull` refreshes every package currently listed in `lnpm.lock`. With one or more package names, it refreshes just those.

## Implementation sketch

1. Add a `pullCmd` in `internal/cli/commands.go`, modeled on `addCmd`/`removeCmd` (`Args: cobra.ArbitraryArgs` since zero args means "pull everything currently linked"), and register it in `internal/cli/root.go`'s `init()` alongside the other `rootCmd.AddCommand(...)` calls.
2. Add `RunPull(packageNames []string) error` in a new `internal/cli/pull.go`. When `packageNames` is empty, read the linked set via `lockfile.Load(cwd)` (`pkg/lockfile/lockfile.go`) and `lock.List()`.
3. For each package, mirror the resolve-and-link steps already in `runAddSingle` (`internal/cli/add.go`): `database.GetPackageByName(name)` to find the current store version, `s.GetFiles(pkg.Name, pkg.ContentHash)`, then `linker.Link(pkg.Name, pkg.StorePath, files)` (`internal/link/link.go`). Skip `updatePackageJSON` — the `file:.lnpm/<pkg>` entry and package.json don't need to change, only the contents of `.lnpm/<pkg>` and the lock entry.
4. Update the lock entry per package with `lock.Add(name, lockfile.Package{...})` using the new `Version`/`ContentHash`/`time.Now()`, preserving the existing `OriginalVersion` field (same pattern `runAddSingle` uses to avoid clobbering it), then `lock.Save(cwd)`.
5. Print a per-package summary line showing old version -> new version (skip/no-op message if a package is already at the latest store version).

## Acceptance criteria

- [ ] `lnpm pull` with no arguments refreshes every package in `lnpm.lock` to the current store version.
- [ ] `lnpm pull <pkg>` refreshes only the named package(s).
- [ ] Pulling a package that is already up to date is a no-op that doesn't error.
- [ ] Pulling a package that isn't linked in the current project returns a clear error.
- [ ] `lnpm pull` never modifies package.json.
- [ ] `lnpm.lock` reflects the new version/hash and updated `Linked` timestamp after a pull.

## Testing

- Integration tests in a new `tests/pull_test.go` (pattern after `tests/add_test.go`): publish v1, `lnpm add`, republish v2 without `--push`, run `lnpm pull`, assert `.lnpm/<pkg>` contents and `lnpm.lock` reflect v2, and package.json is unchanged.
- e2e coverage alongside `tests/e2e/monorepo_test.go`'s style for a multi-package pull.

## Open questions

- Naming: `pull` avoids the collision with the existing self-update `lnpm update`, but alternatives like `lnpm sync` or `lnpm refresh` are also collision-free — worth a quick gut-check before locking in the name.
- Should `lnpm pull pkg@version` be supported to pin to a specific store version, or is that out of scope until version history exists?
