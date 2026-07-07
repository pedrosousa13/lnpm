TITLE: Fix small documentation inaccuracies about status, add, push, and reflink history
LABELS: docs, good first issue

## Severity

Low — each item is an isolated, small factual mismatch (an out-of-date command description, a misleading inline comment, an overstated claim about incremental linking, and a misplaced changelog section) rather than a workflow that breaks end-to-end.

## Background

lnpm's `README.md`, `ARCHITECTURE.md`, and `CHANGELOG.md` have a handful of small, independent inaccuracies. This is a grab-bag of four unrelated one-line fixes — good for a first contribution.

## Problem

**(a) `README.md:81`** describes `lnpm status` in the commands table as:
> `| `lnpm status` | Show current project's links |`

Reality: `RunStatus` (`internal/cli/status.go:14-106`) lists every published package in the store and every active link across every project on the machine (lines 20-86), and only additionally shows the current directory's own links at the end (lines 88-103). `ARCHITECTURE.md:308-310` already describes it correctly: "Show current state of all links."

**(b) `README.md:270`**, in the "How It Works" walkthrough:
> `lnpm add my-library   # Links and runs npm install`

This implies `add` always installs. Reality: `internal/cli/add.go:382-390` only runs the package manager install (`hooks.RunPostAdd`) when the `--install` flag was passed (`runInstall`); otherwise it just prints a tip suggesting the user run install manually (lines 388-389).

**(c) `README.md:317`**:
> `4. **Push** — Updates store and re-links changed files`

This implies push is incremental. Reality: `Linker.Link` (`internal/link/link.go:52-58`) does `os.RemoveAll(lnpmPath)` followed by a full re-create of every file on every push — there is no per-file diffing.

**(d)** `CHANGELOG.md:183-199` has an `## [Unreleased]` section describing "Reflink (Copy-on-Write) support," "Hard link support during publish," "Parallel copy operations," etc., sitting between the `## [1.2.0]` entry (line 175) and the `## [1.1.1]` entry (line 201) — out of chronological order, and still labeled "Unreleased" even though every version through the current 1.11.0 has since shipped. This same work is already credited under `## [1.2.0]` (`CHANGELOG.md:180`, commit `465b64b`, "improve perf by using hard links") — `git show 465b64b --stat` confirms that commit added `internal/link/reflink_darwin.go`, `internal/link/reflink_linux.go`, and the equivalent files under `internal/store/`, i.e. reflink support shipped in the same commit as hard-link support. `ARCHITECTURE.md:79`'s "**New in v1.2.0**" caption on the Reflink section is therefore accurate and does not need to change — the bug is that the stray `[Unreleased]` block duplicates/expands on the terse v1.2.0 changelog entry instead of being merged into it.

## Where to look

- `README.md:81` — status command table row.
- `internal/cli/status.go:14-106` — `RunStatus`, especially lines 20-86 (global packages/links) vs. lines 88-103 (current-directory section).
- `ARCHITECTURE.md:308-310` — the already-correct description to match.
- `README.md:270` — the `add` walkthrough comment.
- `internal/cli/add.go:382-390` — the `runInstall`-gated install logic.
- `README.md:317` — the `push` walkthrough line.
- `internal/link/link.go:52-58` — `RemoveAll` + full re-create on every link.
- `CHANGELOG.md:175-181` — the `## [1.2.0]` entry (commit `465b64b`).
- `CHANGELOG.md:183-199` — the stray `## [Unreleased]` section.
- `ARCHITECTURE.md:79` — the "New in v1.2.0" reflink caption (confirmed accurate, no change needed).

## How to fix

1. `README.md:81` — change the description to `Show all published packages and active links across every project` (matching `ARCHITECTURE.md:310`).
2. `README.md:270` — change the comment to `# Links only (pass --install to also run npm install)`.
3. `README.md:317` — change to `4. **Push** — Updates store and re-links all files to consuming projects` (drop "changed").
4. `CHANGELOG.md:183-199` — merge this content into the `## [1.2.0]` entry above it (lines 175-181), since it describes the same shipped work, then delete the standalone `## [Unreleased]` header. Do not create a new version section for it and do not change `ARCHITECTURE.md:79` — its v1.2.0 attribution is correct.

## Acceptance criteria

- [ ] `README.md:81`'s `lnpm status` description matches its actual global behavior.
- [ ] `README.md:270`'s `add` comment reflects that install only runs with `--install`.
- [ ] `README.md:317`'s `push` description no longer implies incremental re-linking.
- [ ] `CHANGELOG.md` no longer has a `[Unreleased]` section describing already-shipped v1.2.0 work out of chronological order; that content is merged into the `[1.2.0]` entry.
- [ ] `ARCHITECTURE.md:79` is left unchanged (its v1.2.0 reflink attribution is correct).

## Testing

```
grep -n "Show current project's links" README.md
grep -n "Links and runs npm install" README.md
grep -n "re-links changed files" README.md
grep -n "## \[Unreleased\]" CHANGELOG.md
```

All should return nothing after the fix. Confirm CLI help still matches the corrected descriptions:

```
go run ./cmd/lnpm status --help
go run ./cmd/lnpm add --help
```
