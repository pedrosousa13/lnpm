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
- **[Roadmap](ROADMAP.md)** — What lnpm does today, and where planned work is tracked

## Commands

| Command | Description |
|---------|-------------|
| `lnpm publish` | Publish package to local store |
| `lnpm add <pkg...>` | Add package(s) from store to project |
| `lnpm remove <pkg>` | Remove linked package |
| `lnpm pull [pkg...]` | Sync linked packages with the store (all of them when no name is given) |
| `lnpm push` | Push changes to all linked projects (`--tag` picks the channel) |
| `lnpm status` | Show all published packages and active links across every project |
| `lnpm list` | List this project's linked packages (`--store` lists the store, `--projects` lists consumers) |
| `lnpm tag <pkg> <tag>` | Point a dist-tag at a published package (`--delete` removes one) |
| `lnpm check` | Fail if lnpm has left anything an `npm publish` would ship (pre-publish guard) |
| `lnpm doctor` | Diagnose issues |
| `lnpm gc` | Garbage collect unused packages (`--dry-run`, `--older-than 30d`, `--fix-links`, `--yes`) |
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

What follows the `@` is read as a tag first and as an exact version only if no
tag by that name is set. A project that added a package under a tag keeps
following it: `lnpm pull` refreshes it to whatever that tag now names, never to
`latest`. Switching channels is just another `lnpm add` — `lnpm add
my-package@beta` in a project already on `latest` moves it over, and dropping the
tag moves it back. A tag cannot be combined with `--link`, which resolves to the
source directory rather than to any published build.

`lnpm push` goes to the channel the build already in the store carries, so
pushing an unchanged pre-release keeps it a pre-release. Content the store does
not hold yet is in no channel and goes to `latest`; pass `--tag` to say
otherwise.

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

Before publishing or pushing, lnpm runs every one of these scripts that your `package.json` defines, always in this order:

1. `prepublishOnly` — Runs only on publish
2. `prepare` — General build script
3. `prepack` — Runs before packing files

If a script fails, the ones after it do not run and the command stops.

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
lnpm publish              # Runs prepare, then publishes
lnpm push                 # Runs prepare, then pushes
lnpm publish --skip-hooks # Skip prepare scripts
```

### Post-Add Install (opt-in)

By default, `lnpm add` does NOT run `npm install` (matching yalc). Pass `--install` to have lnpm run your package manager after adding, to resolve peer dependencies and install the linked package's dependencies.

```bash
lnpm add my-package             # Adds package, no install (default)
lnpm add my-package --install   # Adds package, then runs npm install
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

**Note:** `lnpm add` does NOT run `npm install` automatically (matches yalc) — pass `--install` or run it yourself if you need to resolve peer dependencies. `lnpm remove`, however, runs your package manager's install afterward to restore the removed dependency.

```
Source Package          Store                    Project
──────────────          ─────                    ───────
src/index.ts    ──►    (reflink/copy)
dist/index.js   ──►     ~/.lnpm/store/      ══► .lnpm/pkg/     ──► node_modules/pkg
package.json    ──►       pkg/abc123/      (reflink/hardlink)     (symlink)
```

### File Filtering

lnpm decides which files to publish in Go — it does not shell out to the `npm` CLI. The rules are closely modeled on npm's conventions:

- Respects `package.json` `files` field
- Honors a root `.npmignore`, falling back to a root `.gitignore` (ignore files in subdirectories are not read)
- Supports gitignore pattern syntax — root anchoring (`/dist`), directory patterns (`dist/`) and negation (`*.txt` followed by `!keep.txt`), with the last matching pattern deciding
- Applies a built-in exclusion list — VCS metadata and ignore files (`.git`, `.hg`, `.svn`, `CVS`, `.gitignore`, `.gitattributes`, `.npmignore`, `.npmrc`), `node_modules`, OS cruft (`.DS_Store`, `Thumbs.db`), editor and merge backups (`*.swp`, `*.swo`, `*~`, `*.orig`), lnpm and yalc state (`.lnpm/`, `.yalc/`, `lnpm.lock`, `lnpm.lock.retreat`, `yalc.lock`), `*.log`, `*.tgz`, and exactly `.env` and `.env.*` — that a `!` pattern cannot re-include. The patterns are literal, not prefixes: `.envrc` matches neither `.env` nor `.env.*`, so it is published
- **Additional safety**: Explicit `.git` filtering prevents any VCS files from being linked
- **Symlinks are skipped, never followed** — a symlink inside a package can't pull files from outside it (e.g. `~/.ssh`) into the store
- Package names are restricted to a plain name (`my-pkg`) or a single scope (`@org/my-pkg`), because the name is joined into the store path. Everything else is rejected: a second `/`, a single `/` whose first segment is not a scope, `.` or `..` segments, absolute paths, backslashes and NUL bytes

Because the filtering is lnpm's own, some cases differ from `npm publish`:

- The default exclusion list is not npm's. lnpm additionally drops `.env`/`.env.*`, logs and `*.tgz`, and does not drop the lockfiles (`package-lock.json`, `yarn.lock`, `pnpm-lock.yaml`) that npm always ignores
- Ignore patterns are applied before the `files` whitelist, so a root `.npmignore` can drop a file that `files` listed; for npm the `files` field wins
- An excluded directory is pruned during the walk, so a negation can't reach back into it: `dist/` plus `!dist/keep.js` publishes neither file. npm diverges in the opposite direction — the negation defeats the `dist/` rule entirely and npm publishes the whole directory, `dist/other.js` included. Spelled `dist/*` instead, the negation does what it looks like in both
- Entries in `files` are matched against the full path and never against a basename, so `**/*.md` picks up `docs/guide.md` but misses `top.md` at the package root, which npm includes
- The always-included set differs: lnpm adds `CHANGELOG*`, `CHANGES*` and `HISTORY*`, and does not automatically include the files named by `main` and `bin`. Those files are exempt from the `files` whitelist only — an ignore pattern still drops them

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
