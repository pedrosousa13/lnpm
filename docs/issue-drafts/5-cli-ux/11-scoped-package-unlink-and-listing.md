TITLE: Handle scoped packages correctly in unlink cleanup and listing
LABELS: bug
---
## Severity

Low — leaves harmless-but-persistent empty directories behind and misreports scoped package names in `list`, but does not lose data.

## Background

Scoped npm packages like `@org/pkg` are linked into `.lnpm/@org/pkg/` and `node_modules/@org/pkg` (a nested directory structure), unlike unscoped packages which live directly at `.lnpm/pkg`. `Linker.Unlink` removes a linked package, and `Linker.ListLinked` reports which packages are currently linked in a project.

## Problem

Two related bugs, both stemming from treating the scope segment (`@org`) as opaque instead of recursing into it:

1. `Unlink("@org/pkg")` removes `.lnpm/@org/pkg` (via `RemoveAll` on the full path) and the `node_modules/@org/pkg` symlink, but never removes the now-empty `.lnpm/@org` scope directory. The cleanup check right after only looks at whether the top-level `.lnpm` directory itself is empty (it still contains the `@org` entry), so it never fires for scoped packages, and `.lnpm/@org` is left behind permanently. The equivalent empty scope directory under `node_modules/@org` is never cleaned up either.
2. `ListLinked` only reads the top level of `.lnpm/` with `os.ReadDir` and returns each directory entry's name directly. For a scoped package, the top-level entry is `@org` (a directory), not `@org/pkg` — so a linked `@org/pkg` is reported to the user as a bare `@org`, which is not a valid, usable package name.

## Where to look

- `internal/link/link.go:229-249` — `Unlink`: removes `.lnpm/{packageName}` (line 230-233) and the `node_modules` symlink (line 236-239), then only checks the top-level `.lnpm` dir for emptiness (line 242-246) — never checks/removes an empty `.lnpm/@org` or `node_modules/@org`.
- `internal/link/link.go:345-362` — `ListLinked`: iterates only top-level entries of `.lnpm/` and appends `entry.Name()` directly, without recursing into `@`-prefixed directories.
- `internal/link/link_test.go:135-195` — `TestLinkScopedPackage`, existing coverage for `Link()` with scoped packages, useful as a pattern for a new `Unlink`/`ListLinked` scoped test.

## How to fix

1. In `Unlink`, after removing `.lnpm/{packageName}`, if `packageName` contains a `/` (i.e. it's scoped), also check whether the parent scope directory (`.lnpm/@org`) is now empty and remove it if so. Do the same for the `node_modules/@org` scope directory after removing the symlink.
2. In `ListLinked`, when an entry is a directory whose name starts with `@`, read its contents and append `@org/pkg` for each sub-entry found, instead of appending the bare `@org`. Non-`@`-prefixed directories continue to be reported as-is (one level, as today).

## Acceptance criteria

- [ ] Linking then unlinking `@org/pkg` leaves no empty `.lnpm/@org` or `node_modules/@org` directory behind.
- [ ] Unlinking an unscoped package is unaffected.
- [ ] `ListLinked` reports a linked scoped package as `@org/pkg`, not `@org`.
- [ ] Multiple packages under the same scope (`@org/a` and `@org/b`) are both listed correctly, and unlinking one leaves the scope directory in place while the other package is still linked.

## Testing

Add `TestUnlinkScopedPackage` and `TestListLinkedScopedPackage` to `internal/link/link_test.go`, following the setup pattern in the existing `TestLinkScopedPackage` (line 135): link a scoped package, then unlink it and assert `.lnpm/@org` and `node_modules/@org` no longer exist; separately, link a scoped package and assert `ListLinked()` returns `"@org/pkg"`.

```
go test ./internal/link/...
go test ./...
```
