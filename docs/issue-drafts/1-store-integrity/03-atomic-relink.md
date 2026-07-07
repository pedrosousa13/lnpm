TITLE: Make relink atomic: populate a temp directory and rename-swap instead of clearing the live package
LABELS: bug
---
## Severity

Medium — an interrupted or failed relink leaves a consumer project pointing at a half-populated or empty package, and lnpm still reports it as linked.

## Background

lnpm links a stored package into a consumer project by populating `<project>/.lnpm/<pkg>` with the package's files and then creating a symlink `node_modules/<pkg>` → `.lnpm/<pkg>`. Relinking happens on every `lnpm add` and on every `lnpm push` (which re-publishes and refreshes all consumers). The important detail is ordering: whether the live directory a consumer is *currently building against* is safe while lnpm rewrites it.

## Problem

`Linker.Link` clears the target directory up front — `os.RemoveAll(lnpmPath)` followed by `os.MkdirAll(lnpmPath)` — and only *then* repopulates it file by file. The `node_modules/<pkg>` symlink already points at `.lnpm/<pkg>` from a previous link, so during the repopulation window the consumer's `node_modules/<pkg>` resolves to a directory that is empty or partially filled. The symlink is (re)created only at the very end of `Link`.

Concrete failure scenario: a consumer is running `webpack --watch`. The developer runs `lnpm push`. `Link` runs `RemoveAll(.lnpm/foo)`, recreates it empty, and begins copying files. At that instant webpack re-resolves `node_modules/foo` and finds an empty or half-written package — build fails or bundles garbage. Worse, if lnpm crashes or hits an error partway through the copy (disk full, permission error, Ctrl-C), the package is left permanently half-populated. `IsLinked` only does an `os.Stat` on the directory, so it still reports `foo` as linked — the broken state is invisible to lnpm.

## Where to look

- `internal/link/link.go:52-58` — `os.RemoveAll(lnpmPath)` then `os.MkdirAll(lnpmPath)` clears the live directory before any file is written.
- `internal/link/link.go:82-142` — the parallel populate loop writes files into `lnpmPath` in place.
- `internal/link/link.go:216-218` — `l.createNodeModulesSymlink(packageName)` runs only after population completes.
- `internal/link/link.go:338-342` — `IsLinked` is just `os.Stat(lnpmPath)`, so a half-populated dir still reads as "linked".
- `internal/store/store.go:88-102` — the pattern to imitate: `os.MkdirTemp(parent, ...)`, a `committed` flag, and a deferred cleanup that removes the temp dir unless committed.
- `internal/store/store.go:272-281` — the commit step to imitate: atomic `os.Rename(destPath, finalPath)` to swap the finished directory into place.

## How to fix

1. In `Linker.Link`, stop populating `lnpmPath` in place. Instead create a sibling temp directory next to it — e.g. `os.MkdirTemp(filepath.Join(l.projectPath, ".lnpm"), ".tmp-"+sanitized(packageName)+"-")` — and populate *that* (reflink/hardlink/copy into the temp dir). Mirror the `committed`-flag + deferred `os.RemoveAll(tempDir)` guard from `store.go:96-102` so a failure or panic cleans up the temp dir and leaves the existing linked package untouched.
2. After population succeeds, swap atomically: remove or rename-away the old `lnpmPath` and `os.Rename(tempDir, lnpmPath)`. Because rename onto an existing non-empty directory fails on most platforms, either rename the old dir aside first then remove it, or remove the old dir immediately before the rename — accept the tiny window but keep it after population, not before. Follow the store's rename-to-commit approach at `store.go:272-281`.
3. Keep the `node_modules` symlink creation at the end (`link.go:216-218`). Since the swap is atomic, the symlink target transitions directly from the old complete package to the new complete package, never through an empty state. For scoped packages (names containing `/`), make sure the temp directory name is filesystem-safe — put the temp dir under `.lnpm/` with a flat sanitized name, not a nested path.
4. Note for a follow-up (do not fix here): `IsLinked` being a bare stat is a related weakness. This issue makes the half-populated state impossible to reach; hardening `IsLinked` is out of scope.

## Acceptance criteria

- [ ] While `Link` is repopulating, the existing `.lnpm/<pkg>` directory (and thus `node_modules/<pkg>`) continues to resolve to the *previous* complete package until the new one is fully built.
- [ ] If `Link` errors or is interrupted mid-population, the previously linked package is left intact and no partial `.lnpm/<pkg>` exists.
- [ ] The final `.lnpm/<pkg>` contains exactly the package's files after a successful link (no leftover temp dirs under `.lnpm/`).
- [ ] Scoped packages (`@org/pkg`) link correctly through the temp-dir-and-swap path.
- [ ] `go test ./...` passes.

## Testing

From the repo root:

```
go test ./internal/link/...
go test ./tests/...
go test ./...
```

Add regression tests in `internal/link/` alongside `internal/link/link_test.go`. Suggested `TestLink_AtomicSwap_LeavesOldPackageOnFailure`: link a package once, then invoke a second link that is forced to fail mid-population (e.g. point at a store path with an unreadable file, or inject an error), and assert the original `.lnpm/<pkg>` files are still present and complete. Suggested `TestLink_NoTempDirsLeftBehind`: after a normal link, assert `.lnpm/` contains only `<pkg>` and no `.tmp-*` entries. Exercise the end-to-end `add`/`push` flows via `go test ./tests/...` (`tests/add_test.go`, `tests/push_test.go`).
