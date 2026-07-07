TITLE: Add `lnpm check` command to catch lingering file: dependencies before publish
LABELS: enhancement
---
## Background

`lnpm add` rewrites a consumer's package.json dependency to `file:.lnpm/<pkg>` (see `updatePackageJSON` in `internal/cli/add.go`). This is meant to be temporary during local development, and the README's "Before Publishing to npm" section tells developers to run `lnpm retreat --force` first to restore the original version before running `npm publish`. That's a manual step with nothing enforcing it: if a developer forgets, a `file:.lnpm/...` (or `link:.lnpm/...`) reference can ship in a published npm package or get committed to git. yalc has a `yalc check` command for exactly this: it fails loudly if any lingering `link:`/`file:` yalc reference is still present.

## Motivation

As a developer, I want a command I can wire into a pre-commit hook or a CI step that fails with a non-zero exit code if package.json (or, in a monorepo, any workspace package's package.json) still has an `lnpm`-owned `file:.lnpm/` or `link:.lnpm/` dependency, so I can't accidentally commit or publish a linked dependency.

## Proposed behavior

```
$ lnpm check
✗ package.json: mylib -> file:.lnpm/mylib
lnpm check failed: 1 lingering lnpm reference found

$ lnpm check
✓ No lnpm references found in package.json
```

Exit code is non-zero when any lingering reference is found, zero otherwise.

## Implementation sketch

1. Add `checkCmd` (`Use: "check"`, no flags needed for the initial version) to `internal/cli/commands.go`, registered in `internal/cli/root.go`'s `init()`.
2. Add `RunCheck() error` in a new `internal/cli/check.go`. Read `package.json` from `os.Getwd()`, and reuse the existing `isLnpmReference(version string) bool` helper already defined in `internal/cli/add.go` (it already checks for both the `file:.lnpm/` and `link:.lnpm/` prefixes) against every value in the `dependencies` and `devDependencies` maps.
3. For monorepos, reuse `workspace.Detect(cwd)` and `ws.ListPackages()` (`internal/workspace/workspace.go`) to run the same check against every workspace package's package.json, not just the current directory's.
4. Print each offending `<package.json path>: <dep name> -> <value>` line, and return a non-nil error so cobra reports failure and the process exits non-zero — the same "return an error so scripts can detect it" pattern `RunAddMultiple` already uses in `internal/cli/add.go`.

## Acceptance criteria

- [ ] `lnpm check` exits 0 and prints a success message when no `file:.lnpm/`/`link:.lnpm/` references exist.
- [ ] `lnpm check` exits non-zero and lists every offending dependency when one or more exist.
- [ ] In a workspace, `lnpm check` scans every workspace package's package.json, not just the current directory.
- [ ] README documents `lnpm check` as a pre-commit hook example.

## Testing

- New `tests/check_test.go`: a clean project (passes), a project with a lingering `file:.lnpm/` dependency (fails with correct exit code and message), and a workspace fixture with one offending sub-package (fails and names the right file).

## Open questions

None — this is a straightforward read-only scan with a clear yalc precedent.
