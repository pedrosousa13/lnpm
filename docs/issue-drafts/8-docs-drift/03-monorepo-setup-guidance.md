TITLE: Fix MONOREPO.md push-location and npm-install-lnpm guidance
LABELS: docs

## Severity

Medium — following the root-level `lnpm push` examples pushes the wrong package (the monorepo root's own `package.json`, not a workspace package), and following the `devDependencies` snippets fails outright since lnpm isn't published to npm — despite the same doc explicitly warning against exactly that a few sections earlier.

## Background

`lnpm push` operates on whatever `package.json` is in the current working directory: it reads the package name from `cwd`, then re-packs and re-links that single package. `MONOREPO.md`'s own banner (line 5) states lnpm is a standalone Go binary that must be installed globally and never added to `package.json`. Despite that, several other sections in the same file contradict it by showing root-directory `lnpm push` usage and `devDependencies` snippets that add `lnpm` as an npm package.

## Problem

Two related issues in the same file.

**(a) Push run from the monorepo root, not the package directory.**

`MONOREPO.md:84-85` (root `package.json` scripts block):
> `"lnpm:publish": "lnpm publish --all",`
> `"lnpm:push": "lnpm push"`

`MONOREPO.md:336-339` (PNPM workflow):
> `# Build and push from monorepo`
> `cd ~/my-monorepo`
> `pnpm --filter @my/package build`
> `lnpm push`

`MONOREPO.md:379-383` (NPM workflow) and `MONOREPO.md:421-425` (Yarn workflow) show the identical pattern: `cd ~/my-monorepo` immediately followed by `lnpm push`.

`MONOREPO.md:630-636` (Summary table) lists a single `lnpm push` per tool under "Push Command" with no note that it must run inside a package directory.

Reality: `internal/cli/push.go:30` calls `pack.ReadPackageJSON(cwd)` — push always acts on the `package.json` in the current directory. Run from the monorepo root, `lnpm push` would try to pack and push the root package (typically `"private": true`, with no publishable code), not any workspace package. There is also no `--all` equivalent for push: `go run ./cmd/lnpm push --help` shows only a `--skip-hooks` flag, so "push all packages from root" isn't possible even if intended.

**(b) Docs show installing lnpm as an npm dependency, contradicting the doc's own warning.**

`MONOREPO.md:49` (architecture diagram):
> `│   └── lnpm ← installed here` (under `node_modules/`)

`MONOREPO.md:316-319` (PNPM root `package.json`), `:360-363` (NPM root `package.json`), `:403-406` (Yarn root `package.json`) all include:
> `"devDependencies": {`
> `  "lnpm": "*"`
> `}`

`examples/nx-example.md:9` and `examples/turborepo-example.md:9`:
> `├── package.json         # Root with lnpm installed`

Reality: `MONOREPO.md:5` states plainly: "lnpm is a **standalone Go binary**, not an npm package... Do NOT add it to package.json dependencies," and Best Practice #1 (`MONOREPO.md:432-442`) shows the exact counter-example:
> `# ❌ Wrong - not an npm package!`
> `npm install -D lnpm  # This won't work`

lnpm is not published to npm, so `"lnpm": "*"` in `devDependencies` can never resolve.

## Where to look

- `MONOREPO.md:5` and `:432-442` — the correct, standalone-binary guidance already in the file.
- `MONOREPO.md:49` — diagram showing `node_modules/lnpm`.
- `MONOREPO.md:84-85` — root `package.json` scripts including `lnpm:push`.
- `MONOREPO.md:316-319`, `:360-363`, `:403-406` — the three `devDependencies: { "lnpm": "*" }` snippets.
- `MONOREPO.md:336-339`, `:379-383`, `:421-425` — the three `cd ~/my-monorepo` + `lnpm push` workflow blocks.
- `MONOREPO.md:630-636` — Summary table's "Push Command" column.
- `examples/nx-example.md:9`, `examples/turborepo-example.md:9` — "Root with lnpm installed" captions.
- `internal/cli/push.go:16-33` — `RunPush` reading `package.json` from `os.Getwd()` (lines 18, 30); this is what makes root-directory push operate on the wrong package.
- `go run ./cmd/lnpm push --help` — confirms push has no `--all` flag (only `--skip-hooks`).

## How to fix

1. `MONOREPO.md:84-85` — remove the `"lnpm:push": "lnpm push"` line from the root `package.json` script block; keep `"lnpm:publish": "lnpm publish --all"` since `publish --all` does correctly operate from the root. Add a short note that pushing an individual package must be run from inside that package's directory (e.g. `cd packages/ui && lnpm push`).
2. `MONOREPO.md:336-339`, `:379-383`, `:421-425` — change `cd ~/my-monorepo` immediately before `lnpm push` to `cd` into the specific package directory that was just built (matching whichever package the preceding build command targeted in each example).
3. `MONOREPO.md:630-636` — update the "Push Command" column to `lnpm push (run from the package directory)` or add a footnote clarifying push must run inside the target package.
4. `MONOREPO.md:49` — change the `node_modules/lnpm ← installed here` diagram line to reflect that lnpm is a global binary, not a `node_modules` entry (e.g. replace with a comment noting lnpm is installed globally, or remove the line).
5. `MONOREPO.md:316-319`, `:360-363`, `:403-406` — remove the `"devDependencies": { "lnpm": "*" }` block from all three root `package.json` snippets.
6. `examples/nx-example.md:9` and `examples/turborepo-example.md:9` — change `# Root with lnpm installed` to something like `# Root package.json (lnpm runs as a global binary, not listed here)`.

## Acceptance criteria

- [ ] No MONOREPO.md workflow shows `lnpm push` being run from the monorepo root; every push example `cd`s into the specific package directory first.
- [ ] No doc snippet adds `lnpm` to a `package.json` `devDependencies` block.
- [ ] The root `package.json` script examples no longer include an `lnpm:push` entry that would push the wrong package.
- [ ] The diagrams/comments in `MONOREPO.md`, `nx-example.md`, and `turborepo-example.md` no longer imply lnpm is installed via `node_modules`.

## Testing

```
grep -n "devDependencies" -A2 MONOREPO.md | grep "lnpm"
grep -n "lnpm:push" MONOREPO.md
grep -n "installed here\|lnpm installed" MONOREPO.md examples/nx-example.md examples/turborepo-example.md
```

All should return nothing (or only the corrected comment text). Confirm push always targets `cwd` and has no `--all` flag:

```
go run ./cmd/lnpm push --help
```
