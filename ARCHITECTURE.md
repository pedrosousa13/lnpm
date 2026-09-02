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
│       ├── .lnpm-linked         # What the last link put here (see below)
│       ├── package.json
│       ├── dist/
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
  safe as long as consumers treat linked packages as read-only — which they now
  have to. Store content is committed with its write bits stripped, so a write
  into a hard-linked file fails instead of rewriting the entry every later `add`
  of that version would serve. All three methods preserve the source's mode, so
  a linked package is read-only whichever one ran. See `SECURITY.md`.

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

## Benchmarks

Three benchmark scripts live in this repository, and they do not measure the
same thing.

`scripts/bench-vs-yalc.sh` is the comparison against yalc, and the one to quote.
It builds the lnpm binary and runs both tools as subprocesses, so each pays its
own process startup. That matters more than it sounds. On the machine the
README's table came from, `lnpm --version` takes 10ms to 30ms and `node -e ''`
takes 230ms to 340ms, so a Node startup is most of what a small `yalc add`
costs. Charging that startup to one side only does not measure the tools.
`ITERATIONS` and `FILES` change the shape of the run.

```bash
ITERATIONS=10 FILES=100 ./scripts/bench-vs-yalc.sh
```

The script seeds a fresh package for every iteration. lnpm's store is
content-addressed, so re-publishing bytes it already holds would time the cache
rather than the work.

`tests/bench_test.go` is a Go benchmark suite. Its lnpm cases are the ones to
track lnpm against itself across releases. Its `BenchmarkYalcPublish` and
`BenchmarkYalcAdd` cases are not a fair comparison. They call lnpm's `cli.Run*`
functions in-process while spawning `yalc` as a command, so yalc is charged for
a Node startup lnpm never pays and the ratio comes out larger than the truth.
They stay for the absolute yalc timings.

`scripts/benchmark-compare.sh` predates both. It does run both tools as
subprocesses, but it publishes the same package on every iteration, so all but
the first run time the store cache. It also passes `--skip-hooks
--skip-validation` to lnpm alone. Treat its output as a smoke test, not a
number to publish.

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
meta               - Metadata (next_id counter, schema_version)
tags               - Dist-tags (key: name + NUL + tag, value: content hash)
```

A package record is addressed by its name and content hash, so several versions
of one name can be in the store at once. Which version a name resolves to is
decided by the `tags` bucket: `packages_by_name` names the version tagged
`latest`, and the two are written together so they cannot disagree.

`meta.schema_version` records the shape above. A database written before tags
existed carries none, and gains a `latest` tag per name when it is opened —
`packages_by_name` was the only way to reach a package then, so the record it
named was that package's latest by definition.

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
  "tag": "beta",  // channel followed; absent means latest
  "pinned": true, // follows no channel: one build, moved only by the user
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

# Publish to a channel other than latest, leaving latest where it is
lnpm publish --tag beta
```

`--tag` changes which tag is moved onto the version being written, and nothing
else. The build is stored and recorded the same way; only `latest` stays put.
The "already published with same content" shortcut is skipped unless the tag
being published already names that content, because that shortcut is what would
otherwise leave the tag unmoved.

**Process:**
1. Read `package.json` for name/version
2. Determine files to include (respects `.npmignore`, `files` field — see the
   README's "File Filtering" section for the rules and where they part company
   with `npm publish`)
3. Rewrite `package.json` — resolve any `workspace:` specifiers, and remove the
   `prepare` and `prepublish` scripts so npm does not run them when the package
   is installed as a `file:` dependency. The developer's own file is untouched;
   the rewritten document goes to a temporary file and the packed entry points
   at it.

   This happens **before** step 4 on purpose. The store is content-addressed, so
   a rewrite after hashing would leave the entry holding bytes its own hash does
   not describe. It used to, which is why `doctor --verify-content` once had to
   exempt the root manifest — see ADR-0009.
4. Calculate content hash of all files
5. Reflink or copy files into a temporary directory alongside
   `~/.lnpm/store/{name}/{hash}/`, then rename it into place

   The store commit is atomic: `~/.lnpm/store/{name}/{hash}/` appears all at
   once, so a reader never sees a half-written package, and an interrupted
   publish leaves only the temporary directory behind. An entry already
   committed for the same hash is never destroyed — the store is
   content-addressed, so an occupied destination already holds this exact
   content.

   Every entry carries a `.lnpm-complete` marker, written as the last file
   inside the temporary directory and removed before the tree when `lnpm gc`
   deletes the entry. The marker records the entry's content hash and a schema
   version. An entry counts as complete only when the marker is there, names the
   directory it sits in, and records a schema this build writes — so a deletion
   interrupted partway reads as *incomplete*, and an entry written before 4.0.0
   changed the hash format reads as unusable rather than being served under
   rules it was not stored under. Neither is deleted; `lnpm gc` reclaims them.

   Both the write path and the read paths check it: `lnpm add`, `lnpm pull` and
   the relink after `lnpm publish --push` refuse an entry that fails, naming the
   directory and saying what to do. lnpm never deletes such an entry — it cannot
   tell what is inside one — so removing it is the user's call, and it has to
   happen before a re-publish of the same content can land, since the store
   never renames over an occupied destination. `lnpm doctor` lists them.

   Markers shipped in 2.0.0. A store in which nothing is marked therefore
   predates them, and is marked once, the first time any command opens it;
   `~/.lnpm/store/.lnpm-markers-backfilled` records that the decision was taken.
   A store in which anything *is* marked is never backfilled — an unmarked entry
   there is a damaged one, and marking it would be the laundering that made the
   marker worthless. A backfill that cannot finish undoes itself and retries on
   the next open, so the store stays recognisably un-migrated. `lnpm doctor`
   reports an un-migrated store as pending rather than listing every entry.

6. Record in bbolt database
7. If `--push`, update all linked projects

### `lnpm add <package>`

Add a package from the store to current project.

```bash
# Add latest published version
lnpm add my-package

# Add a specific version, from anywhere in the retained history
lnpm add my-package@1.0.0

# Roll back to a specific published build, by content-hash prefix
lnpm add my-package@a1b2c3d4

# Add whatever the beta channel currently names
lnpm add my-package@beta

# Add without modifying package.json
lnpm add my-package --pure

# Add with dev dependency
lnpm add my-package --dev

# Link to the live source directory instead of a store copy
lnpm add my-package --link
```

**Process:**
1. Find the version in the store. What follows the `@` is matched in three
   steps, most specific first: as a dist-tag; then as an exact version, against
   every version the store has retained and not only the one `latest` names;
   then as a content-hash prefix of at least four characters, git's minimum for
   an abbreviated object name. A spec with no `@` resolves through `latest`. A
   spec that matches two retained versions is refused with their full hashes
   rather than resolved to one of them. A tag is recorded on the link row, so a
   later move of `latest` does not carry this project onto it; a version and a
   hash name a build rather than a channel, so the link they write is **pinned**
   — it follows nothing, no tag move carries it forward, `lnpm pull` leaves it
   where it is and `lnpm gc` keeps the build it names for as long as it names it.
   A spec with no `@` clears the pin, which is how a project says it wants to
   follow the channel again (ADR-0006)

   None of the three can be combined with `--link`: that resolves to the source
   directory, which holds the working tree and is not the build any of them
   names
2. Create a temporary directory alongside `.lnpm/{package}/`
3. Hard link all files from store into it, write the `.lnpm-linked` manifest,
   then rename it to `.lnpm/{package}/`

   A first `add` into a project has nothing to compare against and so links the
   whole package. Re-running it against a package already linked there takes the
   incremental path described under `lnpm push`.

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

# Push to a channel other than the one the build already carries
lnpm push --tag beta

# Push, skipping prepare scripts
lnpm push --skip-hooks
```

**Process:**
1. Run prepare scripts (unless `--skip-hooks`)
2. Calculate new content hash
3. Work out which channel to push to, unless `--tag` said. Two questions are
   asked in order, and `latest` wins wherever it appears in either answer:
   - which tags name the packed content — exact for an unchanged working tree,
     since that is the build they name
   - failing that, which tags name a stored record carrying the working tree's
     version — a push loop edits files without touching `package.json`, so the
     version still identifies the pre-release being worked on

   One tag other than `latest` wins; several sharing a version is refused,
   since each would move and carry its consumers. Several naming one content
   hash is not refused: each already points at it, so every choice moves
   nothing. A version bumped past everything in the store is answered by
   neither question and goes to `latest`, with a warning naming the other
   channels the package has
4. Update store with new version, moving that tag onto it
5. For each linked project:
   - Skip it if it is live-linked (`.lnpm/{package}` is a link, not a
     directory): it already resolves to the source directory being pushed, and
     relinking would swap its live link for a snapshot copy
   - Compare the package against `.lnpm/{package}/.lnpm-linked`, then read back
     the files that comparison says are unchanged and confirm they still hold
     the recorded content. If every file passes both and the directory holds
     nothing else, there is nothing to do: the directory is left exactly as it
     is, no file changes identity, and only the `node_modules` symlink is
     checked
   - Otherwise create a temporary directory alongside `.lnpm/{package}/` and
     fill it: an unchanged file is hard linked across from the package already
     in place, one syscall and no data moved; a changed one is materialised out
     of the store
   - Rename it over `.lnpm/{package}/`, then delete the old directory
   - Preserve `node_modules` symlink

   The relink is atomic: `.lnpm/{package}/` holds the previous complete package
   until the new one is fully built, so a consumer building against
   `node_modules/{package}` never sees an empty or half-written package, and a
   failed or interrupted push leaves the previous package in place.

   A push therefore *writes* each project the size of the change rather than the
   size of the package, and reports both: `3 changed, 1997 unchanged`. It reads
   more than it writes — see the manifest section below.

#### The link manifest (`.lnpm-linked`)

Every materialised `.lnpm/{package}/` carries a `.lnpm-linked` file recording
what the link put there: the path, content hash and permissions of each file,
the link type the link achieved, and the directory's own location. It is the
counterpart of the store's `.lnpm-complete` marker and is written the same way —
last, inside the temporary directory — so it commits with the content it
describes and can never outlive a swap that did not happen or survive one that
did.

A relink reads it to decide what it can leave alone, and then reads the files it
was about to leave alone to confirm they still hold the recorded content. So
relinking repairs a file deleted from under it or replaced by something that is
not a regular file, drops a stray the package does not list, and re-materialises
a file edited in place — which it reports as changed, because it wrote it.

Only the files a relink was about to skip are read. A file whose content differs
between the two links fails the manifest's hash comparison and is materialised
out of the store whatever is on disk, so reading it would decide nothing and cost
a read of a file about to be overwritten.

Reading the rest is not free, and on a filesystem that hard links or reflinks it
is not cheaper than the materialisation it avoids either — those move no file
data at all, where verification reads every byte. Warm, over 2000 files of 5 KB:
a relink with nothing changed costs 38 ms against 5.5 ms before the check, and a
relink with one file changed costs 61 ms against 45 ms to abandon reuse and
materialise all 2000. Copy mode, and a cold page cache, move the trade in
opposite directions. Reuse still keeps a carried-over file's inode, which is what
stops a watcher seeing the whole package change — but the reason these files are
read is that a relink reporting a file unchanged has to be right about it. The
full argument and the benchmarks behind it are on `verifiedReusable` in
`internal/link/manifest.go`.

What that repairs is accidental damage — a build step that wrote into
`node_modules`, a script that cleaned too much. An edit that reached a hard-linked
file wrote through the shared inode into the store entry itself, and then there
was nothing left to repair from; the store's write protection is what stops that
edit now, and an entry damaged before it existed still needs `lnpm gc` and a
re-publish.

The location it records is the directory as the filesystem knows it — absolute,
with the links along the way resolved — rather than the path the command that
wrote it happened to be given. Commands do not agree on that path: `add`, `pull`,
`remove` and `restore` build their linker from the working directory, while
`push` and `publish` build theirs from the project path the database recorded,
which is stored with its symlinks already resolved. Every spelling names the same
directory, and a relink that rejected the manifest because the previous command
spelled the path differently would rewrite the whole package — which is what
add-then-push did on Windows, where the working directory keeps the 8.3 short
names (`C:\Users\RUNNER~1\…`) that the database's normalization expands.

The name is reserved, exactly as `.lnpm-complete` is reserved by the store. The
store keeps its marker out of what `GetFiles` hands to consumers because the
marker belongs to the store and not to the package; the same holds a level down,
so a package that ships a file called `.lnpm-linked` at its root will **not** find
it in `.lnpm/{package}` — the file is dropped from the set being linked. Nothing
else about the package changes.

Reserving it rather than yielding it is what makes the collision impossible
rather than merely detectable. One path cannot hold two files, and a
`.lnpm-linked` that is sometimes the record of a link and sometimes package
content that looks like one leaves nothing on the way back in able to tell them
apart — a version shipping a manifest that named the next version's hashes could
then describe its own successor's relink.

The recorded location is a separate, smaller thing: a manifest describes one
directory, and is only acted on in the directory it names. Copying a project's
`.lnpm/` into another checkout is the case that happens in practice — the
manifest travels with it, still describing files by hash — and the mismatch costs
one full relink, after which the manifest names its new home.

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
3. Skip any package whose link is pinned, reporting it and the build it stays
   on. A pin follows no channel, so there is nothing to resolve it through. A
   `pull` that *names* a pinned package is refused instead, before anything is
   linked, and the refusal names `lnpm add <package>` as the unpin: naming a
   package is a request rather than a sweep, and this one cannot be honoured
   (ADR-0006)
4. For each remaining package, resolve the channel this project's link row
   follows — `latest` for a link that names none — and skip the package when the
   lock entry already matches that version and content hash. Resolving by name
   instead would answer with `latest` for every project and quietly move a
   tagged consumer onto the stable release
5. Relink it from the store, exactly as `add` and `push` do — including the
   incremental path, so a `pull` that finds only a few files changed rewrites
   only those
6. Update the entry in `lnpm.lock`, preserving the original `package.json`
   specifier recorded by `add`

`package.json` is never touched: the `file:.lnpm/{package}` reference it holds
is already correct, and only the contents behind it change. The link row is
usually left alone too — a publish that moves a tag carries the links following
that tag onto the version it now names, so the row `add` wrote already points at
the version `pull` just linked. It is repointed when it does not, which a publish
leaves behind when the tag it moves was deleted and set again: there is then no
previous version to carry links from. A project with no row for the package
gains none; `pull` refreshes links rather than creating them.

### `lnpm status`

Show current state of all links.

```bash
lnpm status

# Output:
# 📦 Published Packages
# ┌──────────────┬──────────────┬──────────┬──────────┬───────────────┐
# │ Package      │ Version      │ Hash     │ Tags     │ Published     │
# ├──────────────┼──────────────┼──────────┼──────────┼───────────────┤
# │ my-package   │ 1.0.0        │ abc123   │ latest   │ 2 minutes ago │
# │ my-package   │ 2.0.0-beta.1 │ abc789   │ beta     │ 1 minute ago  │
# │ other-pkg    │ 2.1.0        │ def456   │ latest   │ 1 hour ago    │
# └──────────────┴──────────────┴──────────┴──────────┴───────────────┘
#
# 🔗 Active Links
# ┌──────────────┬─────────────────────────┬──────────┐
# │ Package      │ Project                 │ Type     │
# ├──────────────┼─────────────────────────┼──────────┤
# │ my-package   │ ~/code/my-app           │ hardlink │
# │ my-package   │ ~/code/other-app        │ hardlink │
# └──────────────┴─────────────────────────┴──────────┘
```

The published table lists one row per retained version, so it carries the tags
naming each of them. Without that column two rows of one name say nothing about
which build a plain `lnpm add` would give you.

### `lnpm list`

List packages in a project or the store.

```bash
# List packages linked in current project
lnpm list

# List all packages in store
lnpm list --store

# List all projects using a package
lnpm list my-package --projects

# List every retained version of a package, newest first
lnpm list my-package --versions
```

`--store` marks each version with the tags naming it, as a trailing
`[beta, latest]`. With more than one version of a name live at once that is the
only place a user can see which build a channel points at; a version no tag names
is left unmarked, and is what `lnpm gc` will collect once nothing links it.

`--projects` walks every version of the name rather than resolving the name to
one record, and says which build each consumer is on. Resolving by name would
answer with the version `latest` points at, leaving every project that asked for
a channel out of the one listing whose job is naming who consumes this package.

`--versions` is one package's history: every version the store still holds,
newest first, with its content hash, its version, when it was published, the
dist-tags naming it and the projects linked to it. That set is exactly what
`lnpm gc` has not collected, because a version's record and its store entry go
together — there is no separate "ever published" history, and nothing here
retains anything gc would otherwise take. It is what `lnpm add <pkg>@<hash>` can
roll a project back to. It cannot be combined with `--projects`: the two answer
different questions about the same package, so serving one silently would answer
a question the user did not ask.

### `lnpm tag <package> <tag>`

Manage a package's dist-tags without republishing it.

```bash
# Point beta at the version the package resolves to
lnpm tag my-package beta

# Remove the beta tag, leaving the version it named in the store
lnpm tag my-package beta --delete
```

Setting names the version `packages_by_name` points at — the one tagged
`latest` — because that is the version a publish just wrote and the only one a
user can name without knowing content hashes. Moving a tag carries the projects
following *that* tag onto the new version and leaves the others alone.

`latest` cannot be deleted: `packages_by_name` mirrors it, so removing it would
leave the package published, its files on disk, and every command that resolves
by name unable to see it.

### `lnpm gc`

Garbage collect unused packages from store.

A version is collected when no valid link and no tag reaches it. `latest` does
not count: every publish moves it onto what it just wrote, so treating it as a
root would leave nothing collectable — see
`docs/adr/0002-latest-is-not-a-garbage-collection-root.md`.

A pinned link is a link, so a build a project pinned is a root and stays one for
as long as the pin does, with no time bound. That costs the arithmetic no extra
rule — it counts every link and reads no tags — and it is deliberate that no
expiry sits on top: reclaiming a pinned build takes two steps, unpin then
collect, as it already does for a version you tagged. See
`docs/adr/0006-a-pinned-link-follows-no-channel-and-nothing-but-the-user-moves-it-off.md`.

```bash
# Dry run - show what would be removed
lnpm gc --dry-run

# Remove versions no link and no tag reaches
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
# Checking store entries... ✓ OK
# Checking store file integrity... SKIPPED
#   Stored content was not re-hashed: that costs one read of the whole store
#   Run 'lnpm doctor --verify-content' to check it
# Checking store completeness markers... ✓ OK
#
# ⚠ Found 1 warning(s)
```

The two store checks answer different questions, and only one of them is free.
`store entries` reads each entry's completeness marker: it says the entry is
there and was finished, which costs a stat per package and says nothing about
what is inside it. `store file integrity` opens the files.

The store is content-addressed, so an entry's directory name is a claim about
the bytes inside it. `--verify-content` tests that claim: it re-reads every
stored file, checks each one against the content hash the database records, and
faults a file the entry holds that no record mentions — the shape that would
otherwise be copied into consumer projects unnoticed. It is off by default
because it costs one read of the whole store, about two seconds a gigabyte, and
doctor's other checks are bounded by the number of entries rather than by their
size.

A default run therefore ends with `✓ Every check that ran passed` rather than
`✓ All checks passed!`, and names what it left out. Doctor never reports a check
it did not run as one that passed.

What the content check detects is corruption and accident. The store hashes with
a 64-bit non-cryptographic function, which is not evidence against someone who
chose the replacement bytes.

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
    pinned: true              # linked to this build, not to a channel
```

`pinned` is optional and absent from every lock file written before it existed,
which reads as unpinned. The database's link row is the authority on it — that is
what `pull`, `push` and `gc` read — and the entry here is transport, exactly as
`hash` already is: `lnpm retreat` renames this file to `lnpm.lock.retreat`, and
`lnpm restore` rebuilds the link row from it.

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

An `npm publish` from a project lnpm has touched would ship the
`file:.lnpm/{package}` specifiers to the registry. Undo them first:

```bash
lnpm retreat --force   # or: lnpm remove --all
```

`--force` is required. Bare `lnpm retreat` is a preview. It prints the
specifiers it would restore, changes nothing and exits zero, which is easy to
mistake for a completed cleanup.

`lnpm check` is the guard against making that mistake. It fails when a
`package.json` still carries an lnpm reference, and it names the command that
clears it:

```
✗ Found 2 lnpm reference(s) in package.json:
  dependencies.my-package -> file:.lnpm/my-package
  dependencies.other-pkg -> file:.lnpm/other-pkg

  💡 Run 'lnpm retreat --force' to restore original dependencies before publishing
Error: 2 lnpm reference(s) found in package.json
```

It exits non-zero on a finding, so `lnpm check && npm publish` is safe to
script. It also faults the `lnpm.lock.retreat` snapshot a retreat leaves in the
project root, which carries an absolute path per package and is not otherwise
kept out of a tarball.
