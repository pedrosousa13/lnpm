# AGENTS.md

## Agent skills

### Issue tracker

Issues live in the repo itself — GitHub issues on pedrosousa13/lnpm, via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Canonical label names, as repo labels on pedrosousa13/lnpm — plus `in-progress` and `P0`–`P3`, labels that stand in for a missing field. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context. `docs/adr/` at the repo root holds the decisions; there is no `CONTEXT.md`, and `docs/agents/domain.md` says to proceed without one rather than create it upfront.

### Verification discipline

What counts as evidence here, and the failures each rule was earned by — comments are
reviewable assertions, a test must go red for the reason you think, and some claims only CI can
settle. See `docs/agents/verification-discipline.md`.
