TITLE: CLI correctness and UX fixes
LABELS: epic
---
## Overview

lnpm is a command-line tool for local npm package development. Users run commands like `publish`, `add`, `remove`, `push`, `gc`, `doctor`, and `retreat`. This epic collects a set of correctness and user-experience defects in those commands: destructive operations that delete data without asking, commands that mangle the user's `package.json`, lifecycle scripts that silently do not run, concurrent invocations that crash, and several smaller papercuts.

None of these require new features. Each is a fix to make an existing command behave the way a user would reasonably expect.

## Why this matters

Two of these bugs can lose user data or ship stale build output: destructive operations auto-confirm when output is piped, and only the first of several publish lifecycle scripts runs. Others erode trust in the tool: every `add`/`remove` produces a large spurious diff in the user's `package.json`, parallel builds (for example under turbo) crash with a cryptic database timeout, and `doctor` inspects the wrong store directory so it reports healthy when it is not. The remaining items are smaller but visible: wrong exit codes, decorative glyphs leaking into piped output, mishandled scoped packages, and a few flag/config quirks. Fixing them makes the CLI predictable and safe to script against.

## Sub-issues

1. Abort destructive operations in non-interactive mode instead of auto-confirming
2. Make doctor honor the configured store_path when checking the store
3. Make doctor exit non-zero on failure and respect NO_COLOR
4. Preserve package.json key order and formatting when editing dependencies
5. Run all applicable publish lifecycle scripts, not just the first
6. Stop concurrent lnpm invocations from failing with a cryptic database timeout
7. Detect Bun's text lockfile bun.lock
8. Make `lnpm list <package>` honor its argument without --projects
9. Support editors with arguments in `config --edit`
10. Fall back to a user completion directory when the system directory is not writable
11. Handle scoped packages correctly in unlink cleanup and listing
12. Wire up or remove the unused hooks.skip_post_add config field

## Definition of done

- [ ] All twelve sub-issues are closed.
- [ ] Destructive commands (`gc`, `remove --all`) never delete without an explicit confirmation or `--yes` flag.
- [ ] `add` and `remove` produce minimal diffs in the user's `package.json`.
- [ ] `doctor` inspects the same store path the rest of the tool uses and exits non-zero when it finds issues.
- [ ] Parallel lnpm invocations either succeed or fail with a clear "another lnpm process is running" message.
- [ ] Each fix has a test covering the corrected behavior, and `go test ./...` passes.
