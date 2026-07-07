TITLE: yalc feature parity
LABELS: epic, enhancement
---
## Overview

lnpm is a local npm package development tool that replaces `yalc` for linking a package under active development into one or more consumer projects: `lnpm publish` packs the current package into a content-addressed store at `~/.lnpm`, `lnpm add <pkg>` copies/links it into a consumer's `.lnpm/<pkg>/` with a `node_modules/<pkg>` symlink and a `file:.lnpm/<pkg>` entry in package.json, and `lnpm push` re-links every consumer after a new publish.

lnpm intentionally rebuilt this workflow rather than cloning yalc feature-for-feature, but a few yalc workflows that developers migrating from yalc expect don't have an lnpm equivalent yet. This epic tracks closing those specific gaps.

## Why this matters

Developers coming from yalc reach for `yalc update`, `yalc check`, and `yalc restore` out of habit. Today those workflows either don't exist in lnpm or require remembering multi-step manual workarounds, which makes lnpm feel less complete during day-to-day local development even though its core linking mechanism (hard links / content-addressed store) is more robust than yalc's copy-based approach.

## Sub-issues

1. Add `lnpm pull` command to sync linked packages from the store
2. Add `lnpm check` command to catch lingering file: dependencies before publish
3. Add `lnpm restore` to re-link packages after `retreat`
4. Add `--link` mode for live-updating dependencies without publish
5. Rewrite workspace:* dependency specifiers on publish
6. Make push/relink incremental using stored per-file hashes
7. Distribute lnpm via Homebrew and Scoop
8. Sign release artifacts and add a real vulnerability reporting channel
9. Add dist-tag support (publish --tag, add pkg@tag)
10. Add version history and rollback to a previous published version

## Definition of done

- A consumer project can refresh an already-linked package's contents from the store without re-running the full `add` flow.
- A pre-commit/CI-friendly command can detect and fail on lingering `file:.lnpm/`/`link:.lnpm/` dependency entries.
- Running `lnpm retreat --force` no longer permanently forecloses returning to the linked state; a restore path exists.
- A consumer can opt into a live-updating link to a package's source directory instead of only a point-in-time published copy.
- All new commands have integration test coverage under `tests/` and are documented in the README.
