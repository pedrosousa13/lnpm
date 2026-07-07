TITLE: Fix versioned package spec being treated as a content hash in multi-package add
LABELS: bug
---
## Severity

High — `lnpm add pkg@1.2.3 other-pkg` always fails for the versioned package even when that exact version is in the store, so the multi-package command is unusable with any pinned version.

## Background

`lnpm add` accepts specs in the form `name` or `name@version`. When a single package is added, the code looks the package up by name and then checks that the stored version matches the requested version. When two or more packages are added at once, a separate parallel code path (`RunAddMultiple`) resolves each spec. The store keeps the latest published version per package, and each stored package also has a content hash (a long digest of its file contents) that is unrelated to the semver version string.

## Problem

In the multi-package path, when a spec includes a version, the code resolves it with `database.GetPackageByHash(name, version)`. The second parameter of `GetPackageByHash` is the **content hash**, not the version string. A version like `1.2.3` will never equal a content-hash digest, so the lookup returns nil and the command reports `package <name> not found in store`, even when version `1.2.3` is published and stored.

Reproduction: publish `pkg-a` at version `1.2.3`, then run `lnpm add pkg-a@1.2.3 pkg-b`. `pkg-a` fails with "not found in store" while the single-package command `lnpm add pkg-a@1.2.3` succeeds.

## Where to look

- `internal/cli/add.go:73` — `name, version := parsePackageSpec(pkgSpec)` splits the spec
- `internal/cli/add.go:77-81` — the wrong branch: `if version != "" { pkg, lookupErr = database.GetPackageByHash(name, version) }` passes the version where a content hash is expected
- `internal/cli/add.go:88-92` — the resulting nil `pkg` produces `package %s not found in store`
- `internal/db/db.go:304` — `func (db *DB) GetPackageByHash(name, hash string)` — the second arg is a content hash, confirming the misuse
- `internal/cli/add.go:267-276` — the correct single-package logic to mirror: look up by name (`GetPackageByName`), then if `version != "" && pkg.Version != version` return an error naming the latest published version

## How to fix

Make the multi-package resolution match the single-package path:

1. In the goroutine in `RunAddMultiple`, always resolve by name: `pkg, lookupErr = database.GetPackageByName(name)`. Remove the `GetPackageByHash` branch.
2. After confirming `pkg != nil`, if `version != ""` and `pkg.Version != version`, set `result.err` to an error that mirrors the single-package wording at `add.go:275`, e.g. `fmt.Errorf("version %s of %s not found in store (latest published is %s). Re-publish %s to update.", version, name, pkg.Version, name)`, send the result, and return.
3. Keep the existing "not found in store" error for a genuinely absent package (`pkg == nil`).

## Acceptance criteria

- [ ] `lnpm add pkg-a@1.2.3 pkg-b` succeeds when `pkg-a@1.2.3` and `pkg-b` are both published.
- [ ] `lnpm add pkg-a@9.9.9 pkg-b` fails only for `pkg-a`, with a message naming the latest published version, matching the single-package error.
- [ ] The multi-package versioned-spec behavior is identical to the single-package path.

## Testing

Add an integration test to `tests/add_test.go` using the `setupTest` helper (see `tests/helpers_test.go`).

Test outline:
1. `te := setupTest(t)`.
2. Publish two packages: use `te.publishPkg("pkg-a", "1.2.3", map[string]string{"index.js": "module.exports=1"})` and `te.simplePkg("pkg-b")` (published at `1.0.0`).
3. `projectDir := te.newProject("consumer")`, then `te.chdir(projectDir)`.
4. Call `cli.RunAddMultiple([]string{"pkg-a@1.2.3", "pkg-b"}, false, false, false)` and assert it returns `nil`.
5. Assert both are linked: `te.AssertFilesLinked(projectDir, "pkg-a")` and `te.AssertFilesLinked(projectDir, "pkg-b")`, and `te.AssertPackageJSON(projectDir, "pkg-a", "file:.lnpm/pkg-a")`.
6. Add a second test that calls `cli.RunAddMultiple([]string{"pkg-a@9.9.9", "pkg-b"}, false, false, false)`, asserts a non-nil error is returned, and asserts `pkg-b` still linked while `pkg-a` is not.

Run:

```
go test ./tests/ -run TestAdd -v
```
