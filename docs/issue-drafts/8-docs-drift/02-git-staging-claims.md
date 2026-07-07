TITLE: Remove git-staging change detection claims from docs
LABELS: docs

## Severity

Medium — following the documented "stage your changes" workflow gives developers a false model of how `lnpm push` decides what to push, which can lead to confusion (e.g. assuming unstaged changes are ignored, when in fact everything in the working tree is re-packed regardless of git state).

## Background

`lnpm push` re-publishes the package in the current directory to the shared store and re-links it into every project that consumes it. Several docs tell readers to `git add` their changes before running `lnpm push`, framing this as required or helpful for "detecting" changes. lnpm has no git integration anywhere in its codebase — `lnpm push` always re-packs and re-hashes whatever is currently on disk in the working directory, staged or not.

## Problem

`MONOREPO.md:485-498` ("### 5. Git Stage for Fast Iteration"):
> `# Stage changes (no commit needed)`
> `git add packages/ui/src/button.tsx`
> `# Push immediately detects staged changes`
> `lnpm push`

`MONOREPO.md:546-555` ("### Issue: Changes not detected"):
> `Stage your changes so lnpm picks them up:`
> `git add .`
> `# Push detects staged changes`
> `lnpm push`

`examples/turborepo-example.md:137-148` ("### Working with git"):
> `# Stage (no commit needed!)`
> `git add packages/ui/src/button.tsx`
> `# Push detects staged changes instantly`
> `lnpm push`

`examples/turborepo-example.md:213-219` ("**Issue: Changes not pushed**"):
> `Stage your changes so lnpm picks them up:`
> `git add .`
> `lnpm push`

`examples/nx-example.md:171-183` ("### Working with git"):
> `# Stage (no commit needed!)`
> `git add libs/ui/`
> `# Push detects staged changes`
> `cd libs/ui`
> `lnpm push`

`examples/nx-example.md:313-324` ("**Issue: Changes not pushed**"):
> `Make sure you're in the library directory or use git staging:`
> `cd libs/feature-auth`
> `lnpm push`
> `# Or from root with git`
> `git add libs/feature-auth/`
> `cd libs/feature-auth`
> `lnpm push`

None of this is real. `git add` has zero effect on `lnpm push`.

## Where to look

- Doc passages above (`MONOREPO.md:485-498`, `:546-555`; `examples/turborepo-example.md:137-148`, `:213-219`; `examples/nx-example.md:171-183`, `:313-324`).
- `internal/cli/push.go:16-60` — `RunPush`: gets the current directory (`os.Getwd()`, line 18), reads `package.json` from it (`pack.ReadPackageJSON(cwd)`, line 30), and unconditionally re-packs it (`pack.Pack(cwd)`, line 50, and the update path further down the function). No git package is imported and no git command is ever invoked.
- The repository has no git integration to search for: `grep -rn "go-git\|exec.Command(\"git\"" internal/` returns nothing.

## How to fix

1. `MONOREPO.md:485-498` — delete the "### 5. Git Stage for Fast Iteration" subsection entirely; staging has no bearing on push.
2. `MONOREPO.md:546-555` — rewrite "### Issue: Changes not detected" to describe the real cause and fix, e.g.: confirm you're running `lnpm push` from inside the package directory whose files changed, and confirm your build step actually wrote its output before pushing. Remove the `git add` instruction.
3. `examples/turborepo-example.md:137-148` — delete the "### Working with git" subsection.
4. `examples/turborepo-example.md:213-219` — rewrite "Issue: Changes not pushed" to remove `git add .`; replace with the real fix (confirm cwd is the package directory; confirm the build ran and wrote output).
5. `examples/nx-example.md:171-183` — delete the "### Working with git" subsection. If the `cd libs/ui && lnpm push` example is worth keeping, keep only that, without the staging framing.
6. `examples/nx-example.md:313-324` — rewrite "Issue: Changes not pushed" to drop the `git add` branch entirely, keeping only "cd into the library directory before running `lnpm push`" as the fix.

## Acceptance criteria

- [ ] No doc claims `lnpm push` detects, requires, or is affected by git staged/unstaged state.
- [ ] Troubleshooting sections for "changes not pushed/detected" describe the real cause (must run push from inside the correct package directory, after the build has run) instead of `git add`.
- [ ] `internal/cli/push.go` is untouched — this is a docs-only issue.

## Testing

```
grep -rn "staged changes\|git staging\|git add" MONOREPO.md examples/turborepo-example.md examples/nx-example.md
```

Should return nothing. Confirm push has no git dependency:

```
grep -n "go-git\|exec.Command(\"git\"" internal/cli/push.go
```

Should return nothing. Confirm the real push behavior via help text:

```
go run ./cmd/lnpm push --help
```
