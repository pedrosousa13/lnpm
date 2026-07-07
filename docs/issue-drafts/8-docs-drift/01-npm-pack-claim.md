TITLE: Correct README claim that publish uses npm pack for file filtering
LABELS: docs

## Severity

Medium — the doc overstates guarantees ("identical to npm publish") that don't hold, which could give a false sense of security about which files end up copied into the shared store.

## Background

`lnpm publish` decides which files from a package get copied into the shared store at `~/.lnpm/store`. The README describes this filtering as delegating to the real `npm` CLI (`npm pack --dry-run`), with a fallback to custom logic only if `npm` isn't available. In reality, lnpm has never called out to `npm` for this — the filtering is a pure Go implementation, and the `npm pack` dependency lnpm briefly had was removed entirely.

## Problem

`README.md:314`:
> `1. **Publish** — Uses `npm pack` rules to determine files, strips lifecycle scripts (`prepare`/`prepublish`), then links or copies to `~/.lnpm/store/{name}/{hash}/``

`README.md:332`:
> `lnpm uses **npm's standard packing rules** (via `npm pack --dry-run`) to determine which files to include, ensuring consistency with actual npm publish behavior. This means:`

`README.md:340`:
> `- Automatic fallback to custom filtering if npm is unavailable`

`README.md:342`:
> `This approach prevents issues with git hooks (like Husky) running in linked packages and ensures lnpm behaves identically to npm publish.`

None of this is true. `internal/pack/pack.go` never invokes the `npm` binary — there is no `exec.Command` calling `npm` anywhere in the package (or the repo). File selection is entirely custom Go code, and it does not honor `.npmignore`/`.gitignore` negation patterns (`!pattern`), so it is not "identical to npm publish."

## Where to look

- `README.md:314` — "Publish" step description claiming `npm pack` rules.
- `README.md:330-342` — the "File Filtering" section with the `npm pack --dry-run` claim, the "fallback if npm is unavailable" bullet, and the "identical to npm publish" claim.
- `internal/pack/pack.go:89-110` — `Pack()`, the entry point; calls `collectFiles` then `filterGitFiles`, no npm invocation.
- `internal/pack/pack.go:139-145` — `collectFiles` loads `.npmignore`/`.gitignore` patterns via `loadIgnorePatterns` and appends `defaultExcludes`, all pure Go.
- `internal/pack/pack.go:55-86` — `defaultExcludes`, the built-in always-excluded list.
- `internal/pack/pack.go:179-196` — where `collectFiles` calls `isExcluded`, then applies the `files` whitelist via `isIncluded`/`isDefaultInclude`.
- `internal/pack/pack.go:310-353` — `isExcluded`, the custom matcher. Lines 316-318 show negation patterns (`!pattern`) are skipped via `continue` rather than honored — a concrete way the behavior is not "identical to npm publish."
- `CHANGELOG.md:85` — "parallel hashing/linking, remove npm pack dep, fix defer errors" (v1.7.6), confirming the npm-pack dependency was removed, not merely made optional with a fallback.

## How to fix

1. Rewrite `README.md:314` to describe the actual mechanism, e.g.: "1. **Publish** — Walks the package directory, applying the `files` whitelist, `.npmignore`/`.gitignore` patterns, and built-in default excludes to determine files, strips lifecycle scripts, then links or copies to `~/.lnpm/store/{name}/{hash}/`".
2. Rewrite the `README.md:332` lead sentence to drop the `npm pack --dry-run` claim, e.g.: "lnpm determines which files to include using its own file-filtering logic, closely modeled on npm's packing conventions. This means:".
3. Remove the `README.md:340` bullet ("Automatic fallback to custom filtering if npm is unavailable") — there is no npm-based path to fall back from.
4. Rewrite `README.md:342` to drop the "identical to npm publish" claim and note the known gap, e.g.: "This approach prevents issues with git hooks (like Husky) running in linked packages. Note: filtering closely follows npm/gitignore conventions but does not currently honor `.npmignore`/`.gitignore` negation (`!pattern`) entries."

## Acceptance criteria

- [ ] README no longer claims publish shells out to `npm pack` or `npm pack --dry-run`.
- [ ] README no longer claims a "fallback to custom filtering if npm is unavailable."
- [ ] README no longer claims behavior "identical to npm publish"; the negation-pattern gap is called out instead.
- [ ] The "File Filtering" section accurately reflects `collectFiles`/`isExcluded` in `internal/pack/pack.go`.

## Testing

```
grep -n "npm pack" README.md
grep -n "identical to npm publish" README.md
grep -n "Automatic fallback to custom filtering" README.md
```

All three should return nothing. Confirm there's no npm invocation in the pack package:

```
grep -rn "exec.Command" internal/pack/
```
