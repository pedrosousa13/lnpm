TITLE: Make push/relink incremental using stored per-file hashes
LABELS: enhancement
---
## Background

lnpm already records a per-file manifest for every published package: `db.FileEntry` (`internal/db/db.go`) stores `RelativePath`, `ContentHash`, `Size`, `Mode`, and `ModTime` for each file, populated by `finishPublish` in `internal/cli/publish.go` via `database.InsertFiles`. But when a package is re-linked into a project — whether from `lnpm push` (`pushToProject` in `internal/cli/publish.go`) or a plain `lnpm add` — `Linker.Link` in `internal/link/link.go` always does `os.RemoveAll(lnpmPath)` followed by recreating `.lnpm/<pkg>` from scratch and re-processing every file (lines 52-58 of `internal/link/link.go`), even when only a handful of files actually changed since the last link. For a package with thousands of files, this makes every push pay for a full re-link regardless of how small the actual diff is.

## Motivation

As a developer with a package that has a large file count (e.g. a built `dist/` with many small files) but small, frequent edits, I want `lnpm push`/`lnpm add` to only touch files that actually changed, so pushes stay fast as the package grows instead of scaling with total file count.

## Proposed behavior

```
$ lnpm push
Publishing mylib@1.3.1 (1,204 files)...
✓ Published mylib@1.3.1

Pushing to 1 linked projects...
  ✓ /Users/dev/myapp (3 changed, 1,201 unchanged)
```

A push where nothing changed completes near-instantly instead of paying for a full copy/relink of every file.

## Implementation sketch

1. `Linker.Link` (`internal/link/link.go`) needs to know what's already present in `.lnpm/<pkg>` before deciding what to touch. Since content is addressed by hash and `db.FileEntry` already has per-file `ContentHash`, the simplest source of truth is to record, per (project, package) link, the per-file hash set that was last linked — e.g. alongside `db.Link` (`internal/db/db.go`), or by hashing the existing `.lnpm/<pkg>` tree once at the start of `Link`.
2. Change `Link` in `internal/link/link.go` to diff the new `files []*pack.FileInfo` (already carrying `ContentHash` from the store, per `store.GetFiles`) against the last-known set: create files present only in the new set, replace files whose `RelPath` exists in both but `ContentHash` differs, and remove files present only in the old set. Skip files whose `RelPath`+`ContentHash` are unchanged entirely — for those, no reflink/hardlink/copy work needs to happen at all.
3. Replace the current blanket `os.RemoveAll(lnpmPath)` + full recreate (`internal/link/link.go` lines 52-58) with this targeted create/replace/remove pass, keeping the existing parallel reflink/hardlink/copy worker pool for whatever subset actually needs writing.
4. Preserve the atomic-swap guarantee the current RemoveAll+recreate gives today (a consumer never observes a half-old-half-new tree mid-push): either stage changed files into a temp subdirectory and rename into place only once every change succeeds (the same temp-dir-then-rename pattern `internal/store/store.go`'s `Store()` already uses), or ensure the in-place update path fails atomically (all-or-nothing) rather than leaving a partially-updated `.lnpm/<pkg>` on error.
5. Update the summary output in `internal/cli/publish.go`'s `pushToLinkedProjects`/`pushToProject` to report changed vs. unchanged file counts per project.

## Acceptance criteria

- [ ] A push where no files changed since the last link completes without rewriting any file in `.lnpm/<pkg>`.
- [ ] A push where only some files changed only touches those files (verified by unchanged files keeping their original inode/mtime).
- [ ] A file removed from the package since the last link is removed from `.lnpm/<pkg>` on the next link.
- [ ] An interrupted push (process killed mid-relink) never leaves `.lnpm/<pkg>` in a state mixing old and new content for the same file.
- [ ] `lnpm push` output reports how many files changed vs. were left untouched.

## Testing

- Extend the existing benchmark in `tests/bench_test.go` to cover a large package with a small incremental change, asserting reduced work vs. a full relink.
- New assertions in `tests/push_test.go`: publish, add, modify one file and republish + push, assert only that file's entry in `.lnpm/<pkg>` changed (compare file identity/mtime before and after) and that content still matches after a simulated interrupted-then-retried push.

## Open questions

None — this is a performance change with a clear correctness bar (same end state, less work), not a new user-facing behavior.
