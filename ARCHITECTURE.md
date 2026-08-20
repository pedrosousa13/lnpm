# lnpm - Local NPM Package Development Tool

> A faster, more reliable alternative to yalc for local package development.

> **Note:** This project was built with AI assistance (Claude by Anthropic).

## Design Principles

1. **Speed** - Go for fast startup, hard links for instant syncing
2. **Visibility** - bbolt-backed state, always know what's linked where
3. **Reliability** - Hard links avoid symlink resolution issues
4. **Simplicity** - Minimal commands, sensible defaults
5. **Universal** - Works with npm, yarn, pnpm, and bun

---

## Technical Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                           lnpm CLI (Go)                             │
│         Commands: publish | add | remove | push | status    │
└──────────────────────────────────┬──────────────────────────────────┘
                                   │
                    ┌──────────────┴──────────────┐
                    │                             │
                    ▼                             ▼
           ┌───────────────┐            ┌─────────────────┐
           │ Package Store │            │  Link Manager   │
           │ ~/.lnpm/store │            │  (hard links)   │
           └───────┬───────┘            └─────────────────┘
                                  │
                                  ▼
                          ┌───────────────┐
                          │   bbolt DB    │
                          │ ~/.lnpm/lnpm.db│
                          └───────────────┘
```

### Directory Structure

```
~/.lnpm/
├── lnpm.db                      # bbolt database (key-value store)
├── config.yaml                  # Global config (optional)
├── store/                       # Package store
│   └── {package-name}/
│       └── {content-hash}/      # Versioned by content hash
│           ├── package.json
│           ├── dist/
│           └── ...

project/
├── .lnpm/                       # Local package cache (hard linked)
│   └── {package-name}/
│       └── ...
├── lnpm.lock                    # Lock file (YAML)
├── node_modules/
│   └── {package-name} -> ../.lnpm/{package-name}  # Symlink to .lnpm
└── package.json                 # Modified with file: protocol
```

---

## Intelligent Linking Strategy

### Priority System

lnpm uses a sophisticated multi-tier approach for maximum performance. The tiers
differ between the two hops:

- **Source → store** (`publish`, `push`): reflink → parallel copy. The store
  **never** hard links a source file. A hard link would give the source file and
  the store entry one shared inode, so an in-place rewrite of the source (tsc,
  webpack, an editor saving over its own output) would mutate content already
  filed under its content hash. The store must own a private copy of its bytes.
- **Store → project** (`add`, `push`): reflink → hard link → parallel copy. The
  store entry is already a private copy, so sharing its inode with consumers is
  safe as long as consumers treat linked packages as read-only.

| Method | Speed | Platform Support | Use Case | Disk Usage |
|--------|-------|------------------|----------|------------|
| **Reflink (CoW)** | Instant (~1ms) | macOS APFS, Linux Btrfs/XFS/OCFS2 | Primary method, both hops | Zero (copy-on-write) |
| **Hard Link** | Instant (~1ms) | Same filesystem (all platforms) | Fallback #1, store → project only | Zero (shared inodes) |
| **Parallel Copy** | Fast (~100ms for 10k files) | All platforms, cross-filesystem | Fallback #2 | Full copy |

### Reflink (Copy-on-Write Cloning)

**New in v1.2.0** — Revolutionary performance for modern filesystems.

#### What is Reflink?
- Creates an instant "clone" of a file without copying data
- Uses copy-on-write (CoW): data is only copied when modified
- Provides copy semantics with link performance
- **Safe**: modifications don't affect the original

#### Platform Implementation
- **macOS APFS**: Uses the `clonefile()` system call, via `golang.org/x/sys/unix`
- **Linux Btrfs/XFS**: Uses `FICLONE` ioctl (#0x40049409)
- **Other filesystems**: Gracefully falls back to hard links (store → project) or to a parallel copy (source → store)

#### Why Reflink is Better Than Hard Links
```
Hard Link:     source.js ════► [inode 12345] ◄════ dest.js
               (both point to same inode - modifications affect both)

Reflink:       source.js ──► [inode 12345: blocks 1-100]
               dest.js   ──► [inode 67890: blocks 1-100] (CoW clone)
               (separate inodes, shared blocks until modified)
```

### How It Works

#### During `lnpm publish`:
1. Try reflink first (works even across directories on same FS)
2. If reflink is unsupported, use parallel copy with 8 worker goroutines
3. Hard linking from the source is never attempted — see the priority system above
4. Display progress for user visibility

#### During `lnpm add`:
1. Check user config for `link_mode` preference
2. Try reflink from store to project (instant, safe)
3. If reflink unsupported, try hard link (instant, requires same FS)
4. If hard link fails, fall back to parallel copy
5. Warn user about fallback with optimization tips

```
Source Package          Store                    Project
──────────────          ─────                    ───────
src/index.ts    ──►    (reflink/copy)
dist/index.js   ──►     ~/.lnpm/store/      ══► .lnpm/pkg/     ──► node_modules/pkg
package.json    ──►       pkg/abc123/      (reflink/hardlink)     (symlink)
                       (private copy)       (CoW or shared)
```

### Performance Characteristics

**10,000 file package:**
- Reflink (APFS/Btrfs): **~5ms** ⚡
- Hard link (same FS): **~10ms** ⚡
- Sequential copy: **~5000ms** 🐌
- Parallel copy (8 workers): **~800ms** 🚀

**Result: Up to 1000x faster** for large packages!

### Cross-Filesystem & Fallback Handling

lnpm intelligently handles edge cases:

1. **Source → store**: Try reflink → parallel copy (hard link never attempted)
2. **Store → project, same filesystem**: Try reflink → hard link → parallel copy
3. **Store → project, different filesystem**: Try reflink → parallel copy (skip hard link)
4. **User config `link_mode: copy`**: Skip linking, use parallel copy
5. **User config `link_mode: hardlink`**: Store → project tries reflink → hard link → parallel copy

**User Feedback:**
```bash
# Large packages report progress while the store is populated
  Processing... 10000/10000 files
  Copying... 10000/10000 files

# Store → project hard linking not available
  ⚠ Hard linking failed, falling back to copying files
```

---

## Database Schema (bbolt)

lnpm uses [bbolt](https://github.com/etcd-io/bbolt), an embedded key-value database (same as used by etcd).

### Buckets

```
packages           - Package data (key: ID, value: JSON)
packages_by_name   - Index (key: name, value: ID)
projects           - Project data (key: ID, value: JSON)
projects_by_path   - Index (key: path, value: ID)
links              - Link data (key: ID, value: JSON)
links_by_package   - Index (key: package_id, value: [link_ids])
links_by_project   - Index (key: project_id, value: [link_ids])
files              - File manifest (key: package_id, value: [FileEntry])
meta               - Metadata (next_id counter)
```

### Data Structures (JSON)

```json
// Package
{
  "id": 1,
  "name": "my-package",
  "version": "1.0.0",
  "content_hash": "abc123...",
  "source_path": "/path/to/source",
  "store_path": "/home/user/.lnpm/store/my-package/abc123",
  "files_count": 42,
  "total_size": 123456,
  "created_at": "2024-01-15T10:00:00Z",
  "updated_at": "2024-01-15T10:30:00Z"
}

// Project
{
  "id": 1,
  "path": "/path/to/project",
  "name": "my-app",
  "package_manager": "pnpm",
  "created_at": "2024-01-15T10:00:00Z",
  "updated_at": "2024-01-15T10:30:00Z"
}

// Link
{
  "id": 1,
  "package_id": 1,
  "project_id": 1,
  "link_type": "hardlink",
  "created_at": "2024-01-15T10:00:00Z",
  "updated_at": "2024-01-15T10:30:00Z"
}
```

---

## CLI Commands

### `lnpm publish`

Publish a package to the local store.

```bash
# Basic usage (in package directory)
lnpm publish

# Publish and push to all linked projects
lnpm publish --push
```

**Process:**
1. Read `package.json` for name/version
2. Determine files to include (respects `.npmignore`, `files` field — see the
   README's "File Filtering" section for the rules and where they part company
   with `npm publish`)
3. Calculate content hash of all files
4. Reflink or copy files into a temporary directory alongside
   `~/.lnpm/store/{name}/{hash}/`, then rename it into place

   The store commit is atomic: `~/.lnpm/store/{name}/{hash}/` appears all at
   once, so a reader never sees a half-written package, and an interrupted
   publish leaves only the temporary directory behind. An entry already
   committed for the same hash is never destroyed — the store is
   content-addressed, so an occupied destination already holds this exact
   content.

   Every entry carries a `.lnpm-complete` marker, written as the last file
   inside the temporary directory and removed before the tree when `lnpm gc`
   deletes the entry. The store reports an entry as present only when the
   marker is there, so a deletion interrupted partway reads as absent rather
   than as a truncated package. Entries written before markers existed are
   marked once, the first time a command opens the store, and
   `~/.lnpm/store/.lnpm-markers-backfilled` records that the pass finished. An
   entry that cannot be marked is skipped and the sentinel is withheld, so the
   next store open retries it; `lnpm doctor` reports the backfill as pending
   until it completes.

5. Record in bbolt database
6. If `--push`, update all linked projects

### `lnpm add <package>`

Add a package from the store to current project.

```bash
# Add latest published version
lnpm add my-package

# Add a specific version (must match the latest published version)
lnpm add my-package@1.0.0

# Add without modifying package.json
lnpm add my-package --pure

# Add with dev dependency
lnpm add my-package --dev

# Link to the live source directory instead of a store copy
lnpm add my-package --link
```

**Process:**
1. Find package in store (latest or specified version)
2. Create a temporary directory alongside `.lnpm/{package}/`
3. Hard link all files from store into it, then rename it to `.lnpm/{package}/`
4. Create symlink `node_modules/{package}` → `.lnpm/{package}`
5. Update `lnpm.lock`, recording the current `package.json` specifier as the
   original version

   The lock write stays ahead of the rewrite below: the original specifier
   exists only in `package.json`, so a rewrite that ran first would overwrite
   it and any later failure would leave `remove` nothing to restore.

6. Update `package.json` with `file:.lnpm/{package}`
7. Register link in bbolt

**Live linking (`--link`):** step 3 is skipped entirely, and step 2 creates a
link rather than a directory. `.lnpm/{package}` becomes a link to the
`source_path` recorded at publish time — created alongside and renamed into
place, so replacing a store copy never deletes it before its replacement exists
— `package.json` gets `link:.lnpm/{package}`, and the recorded link type is
`link`. Nothing is copied,
so every later edit of the source is visible to the project at once — and so the
project builds against whatever is in the source tree right now, including files
that have never been published and are not even committed. That is the isolation
the default snapshot gives you and `--link` gives up. `--link` therefore still
requires the package to have been published once: that is what records its
source path.

Teardown never follows the link: `remove`, `retreat` and a re-`add` all delete
`.lnpm/{package}` with `os.RemoveAll`, which removes a link without descending
through it, so the source tree is untouched.

### `lnpm remove <package>`

Remove a linked package.

```bash
lnpm remove my-package

# Remove all linked packages
lnpm remove --all
```

**Process:**
1. Remove `.lnpm/{package}/` directory
2. Remove `node_modules/{package}` symlink
3. Restore original `package.json` dependency (if any)
4. Update `lnpm.lock`
5. Remove link from bbolt

### `lnpm push`

Push updates to all linked projects.

```bash
# Push current package to all consumers
lnpm push

# Push, skipping prepare scripts
lnpm push --skip-hooks
```

**Process:**
1. Run prepare scripts (unless `--skip-hooks`)
2. Calculate new content hash
3. Update store with new version
4. For each linked project:
   - Skip it if it is live-linked (`.lnpm/{package}` is a link, not a
     directory): it already resolves to the source directory being pushed, and
     relinking would swap its live link for a snapshot copy
   - Create new hard links in a temporary directory alongside `.lnpm/{package}/`
   - Rename it over `.lnpm/{package}/`, then delete the old directory
   - Preserve `node_modules` symlink

   The relink is atomic: `.lnpm/{package}/` holds the previous complete package
   until the new one is fully built, so a consumer building against
   `node_modules/{package}` never sees an empty or half-written package, and a
   failed or interrupted push leaves the previous package in place.

### `lnpm pull [package...]`

Refresh linked packages from the store. The counterpart to `push`: where `push`
sends a package out to its consumers, `pull` fetches the store's current
contents into the consumer that runs it.

```bash
# Refresh every package in lnpm.lock
lnpm pull

# Refresh only the named packages
lnpm pull my-package other-pkg
```

**Process:**
1. Read the linked set from `lnpm.lock` (all of it, or just the named packages,
   which must already be linked here — `pull` refreshes links, it never creates
   them)
2. Skip any package that is live-linked (`.lnpm/{package}` is a link, not a
   directory): it resolves to its source, so there is nothing to refresh, and
   relinking it would silently swap the live link for a snapshot copy
3. For each remaining package, look it up in the store; skip it when the lock
   entry already matches the store's version and content hash
4. Hard link the store's current files into a temporary directory and rename it
   over `.lnpm/{package}/`, exactly as `add` does
5. Update the entry in `lnpm.lock`, preserving the original `package.json`
   specifier recorded by `add`

`package.json` is never touched: the `file:.lnpm/{package}` reference it holds
is already correct, and only the contents behind it change. Nothing is written
to bbolt either — publishing updates the package row in place, so the link row
`add` recorded still points at the right package.

### `lnpm status`

Show current state of all links.

```bash
lnpm status

# Output:
# 📦 Published Packages
# ┌──────────────┬─────────┬──────────┬─────────────────────┐
# │ Package      │ Version │ Hash     │ Published           │
# ├──────────────┼─────────┼──────────┼─────────────────────┤
# │ my-package   │ 1.0.0   │ abc123   │ 2 minutes ago       │
# │ other-pkg    │ 2.1.0   │ def456   │ 1 hour ago          │
# └──────────────┴─────────┴──────────┴─────────────────────┘
#
# 🔗 Active Links
# ┌──────────────┬─────────────────────────┬──────────┐
# │ Package      │ Project                 │ Type     │
# ├──────────────┼─────────────────────────┼──────────┤
# │ my-package   │ ~/code/my-app           │ hardlink │
# │ my-package   │ ~/code/other-app        │ hardlink │
# └──────────────┴─────────────────────────┴──────────┘
```

### `lnpm list`

List packages in a project or the store.

```bash
# List packages linked in current project
lnpm list

# List all packages in store
lnpm list --store

# List all projects using a package
lnpm list my-package --projects
```

### `lnpm gc`

Garbage collect unused packages from store.

```bash
# Dry run - show what would be removed
lnpm gc --dry-run

# Remove packages with no active links
lnpm gc

# Remove packages older than 30 days
lnpm gc --older-than 30d
```

### `lnpm doctor`

Diagnose common issues. Doctor repairs nothing it finds — each finding names
the command that fixes it instead. It is not otherwise write-free: probing
whether the store is writable writes and removes a temporary file, and opening
the database creates it if it is missing.

```bash
lnpm doctor

# Output:
# Running lnpm doctor...
#
# Checking store directory... ✓ OK
# Checking database... ✓ OK
# Checking for orphaned packages... ✓ OK
# Checking for orphaned links... ⚠ 1 orphaned link(s)
#   Fix: Run 'lnpm gc --fix-links' to clean up
# Checking store file integrity... ✓ OK
# Checking store completeness markers... ✓ OK
#
# ⚠ Found 1 warning(s)
```

Doctor separates issues from warnings, and so does its exit code: it exits
non-zero once it has reported an issue, and zero otherwise. Warnings alone —
the run above among them — still exit zero, because they describe cleanup worth
doing rather than something broken. That makes `lnpm doctor && deploy.sh` safe
to script. The findings are printed either way; the exit code only says whether
any of them was an issue.

The markers are decorative. On a terminal doctor prints `✓`/`✗`/`⚠` as shown
above; when its output is piped, or `NO_COLOR` is set, it prints `OK`/`x`/`!`
instead, like the rest of the CLI.

---

## Configuration

### Global Config (`~/.lnpm/config.yaml`)

```yaml
# Store location (default: ~/.lnpm)
store_path: ~/.lnpm

# Default link mode: "hardlink" | "copy"
link_mode: hardlink
```

---

## Lock File Format (`lnpm.lock`)

```yaml
version: 1
packages:
  my-package:
    version: 1.0.0
    hash: abc123def456
    source: ~/code/my-package
    linked: 2024-01-15T10:30:00Z
    originalVersion: ^1.0.0   # original specifier, restored on retreat/remove
  other-pkg:
    version: 2.1.0
    hash: 789xyz000111
    source: ~/code/other-pkg
    linked: 2024-01-15T09:00:00Z
```

---

## Package Manager Integration

### Detection

lnpm auto-detects package manager by checking for:
1. `bun.lockb` → bun
2. `pnpm-lock.yaml` → pnpm
3. `yarn.lock` → yarn
4. `package-lock.json` → npm

### package.json Modification

```json
{
  "dependencies": {
    "my-package": "file:.lnpm/my-package"
  }
}
```

The `file:` protocol is universally supported by all package managers.

Edits are spliced into the file's existing bytes rather than re-serialized from
a parsed document, so key order, indentation style, line endings and number
literals outside the edited entry are left exactly as the author wrote them.
`lnpm add` and `lnpm remove` therefore produce a one-line diff.

### Cleanup Before Publish

Before publishing to npm registry, run:
```bash
lnpm retreat  # or: lnpm remove --all
```

This restores original version specifiers in `package.json`.
