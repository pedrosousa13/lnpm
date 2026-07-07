TITLE: State-consistency hardening for add, remove, and retreat
LABELS: bug, epic
---
## Overview

`lnpm` mutates a consumer project in several steps: it copies a package into `.lnpm/<pkg>`, symlinks `node_modules/<pkg>`, rewrites the dependency in `package.json` to `file:.lnpm/<pkg>`, records the user's original version specifier (for example `^1.2.3`) in `.lnpm/lnpm.lock`, and tracks the link in a database at `~/.lnpm/lnpm.db`. The original version stored in the lock file is what `lnpm remove` and `lnpm retreat` use to put the real dependency back.

These multi-step mutations are not resilient to partial failure or bad input. If a step in the middle fails, the project can be left with `package.json` pointing at `file:.lnpm/...` but no lock record of the original version. When that record is missing, `remove`/`retreat` do not restore `^1.2.3` — they delete the dependency outright. This epic collects the concrete failure paths and fixes each so a partial failure or a corrupt input never loses the user's original version and never crashes.

## Why this matters

Losing the user's original dependency specifier is the worst failure mode for a tool like this: the user can no longer tell what version they depended on, and no `lnpm` command can recover it. A crash mid-operation can also leave the project half-linked. Every issue below either prevents the original version from being lost or makes a failing operation fail loudly and safely instead of silently corrupting state.

## Sub-issues

1. Fix nil-pointer crash in retreat when lnpm.lock is corrupt
2. Fix versioned package spec being treated as a content hash in multi-package add
3. Save the lock file before rewriting package.json in add
4. Skip lock and database registration when a package.json update fails in multi-add
5. Keep the lock entry when remove fails to restore package.json
6. Make publish --push fail when every linked-project push fails

## Definition of done

- [ ] `lnpm retreat` never panics on a corrupt or hand-edited `lnpm.lock`.
- [ ] `lnpm add pkg@1.2.3 other-pkg` resolves the versioned spec the same way the single-package path does.
- [ ] A failure after `package.json` is rewritten never leaves the project with a `file:.lnpm/...` reference but no recorded original version.
- [ ] `lnpm remove` and `lnpm retreat` never delete a dependency that they failed to restore; such failures exit non-zero.
- [ ] `lnpm publish --push` exits non-zero when all linked-project pushes fail, matching `lnpm push`.
- [ ] Each fix has an integration test in `tests/` using the `setupTest` helper environment.
