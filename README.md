# lnpm

> Fast, reliable local npm package development tool — a better alternative to yalc.

[![CI](https://github.com/pedrosousa13/lnpm/actions/workflows/ci.yaml/badge.svg)](https://github.com/pedrosousa13/lnpm/actions/workflows/ci.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/pedrosousa13/lnpm)](https://goreportcard.com/report/github.com/pedrosousa13/lnpm)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## Features

- **Blazing fast** — Reflink/CoW + hard links for instant operations on large packages
- **Smart linking** — Automatically uses the fastest method: reflink → hardlink → parallel copy (store → project); reflink → parallel copy into the store
- **Monorepo support** — Publish all workspace packages at once
- **Cross-platform** — Works on Linux, macOS, and Windows
- **All package managers** — npm, yarn, pnpm, and bun
- **Full visibility** — Know exactly what's linked where

## Installation

> **On lnpm 1.9.x or older? Reinstall manually.** Those versions compare versions
> byte-wise, so `lnpm update` reports `Already up to date` and never upgrades you.
> Run `lnpm --version` to check, then reinstall with the script below or with
> `go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest`. 1.10.0 and later are
> unaffected.

### Quick Install (Linux/macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.sh | sh
```

### Quick Install (Windows)

```powershell
irm https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.ps1 | iex
```

Installs to `%LOCALAPPDATA%\lnpm`. Override with `$env:LNPM_INSTALL_DIR`.

> **Note:** On Windows, lnpm uses NTFS junction points for linking — no Developer Mode or admin privileges required.

### Go

```bash
go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest
```

### From Source

```bash
git clone https://github.com/pedrosousa13/lnpm.git
cd lnpm
make install
```

## Quick Start

```bash
# In your library package
cd ~/code/my-library
lnpm publish

# In your app that uses the library
cd ~/code/my-app
lnpm add my-library

# Make changes to my-library, then push updates
cd ~/code/my-library
lnpm push
```

## Documentation

- **[Monorepo Guide](MONOREPO.md)** — Turborepo, Nx, PNPM/NPM/Yarn workspaces integration
- **[Architecture](ARCHITECTURE.md)** — System design and implementation details
- **[Changelog](CHANGELOG.md)** — Version history and updates
- **[Releasing](docs/releasing.md)** — How a release is cut, and the two steps that need a person
- **[Roadmap](ROADMAP.md)** — What lnpm does today, and where planned work is tracked

## Commands

| Command | Description |
|---------|-------------|
| `lnpm publish` | Publish package to local store (`--dry-run` lists what would be packed and writes nothing) |
| `lnpm add <pkg...>` | Add package(s) from store to project |
| `lnpm remove <pkg>` | Remove linked package |
| `lnpm pull [pkg...]` | Sync linked packages with the store (all of them when no name is given) |
| `lnpm push` | Push changes to all linked projects (`--tag` picks the channel) |
| `lnpm status` | Show all published packages and active links across every project |
| `lnpm list` | List this project's linked packages (`--store` lists the store, `--projects` lists consumers, `--versions` lists one package's history) |
| `lnpm tag <pkg> <tag>` | Point a dist-tag at a published package (`--delete` removes one) |
| `lnpm check` | Fail if lnpm has left anything an `npm publish` would ship (pre-publish guard) |
| `lnpm doctor` | Diagnose issues |
| `lnpm gc` | Garbage collect unused packages (`--dry-run`, `--older-than 30d`, `--fix-links`, `--yes`) |
| `lnpm forget <project-path>` | Drop the record of a project whose filesystem is gone for good, so `lnpm gc` can reclaim what it was holding (`--yes`) |
| `lnpm retreat` | Remove all lnpm changes |
| `lnpm restore` | Re-link the packages `lnpm retreat` removed |
| `lnpm config` | View/edit configuration |
| `lnpm update` | Update lnpm to the latest version |
| `lnpm completion` | Generate shell completions |

## Usage Examples

### Basic Workflow

```bash
# Publish a package
lnpm publish

# Add to a project (supports multiple packages)
lnpm add my-package
lnpm add pkg-a pkg-b pkg-c
lnpm add my-package --link   # Link to the live source instead of a store copy

# Push updates after making changes
lnpm push
```

### Live Linking (`--link`)

By default `lnpm add` copies the package's published snapshot into
`.lnpm/{package}/`, so the project only sees changes you deliberately
`publish` or `push`. With `--link`, `.lnpm/{package}` is instead a link to the
directory the package was published from, and `package.json` says
`link:.lnpm/{package}`:

```bash
lnpm add my-package --link
# Every edit in the source package is visible to this project immediately,
# with no publish, push or pull.
```

The tradeoff is the isolation you give up: the project builds against whatever
is in the source tree right now, including files that have never been published
and are not even committed. Prefer the default when you want a reproducible
snapshot; use `--link` while you iterate. `lnpm pull` and `lnpm push` skip
live-linked packages rather than replacing them with a snapshot, and
`lnpm remove` / `lnpm retreat` delete only the link, never the source.

### Dist-tags

A publish moves the `latest` tag, and `lnpm add my-package` resolves through it.
`--tag` publishes to another channel instead, leaving `latest` where it is, so an
experimental build can sit in the store without reaching the projects that are on
the stable release:

```bash
# In the package directory
lnpm publish --tag beta

# In a project that wants the experimental build
lnpm add my-package@beta

# Everyone else is unaffected
lnpm add my-package        # still the latest release
```

What follows the `@` is read as a tag first, then as an exact version, then as a
content-hash prefix — the last two are what [roll a project back](#version-history-and-rollback).
A project that added a package under a tag keeps following it: `lnpm pull`
refreshes it to whatever that tag now names, never to `latest`. Switching
channels is just another `lnpm add` — `lnpm add my-package@beta` in a project
already on `latest` moves it over, and dropping the tag moves it back — and so
does [unpinning](#version-history-and-rollback), which is the same command. None
of the three can be combined with `--link`, which resolves to the source
directory rather than to any published build.

`lnpm push` goes to the channel the build in the store already carries, so
pushing a pre-release keeps it a pre-release. An edit gives the tree content no
channel has ever named, and the version in `package.json` answers instead — a
push loop does not bump it, so the tags naming the stored build with that
version say which channel the tree is on. Bump past everything in the store and
push has nothing left to go on: it goes to `latest` and says so. Pass `--tag` to
say otherwise.

Tags can also be moved on a package already in the store, with no republish:

```bash
lnpm tag my-package beta            # Point beta at the published version
lnpm tag my-package beta --delete   # Remove the beta tag
lnpm list --store                   # Shows which tags name each stored version
```

`latest` cannot be deleted — it is what every lookup by name resolves through.
It is also not a garbage-collection root: only a tag you set keeps a version
alive, because `latest` moves onto everything a publish writes and treating it
as a root would leave `lnpm gc` unable to reclaim anything.

### Version History and Rollback

Publishing keeps the version published before it, so a release that breaks one
consumer can be undone for that consumer alone — no republishing an old source
tree from a git checkout:

```bash
lnpm list mylib --versions
# mylib versions:
#   HASH       VERSION        PUBLISHED            TAGS                 LINKED IN
#   -----------------------------------------------------------------------------
#   a1b2c3d4   1.3.0          2 hours ago          latest               ~/apps/myapp
#   9f8e7d6c   1.2.0          3 days ago

cd ~/apps/myapp
lnpm add mylib@9f8e7d6c   # Roll this project back to the build that worked
```

Either identifier the listing prints works. What follows the `@` is matched as a
dist-tag first, then as an exact version against every retained version, then as
a content-hash prefix of at least four characters. The eight characters the
listing shows are enough; type more of them if that ever names two builds. A spec
that names two builds is refused with their full hashes rather than resolved to
one of them.

The history is the set `lnpm gc` has not collected: a version stays for as long
as a link or a tag you set reaches it. A version a project has rolled back to is
linked, so `gc` keeps it — while the versions in between, which nothing reaches
any more, are reclaimed.

**A rollback pins the link, and nothing but you moves it off.** Naming a build —
either identifier — says *follow this build*, where naming a channel says *follow
this channel*, so a pinned project stays where you put it:

```bash
lnpm pull                 # Refreshes everything else; leaves mylib pinned and says so
lnpm pull mylib           # Refuses: mylib is pinned, and names the way out
lnpm publish              # In the package: moves latest, does not drag the pin along
lnpm gc                   # Keeps the pinned build, with no time limit
lnpm status               # Shows the pin next to the linked package

lnpm add mylib            # Unpin: follow latest again
```

A pin has no expiry. `lnpm gc` keeps the build for as long as the pin names it,
so reclaiming that space takes two deliberate steps — unpin, then collect —
exactly as it does for a version you tagged. `lnpm retreat` and `lnpm restore`
carry the pin, which travels in `lnpm.lock`; the field is optional, and a lock
file written before it existed reads as unpinned.

`lnpm add mylib@beta` does not pin, because a tag is a channel and following one
means being carried along it.

### Monorepo Support

lnpm integrates seamlessly with Turborepo, Nx, and all workspace managers:

```bash
# lnpm is installed globally (one-time system install)
# See installation section above

# Publish all workspace packages (from the workspace root)
lnpm publish --all

# Push updates for one package (from that package's own directory)
cd packages/ui
lnpm push
```

**`workspace:` specifiers are resolved on publish.** A sibling dependency written
with the `workspace:` protocol means nothing outside the workspace it came from,
so lnpm resolves it in the *stored* package.json — your own package.json is left
byte-for-byte as you wrote it. All four dependency maps are covered:
`dependencies`, `devDependencies`, `peerDependencies` and `optionalDependencies`.

| Specifier | Stored as (sibling at 2.3.0) |
|-----------|------------------------------|
| `workspace:*` | `2.3.0` |
| `workspace:latest` | `2.3.0` |
| `workspace:^` | `^2.3.0` |
| `workspace:~` | `~2.3.0` |
| `workspace:^1.2.3`, `workspace:1.2.3`, any explicit range | the range itself: `^1.2.3`, `1.2.3`, … |

Publishing fails instead if the sibling is not a package of the workspace, if the
package is not in a workspace at all, or on a bare `workspace:` — shipping the
literal specifier would only break the consumer's `npm install` later.

**📖 See [MONOREPO.md](MONOREPO.md) for complete guides on:**
- Turborepo integration
- Nx integration
- PNPM/NPM/Yarn workspaces
- Best practices and troubleshooting

### Before Publishing to npm

```bash
# Remove all lnpm links and restore original dependencies
lnpm retreat --force
npm install  # Restore original packages

# Guard: fails (non-zero) if any lnpm reference is still in package.json
lnpm check

npm publish
```

`lnpm check` reports what lnpm has left in the project that an `npm publish`
would carry into the tarball, and exits non-zero if there is any — drop it in a
`prepublishOnly` script or CI step. It looks for two things: leftover
`file:.lnpm/` or `link:.lnpm/` references in `package.json`, and the
`lnpm.lock.retreat` snapshot described below, when nothing in the project would
keep that snapshot out of the tarball.

In a monorepo the reference scan covers the workspace root's `package.json` and
every workspace package's, not only the manifest where you ran it, and each
finding names the package it came from. A workspace whose member list will not
resolve — a malformed pattern in the `workspaces` field, a member manifest that
will not read or parse, a member with no `name` — fails the check rather than
passing on the manifest at hand alone.

### After Publishing to npm

```bash
# Re-link everything the retreat removed
lnpm restore
```

`lnpm retreat --force` saves what it unlinked to `lnpm.lock.retreat`, and
`lnpm restore` links it all back. Packages added again in the meantime are kept
as they are; nothing is unlinked. Packages restored this way are store copies
(`file:.lnpm/<pkg>`) — re-run `lnpm add --link <pkg>` for the ones you want
pointed back at their live source. A second `lnpm retreat --force` merges into
`lnpm.lock.retreat` rather than replacing it, so an unfinished restore is not
lost.

`lnpm.lock.retreat` is lnpm's own state, and it records an absolute source path
for every package it lists. `lnpm publish` never packs it. `npm publish` knows
nothing about it, so keep it out of your tarball the way you keep any other file
out — a `files` field in `package.json`, or a line in `.npmignore` or
`.gitignore`. `lnpm check` fails if the snapshot is there and none of those
would stop it.

The snapshot records no `--dev` or `--pure` flag, so a package that had no
`package.json` entry before the retreat cannot get the same treatment back.
Restore writes it into `dependencies` and says so. For a `--pure` package that
entry is spurious — `--pure` exists to keep a package out of `package.json` —
so delete it after the restore.

### Shell Completions

**Quick setup (recommended):**
```bash
lnpm completion install
```

This auto-detects your shell and installs completions. Follow the on-screen instructions to enable.

**Alternative - Dynamic loading** (add to shell config):
```bash
# Zsh (~/.zshrc)
eval "$(lnpm completion zsh)"

# Bash (~/.bashrc)
eval "$(lnpm completion bash)"

# Fish (~/.config/fish/config.fish)
lnpm completion fish | source
```

**Manual installation:**
```bash
# Zsh
lnpm completion zsh > ~/.zsh/completions/_lnpm

# Bash
lnpm completion bash > /etc/bash_completion.d/lnpm

# Fish
lnpm completion fish > ~/.config/fish/completions/lnpm.fish
```

## Configuration

Configuration file: `~/.lnpm/config.yaml`

```yaml
# Custom store location (default: ~/.lnpm)
store_path: /path/to/store

# Link mode: "hardlink" (try reflink/hardlink) or "copy" (force copy)
# Default: hardlink
link_mode: hardlink

# Auto-manage .gitignore (add/remove .lnpm/ entry)
# Default: true
manage_gitignore: true

# Work through a node_modules — or a node_modules/@scope — that is not a real
# directory. Off by default: lnpm creates directories and deletes entries
# there, and a symlink committed to a repository aims both at whatever it
# points at. Turn it on only where the relocation is yours.
#
# It is named for the symlink because that is the case it exists for, but it
# switches the whole check off: a regular file or a device at either path is
# accepted too, exactly as it was before the check existed.
# Default: false
follow_symlinked_node_modules: false

# Build scripts and hooks
hooks:
  # Skip prepare scripts (prepare, prepublishOnly, prepack) on publish/push
  skip_prepare: false      # Default: false (run scripts)

  # Custom hooks (optional)
  pre_publish: npm run build    # Runs before publish/push packs files
  post_publish: echo done       # Runs after publish/push completes
  # Runs only when `lnpm add --install` is used; add does NOT install by default
  post_add: npm install
```

View/modify config:

```bash
lnpm config                     # Show all
lnpm config store_path          # Get value
lnpm config store_path /data    # Set value
lnpm config --path              # Show config file path
lnpm config --edit              # Open config file in $EDITOR
```

Environment overrides: `LNPM_STORE` (store location, wins over `store_path`), `LNPM_CONFIG` (config file path), `LNPM_DEBUG` (debug logging).

## Build Workflows & Hooks

lnpm automatically runs your package's build (prepare) scripts before publishing so linked output is up to date. Dependency installation after `add` is opt-in via `--install` (see below).

### Prepare Scripts

Before packing, lnpm runs every one of these scripts that your `package.json` defines, always in this order:

1. `prepublishOnly` — `publish` only, as with npm
2. `prepack` — Runs before packing files
3. `prepare` — General build script

`lnpm publish` runs all three, the set `npm publish` runs. `lnpm push` runs `prepack` and `prepare`, the set `npm pack` runs: a push is a pack, so a build or a slow gate kept in `prepublishOnly` stays out of the push loop.

If a script fails, the ones after it do not run and the command stops.

The order is npm's own, so a package that builds under npm builds the same way under lnpm. `prepack` before `prepare` matters when one reads what the other wrote.

**Example package.json:**
```json
{
  "name": "my-library",
  "scripts": {
    "build": "tsc",
    "prepare": "npm run build"
  }
}
```

**Commands:**
```bash
lnpm publish              # Runs prepublishOnly, prepack, prepare, then publishes
lnpm push                 # Runs prepack, prepare, then pushes
lnpm publish --skip-hooks # Skip prepare scripts
lnpm push --skip-hooks    # Re-push what is on disk without rebuilding
```

### Post-Add Install (opt-in)

By default, `lnpm add` does NOT run `npm install` (matching yalc). Pass `--install` to have lnpm run your package manager after adding, to resolve peer dependencies and install the linked package's dependencies.

```bash
lnpm add my-package             # Adds package, no install (default)
lnpm add my-package --install   # Adds package, then runs npm install
```

`lnpm remove` and `lnpm retreat` take the same flag, and default the same way. An install runs every dependency's install scripts, so none of these commands starts one unless you ask for it.

```bash
lnpm remove my-package             # Removes package, no install (default)
lnpm remove my-package --install   # Removes package, then runs npm install
```

### Package Validation

lnpm validates packages before publishing:
- Checks `package.json` has `name` and `version`
- Verifies `main` entry point exists (if declared)

```bash
lnpm publish                  # With validation
lnpm publish --skip-validation # Skip validation (for broken packages)
```

### TypeScript Workflow Example

```bash
# In your TypeScript library
cd ~/code/my-library
# package.json has "prepare": "tsc"

lnpm publish    # Automatically builds TypeScript → dist/

# In your app
cd ~/code/my-app
lnpm add my-library   # Links only (pass --install to also run npm install)

# Make changes to library
cd ~/code/my-library
# Edit src/index.ts
lnpm push       # Rebuilds and updates all linked projects
```

### Troubleshooting

**Husky/git hooks fail during npm install:**
- lnpm strips `prepare` and `prepublish` scripts from stored packages (like yalc)
- If you see husky errors, re-publish the package: `lnpm publish`

**Duplicate React or other peer dependency issues:**
- Don't use `--install` flag; run `npm install` manually with your preferred flags
- Or configure npm: `npm config set legacy-peer-deps true`

**npm ERESOLVE peer dependency errors:**
- npm has a bug where `file:` dependencies show as `@undefined` ([npm/cli#2199](https://github.com/npm/cli/issues/2199))
- When using `--install`, lnpm uses `--legacy-peer-deps` automatically
- Or run `npm install --legacy-peer-deps` manually

**Retreat restores wrong version:**
- Fixed in latest version - lnpm now properly tracks original versions
- If stuck, manually edit package.json and delete lnpm.lock

## Debugging

Enable debug mode for verbose logging:

```bash
# Flag
lnpm --debug publish
lnpm -d status

# Environment variable
LNPM_DEBUG=1 lnpm publish
```

Debug output goes to stderr with timestamps, useful for diagnosing slow operations or unexpected behavior.

## How It Works

1. **Publish** — Selects files with lnpm's own filtering (see [File Filtering](#file-filtering)), strips lifecycle scripts (`prepare`/`prepublish`), resolves `workspace:` dependency specifiers to versions npm can install (see [Monorepo Support](#monorepo-support)), then reflinks or copies to `~/.lnpm/store/{name}/{hash}/`
2. **Add** — Creates reflinks or hard links from store to `project/.lnpm/{package}/`, updates package.json to `file:.lnpm/{package}`
3. **Symlink** — Links `node_modules/{package}` → `.lnpm/{package}`
4. **Push** — Updates store and re-links all files to consuming projects
5. **Auto .gitignore** — Optionally manages `.lnpm/` in `.gitignore` (enabled by default)

**Note:** `lnpm add` does NOT run `npm install` automatically (matches yalc) — pass `--install` or run it yourself if you need to resolve peer dependencies. `lnpm remove` and `lnpm retreat` behave the same way: pass `--install` to have the removed dependency reinstalled.

```
Source Package          Store                    Project
──────────────          ─────                    ───────
src/index.ts    ──►    (reflink/copy)
dist/index.js   ──►     ~/.lnpm/store/      ══► .lnpm/pkg/     ──► node_modules/pkg
package.json    ──►       pkg/abc123/      (reflink/hardlink)     (symlink)
```

### File Filtering

lnpm decides which files to publish in Go — it does not shell out to the `npm` CLI. The rules are closely modeled on npm's conventions:

- Respects `package.json` `files` field. A file it selects is published whatever the ignore files say, as it is for npm — a `.gitignore` holding `dist/` out of version control does not stop `files: ["dist"]` publishing the build output
- A `files` entry globs with the same engine ignore patterns use, so `**` means one thing across the whole tool: `*` matches within one path segment and `**` spans zero or more of them. `files: ["lib/**/*.js"]` selects `lib/top.js` as well as `lib/sub/a.js`, and a bare `["**"]` selects every path in the package. Entries are matched against the full path and never against a basename, as npm's are — `["guide.md"]` selects nothing when the file is `docs/guide.md`. A leading `/` or `./` and a trailing `/` are normalized away, a run of `/` at either end included, so `["dist"]`, `["/dist"]`, `["dist/"]`, `["/dist/"]`, `["./dist"]`, `["//dist"]`, `["dist//"]` and `["//dist//"]` all mean the same thing; a trailing `/` on a glob is not a directory marker and is left alone, so `["dist/**/"]` selects nothing, which is what npm does with it
- An entry whose **last segment is a bare wildcard** expands every directory it matches into that directory's whole subtree, as npm does: `["dist/*"]` selects `dist/a.js`, `dist/cli/index.js` and `dist/cli/deep/z.js`, and a bare `["*"]` selects the entire tree rather than only the root's own files. The reach belongs to the last segment and to nothing else, so the rule is narrower than "an entry that matches a directory expands it" — `["d*"]` and `["dist/c*"]` each select nothing but the always-included set, though `d*` matches the directory `dist` and `dist/c*` matches `dist/cli`. A globbed *prefix* is fine, so `["di*/*"]` expands the `dist` subtree; the expansion never reaches a root file, so `["*/*"]` selects `lib/keep.js` and the whole `dist` subtree and leaves a root `index.js` out. All six entries were run against npm 11.16.0 with `npm pack --dry-run --json` on one fixture package holding exactly the five files named above, and lnpm's packed set came back identical to npm's for every one, so this is parity rather than an approximation of it. Sweeping a directory in is still not naming anything in it, so `["*"]` reaches a `dist/.env` and does not publish it, for the same reason `["dist"]` does not
- Honors a `.npmignore` in every directory, falling back per directory to a `.gitignore` where there is no `.npmignore` beside it. An ignore file governs everything below it — it cannot exclude the directory holding it, just as a `src/.gitignore` cannot exclude `src` — and its patterns are resolved against that directory — `/lib/gen.js` in `src/.npmignore` names `src/lib/gen.js`, not `lib/gen.js` at the package root. They decide every path the `files` field does not select, including the always-included set below — the two exceptions being the package root's own `package.json`, which nothing can exclude, and the file named by `main`, which a `files` field force-includes past them
- Where two ignore files both match a path, the deeper one has the last word, as it does in git: a root `*.txt` plus `!keep.txt` in `src/.npmignore` publishes `src/keep.txt`
- Supports gitignore pattern syntax — anchoring to the ignore file's own directory (`/dist`), directory patterns (`dist/`) and negation (`*.txt` followed by `!keep.txt`), with the last matching pattern deciding. `*` matches within one path segment and `**` spans zero or more of them, so `**/*.pem` excludes a key at any depth including the package root, and `src/**/*.key` excludes one at any depth under `src`
- Both matchers additionally expand brace alternation (`*.{pem,key}`), which git does not and npm's minimatch does. The trade is that `{` and `}` are no longer literal characters in a pattern: `weird{a,b}.txt` matches `weirda.txt` and `weirdb.txt` rather than the file literally named `weird{a,b}.txt`. On the ignore side that direction fails open, and the escape hatch is a pattern spelling out the full path (`/src/weird{a,b}.txt`), which is compared as a string before any globbing; a bare basename pattern no longer reaches it. On the `files` side the same escape hatch means a literal-brace entry still selects its file, since a `files` entry is only ever compared against the full path — so `files: ["weird{a,b}.txt"]` selects `weirda.txt`, `weirdb.txt` and the literal file. `docs/adr/0003` records both directions
- Applies two built-in exclusion lists, which differ in whether you can override them.
  - **Never published, by any route.** VCS metadata (`.git`, `.hg`, `.svn`, `CVS`), git's own metadata files (`.gitignore`, `.gitattributes`, `.gitmodules`), `.npmrc`, `node_modules`, `.DS_Store`, `*.orig`, lockfiles (`package-lock.json`, `yarn.lock`, `pnpm-lock.yaml`, `bun.lockb`), and lnpm and yalc state (`.lnpm/`, `.yalc/`, `lnpm.lock`, `lnpm.lock.retreat`, `yalc.lock`). Nothing re-includes these — not a `!` pattern, not a `files` entry, not `main`. Naming one in `files`, or negating it in a root ignore file, prints a warning saying so rather than dropping it in silence. The membership rule follows npm's "cannot be included even if specified in the files globs" list, plus the lnpm and yalc state files, which record absolute paths from the machine that published, plus git's own metadata files, which lnpm has always stripped from a finished tarball and now refuses out loud. It is not identical to npm's: lnpm is stricter on `.hg`, `.svn`, `CVS`, `.DS_Store` and `*.orig`, which npm ignores by default but will include for a `files` glob, and stricter again on `.gitignore`, `.gitattributes` and `.gitmodules`, which are on neither npm list at all. On the lockfiles what matches npm is the membership: all four names are on npm's short list, so neither tool publishes one for a `files` glob. lnpm holds them back at any depth and in every mode; npm's own list is a list of names and does not say how far it reaches into subdirectories, so read the parity as being about which names are unpublishable rather than about matching behaviour path for path. The reason for the entry is that a lockfile records the URL each dependency resolved from, and so publishes a private registry's hostname along with the names and versions of everything private in it. One lockfile is deliberately not on the list: Bun 1.2 and later write a text `bun.lock` beside — or instead of — the binary `bun.lockb`, npm's short list names only `bun.lockb`, and lnpm follows npm's membership rather than extending it. So a `bun.lock` publishes like any ordinary file, at the package root and nested, even though lnpm does recognise the name when it works out which package manager a project uses
  - **Excluded unless you say otherwise.** `.npmignore`, `Thumbs.db`, editor backups (`*.swp`, `*.swo`, `*~`), `*.log`, `*.tgz`, and exactly `.env` and `.env.*`. A `files` entry naming the path, or an `!` negation in an ignore file, publishes it — so `"files": [".env.example"]` ships the template, which is what the list is for. Which of the two applies depends on the package. With a `files` field your ignore files stop being consulted for the paths the whitelist selects — that is the same rule that lets `files` beat a `.gitignore` — so `"files": ["dist"]` plus `!dist/.env` still keeps `dist/.env` out, and naming the path in `files` is the way in. The one exception is a file that is also in the always-included set below: a root `readme.log` or `README.md~` still goes through your ignore rules even under a `files` field, so `!readme.log` publishes it. The entry has to name the path: `"files": ["dist"]` does **not** publish `dist/.env`, `dist/app.log` or `dist/pkg.tgz`, because naming a build directory is not a statement about what landed inside it. The same holds one level up — `"files": ["src"]` does not publish `src/.env/config`, since a directory this list covers keeps its contents out too. What counts as naming is a glob's last segment: `"files": ["dist/.env"]`, `["dist/*.env"]` and `[".env.*"]` all publish, because each says something about the name, while `["dist/*"]`, `["dist/**"]` and a bare `["*"]` do not, because a lone wildcard sweeps a directory rather than naming anything in it — the same reason `[""]` and `["."]` do not. Paths the list does not cover are unaffected — `dist/a.js` ships for `"files": ["dist"]` as it always has. `main` is not a third way in: naming a path in `main` says where the entry point is, not that you meant to publish a `.env`, so a `main` this list covers loses to it and you get a warning instead (`docs/adr/0004`) — unless `files` also names that path directly, in which case the `files` entry is the consent and it ships. `.npmignore` is the only ignore file on this list: `.gitignore`, `.gitattributes` and `.gitmodules` are on the never-published list above, so naming one in `files` warns instead of publishing it. Publishing an `.npmignore` is harmless — it describes what was left out, not a secret — so that one really does publish when you name it
  - Both lists are literal, not prefixes: `.envrc` matches neither `.env` nor `.env.*`, so it is published. Both match case-insensitively on every platform, so `.ENV` and `Node_Modules/` are held back exactly as their lowercase forms are
- **Additional safety**: Explicit `.git` filtering prevents any VCS files from being linked
- **Symlinks are skipped, never followed** — a symlink inside a package can't pull files from outside it (e.g. `~/.ssh`) into the store
- Package names are restricted to a plain name (`my-pkg`) or a single scope (`@org/my-pkg`), because the name is joined into the store path. Everything else is rejected: a second `/`, a single `/` whose first segment is not a scope, `.` or `..` segments, absolute paths, backslashes and NUL bytes

Because the filtering is lnpm's own, some cases differ from `npm publish`:

- The exclusion lists are not npm's. lnpm additionally excludes `.env`/`.env.*`, logs and `*.tgz` by default. Those additions are the overridable kind, so a manifest naming one directly in `files`, or negating it in an ignore file, still publishes it — a divergence in both directions, since npm never ignores `.env` and so publishes `dist/.env` for `"files": ["dist"]` where lnpm does not. `.npmignore` is on that same overridable list and is **not** one of the additions, which was measured rather than assumed: against npm 11.16.0, a package with no `files` field leaves `.npmignore` out of the tarball under both tools, and `"files": [".npmignore"]` publishes it under both. Git's own metadata files — `.gitignore`, `.gitattributes` and `.gitmodules` — are the deliberate exception: they are on lnpm's never-published list, where nothing overrides them, and they are on neither of npm's two lists, so `npm publish` ships a `.gitignore` for `"files": [".gitignore"]` — measured against npm 11.16.0, which packs all three when they are named — and `lnpm publish` warns and leaves it out. The same npm runs settle the no-`files` case, where the three do not behave alike: npm packs `.gitattributes` and `.gitmodules` and drops the `.gitignore` it read as ignore rules, while lnpm holds back all three. The reason is that a `.git` safety pass has always stripped all three from the finished tarball whatever the selection rules decided, so the overridable tier was a claim the code did not honour; naming the tier the code enforces is what makes the refusal something lnpm can tell you about. The lockfiles (`package-lock.json`, `yarn.lock`, `pnpm-lock.yaml`, `bun.lockb`) are not a divergence in membership: all four are on npm's "cannot be included even if specified in the files globs" list, and lnpm carries all four on the never-overridable list above, so neither tool publishes one for a `files` glob. That is a statement about which names are unpublishable, not a claim that the two match path for path — the list above says what the quote does and does not settle. Bun 1.2+'s `bun.lock` is the exception: neither of npm's lists names it, so lnpm's does not either, and an `lnpm publish` ships it. `lnpm.lock` and `yalc.lock` are lnpm's and yalc's own state rather than package-manager lockfiles, and are on that list for their own reason — they record absolute paths from the machine that published
- Case sensitivity splits by who wrote the rule. Both built-in lists fold case on every platform, because they are guards rather than preferences and `.ENV` is the same file as `.env` on macOS and Windows; patterns you write in a `.npmignore` or `.gitignore` never fold, so `secret.txt` there does not exclude `SECRET.TXT`. git splits it the other way round — it folds ignore matching whenever `core.ignorecase` is set, and `git init` and `git clone` probe the filesystem and set it on a case-insensitive one, which is what macOS and Windows default to. So on those two platforms a pattern that hides a file from git may **not** withhold it from an `lnpm publish`: `secret.txt` in a `.npmignore` leaves `SECRET.TXT` in the tarball. That direction is fail-open, and deliberate: lnpm has no per-project `core.ignorecase` to condition on
- With no `files` field, an excluded directory is pruned during the walk, so a negation can't reach back into it: `dist/` plus `!dist/keep.js` publishes neither file. npm diverges in the opposite direction — the negation defeats the `dist/` rule entirely and npm publishes the whole directory, `dist/other.js` included. Spelled `dist/*` instead, the negation does what it looks like in both. A `dist/.npmignore` cannot re-include anything out of an excluded `dist` either, and that part does not depend on the `files` field: git never descends into an ignored directory, so an ignore file inside one is never consulted
- A `files` entry with a literal `{`, `}` or `[` in it still selects the file of that name, because the entry is compared against the full path as a string before any globbing. npm has no such escape and reads only the expanded form: `["weird{a,b}.txt"]` selects `weirda.txt`, `weirdb.txt` and the literal `weird{a,b}.txt` here, where npm ships only the first two. The subtree form inverts npm outright — `["weird{a,b}/**"]` selects the literal directory's contents and not `weirda/`'s, and npm does the reverse
- The always-included set differs in what is in it: lnpm adds `CHANGELOG`, `CHANGES` and `HISTORY`, and does not automatically include the files named by `bin`. Those three additions are matched by name rather than as prefix globs — each is accepted with no extension or with `.md`, `.markdown`, `.txt` or `.rst`, and nothing else, so a root `history.db` is left to `files` and your ignore rules like any other path. npm's own `README*`, `LICENSE*` and `LICENCE*` stay prefix globs here because they are prefix globs for npm too, which does mean a root `readme.db` ships past a `files` whitelist on both — measured against npm 11.16.0, which under `"files": ["dist"]` packs a root `readme.db` and leaves a root `history.db` out. Two entries beat your own `.npmignore` and `.gitignore` as well as the `files` whitelist; everything else in the set is exempt from the whitelist only. The package root's own `package.json` cannot be excluded by anything — no ignore pattern and no `files` field drops it, and a pack that somehow ends up without it fails rather than publishing a package no consumer can resolve; `docs/adr/0005` records the reasoning and the conflict with `docs/adr/0001` that it accepts. The file named by `main` is included as it is for npm, and it also beats your ignore files, but it still loses to both built-in exclusion lists, only ever selects the one path it names, and does nothing at all in a package with no `files` field; `docs/adr/0004` records the reasoning and the conflict with `docs/adr/0001` that it accepts. Both exemptions are anchored at the package root, so a nested `sub/package.json` is an ordinary file your patterns still govern. Where the set applies is not a divergence: it is matched at the package root only, as npm's is, so a root `README.md` ships whatever `files` says while a `docs/README.md` is left to `files` and the ignore rules like any other path

### Automatic .gitignore Management

lnpm automatically manages `.gitignore` entries to prevent committing linked packages (enabled by default, configurable via `manage_gitignore: false`):

- **On `lnpm add`** — Adds `.lnpm/` to `.gitignore` with marker comment `# Added by lnpm`
- **On `lnpm retreat`** — Removes `.lnpm/` entry and marker from `.gitignore`
- **Smart detection** — Skips if pattern already exists
- **Atomic writes** — Uses temp file + rename to prevent corruption
- **Configurable** — Set `manage_gitignore: false` in config to disable

### Smart Linking Strategy

lnpm uses an intelligent priority system for maximum performance:

**Priority Order:**
- Source → store: Reflink → Parallel Copy
- Store → project: Reflink → Hard Link → Parallel Copy

The store never hard links your source files. A hard link would make the source
file and the store entry share one inode, so rewriting the source in place (tsc,
webpack, an editor saving over its own output) would silently mutate content
already filed under its content hash. The store keeps a private copy instead.

| Method | Speed | Platform Support | Disk Usage |
|--------|-------|------------------|------------|
| **Reflink (CoW)** | Instant | macOS APFS, Linux Btrfs/XFS | Zero (copy-on-write) |
| **Hard Link** | Instant | Same filesystem, store → project only | Zero (shared inodes) |
| **Parallel Copy** | Fast | Always works | Full copy (8 workers) |

**Benefits:**
- ⚡ **10,000+ files?** Instant with reflink or hard links
- 🔄 **Copy-on-write** — Modify files safely without affecting store
- 🚀 **Parallel copying** — 4-8x faster when copying is needed
- 💾 **Space efficient** — No duplication on modern filesystems

lnpm automatically detects the best method and falls back gracefully with helpful warnings.

## Comparison with yalc

| Feature | lnpm | yalc |
|---------|------|------|
| Link method | Reflink → Hard link → Parallel copy (reflink or copy into the store) | Sequential copy only |
| Large packages (10k+ files) | Instant (~5ms) on APFS/Btrfs | Slow (~5s+) |
| State tracking | bbolt database | Hash files |
| Monorepo | Native support | Manual |
| Speed | ~10ms startup | ~100ms startup |
| Cross-filesystem | Parallel copy (4-8x faster) | Sequential copy |
| Visibility | `lnpm status` | Limited |

## Benchmarks

### lnpm vs yalc (100 files)

| Operation | lnpm | yalc | Speedup |
|-----------|------|------|---------|
| `publish` | **75ms** | 164ms | 2.2x |
| `add` | **71ms** | 251ms | 3.5x |
| `push` | **90ms** | 385ms | 4.3x |

### Detailed benchmarks (Apple M1 Pro, APFS)

| Operation | Files | Time | Memory |
|-----------|-------|------|--------|
| `publish` | 100 | **17ms** | 3.5 MB |
| `add` | 100 | **48ms** | 340 KB |
| `push` (1 project) | 100 | **146ms** | 3.8 MB |
| `push` (5 projects) | 100 | **183ms** | 4.5 MB |
| `publish` | 500 | **41ms** | 17 MB |

> **Note:** These figures are illustrative — measured once on the machine above. The `vs yalc` and memory numbers are not reproduced by the committed `scripts/benchmark-compare.sh` (which measures wall-clock only). Run the benchmarks yourself for numbers on your hardware.

Run benchmarks yourself:
```bash
make bench          # Go benchmarks
make bench-mem      # With memory stats
make bench-compare  # Compare vs yalc (if installed)
```

## Platform Support

- **Linux** — amd64, arm64
- **macOS** — amd64 (Intel), arm64 (Apple Silicon)
- **Windows** — amd64

## Requirements

- Go 1.26+ (for building from source)
- Node.js project with `package.json`

## Contributing

Contributions are welcome!

### Prerequisites

- Go 1.26+
- Node.js (for integration tests that invoke npm)

### Setup & Testing

**Linux / macOS:**

```bash
git clone https://github.com/pedrosousa13/lnpm.git
cd lnpm
make deps
make build
make test              # all tests
make test-coverage     # with coverage report
make lint              # golangci-lint (falls back to go vet)
```

**Windows (PowerShell):**

```powershell
git clone https://github.com/pedrosousa13/lnpm.git
cd lnpm
go mod download
go build ./cmd/lnpm
go test -v ./...
go vet ./...
```

### Cross-compile check

Verify your changes compile for all platforms:

```bash
GOOS=linux go build ./...
GOOS=darwin go build ./...
GOOS=windows go build ./...
```

### Running specific test suites

```bash
go test -v -race ./internal/...   # unit tests
go test -v -race ./tests/...      # integration tests
```

> **Windows note:** Some symlink tests are skipped on Windows since lnpm uses NTFS junctions (absolute paths) instead of relative symlinks. This is expected.

## License

MIT License — see [LICENSE](LICENSE) for details.

---

## Disclaimer

> **Note:** This project was built with assistance from Claude (Anthropic's AI assistant).
> The architecture, implementation, and documentation were developed through an AI-assisted
> development process. While the code has been reviewed and tested, users should evaluate
> it according to their own requirements and standards before use in production environments.

---

## Acknowledgments

- Inspired by [yalc](https://github.com/wclr/yalc)
- Built with [Cobra](https://github.com/spf13/cobra) for CLI
- Uses [bbolt](https://github.com/etcd-io/bbolt) for state storage
