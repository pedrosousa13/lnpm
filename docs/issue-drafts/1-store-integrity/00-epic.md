TITLE: Store integrity: the content-addressed store can be silently corrupted
LABELS: epic, bug
---
## Overview

lnpm is a Go CLI that acts as a local alternative to npm for developing packages against real consumer projects. Its core is a **content-addressed store** at `~/.lnpm/store`: when you run `lnpm publish`, lnpm packs the package in the current directory, computes a content hash over the file list (contents plus permission bits), and stores the files under `~/.lnpm/store/<package>/<hash>/`. When you run `lnpm add <pkg>` in a consumer project, lnpm copies or links those files into `<project>/.lnpm/<pkg>`, points `node_modules/<pkg>` at it via a symlink, and rewrites the dependency in `package.json` to `file:.lnpm/<pkg>`. `lnpm push` re-publishes and refreshes every linked consumer.

"Content-addressed" means the path of a stored package is derived from a hash of its bytes: the directory name *is* a promise that the bytes inside hash to that value. The whole system depends on that promise holding. This epic collects a set of defects that break it — the store can end up holding files whose contents no longer match their hash, consumer projects can be mutated mid-build, permission bits can be lost, and interrupted operations can leave partial packages that later look complete.

Several of these stem from how lnpm links files together at the filesystem level. Three terms used throughout:

- **hardlink**: two directory entries pointing at the same underlying file data (the same *inode* — the on-disk object that holds a file's bytes and metadata). Editing through one path changes what you see through the other; they are the same file with two names.
- **inode**: the kernel's identity for a file's data. Two paths sharing an inode are the same file. `os.Link` creates a second name for an existing inode.
- **reflink** (also "CoW" / copy-on-write clone): a filesystem-level clone that starts out sharing data blocks but is *logically independent* — writing to one copy transparently allocates new blocks so the other copy is untouched. Fast like a hardlink, isolated like a real copy. Supported on APFS (macOS) and Btrfs/XFS (Linux).

## Why this matters

A content-addressed store that can silently disagree with its own hashes is worse than no store, because callers *trust* it. The most severe defects here cause the store to be populated with hardlinks back to the developer's live source tree, so a routine rebuild (tsc, webpack, an editor saving in place) mutates the stored package *and* every consumer project that linked it — mid-build, invisibly. Once mutated, a later publish that produces the same hash short-circuits on an existence check and links the corrupted content forward. On macOS the intended fast-and-safe path (reflink) never actually runs because the wrong syscall is invoked, so every operation silently falls through to the unsafe hardlink path. These are correctness bugs that produce wrong bytes on disk with no error, which is the hardest class of bug for a downstream developer to diagnose.

## Sub-issues

1. Fix macOS reflink: the clonefile syscall is invoked with the wrong signature and never succeeds
2. Stop hardlinking source files into the store: isolate stored content from the source tree and consumers
3. Make relink atomic: populate a temp directory and rename-swap instead of clearing the live package
4. Remove the destructive RemoveAll before rename that can bless a partial store entry as complete
5. Preserve permission bits in copy and reflink fallbacks so umask cannot corrupt stored file modes
6. Harden the copy loop and reflink cleanup: use io.Copy, errors.Is, and single-close/unlink-on-failure

## Definition of done

- [ ] On macOS/APFS, reflinks actually succeed and are the primary path for both store population and project linking (verified via debug logs showing reflink counts > 0).
- [ ] The store is never populated by hardlinking the developer's source files; stored content is isolated from the source tree so in-place rebuilds cannot mutate a stored package.
- [ ] An interrupted or crashed `lnpm add`/`push` never leaves a consumer's `node_modules/<pkg>` pointing at a half-populated or missing package.
- [ ] An interrupted store operation never leaves a partial package directory that `Exists` treats as complete.
- [ ] Executable and other permission bits recorded in the package hash are preserved in the store and in linked projects regardless of the developer's umask.
- [ ] The hand-rolled copy loop and reflink cleanup paths are robust (no string-compared EOF, no double-close, dst removed on every reflink failure path).
- [ ] `go test ./...` passes, including new regression tests for each fix.
