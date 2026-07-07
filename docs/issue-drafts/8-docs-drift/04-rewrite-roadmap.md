TITLE: Rewrite ROADMAP.md to reflect actual implementation
LABELS: docs

## Severity

Medium — `ROADMAP.md` is linked from the README as the current feature roadmap, but it's a pre-implementation design document that predates the Go rewrite; it describes a different tech stack than what exists and marks long-shipped commands as pending work, actively misleading anyone using it to gauge project status.

## Background

`ROADMAP.md` was written before lnpm's current implementation and lists the dependencies and architecture that were originally planned. The codebase has since diverged significantly (different storage engine, different dependencies, different config format), but `ROADMAP.md` was never updated, and every checkbox in it is still unchecked — even for commands that have worked for a long time. The README links to it as the live "Planned features" page.

## Problem

`ROADMAP.md:3-23` lists Go dependencies:
> `github.com/spf13/viper`, `modernc.org/sqlite`, `github.com/charmbracelet/lipgloss`, `github.com/charmbracelet/bubbles`, `github.com/fatih/color`, `github.com/pelletier/go-toml/v2`

None of these appear in `go.mod`.

`ROADMAP.md:54-59` ("### 1.2 SQLite Database"):
> `- [ ] Database initialization with migrations`
> `- [ ] Database location: `~/.lnpm/lnpm.db``

lnpm does not use SQLite anywhere.

`ROADMAP.md:206-208` (under "### 4.4 Configuration"):
> `- [ ] Global config (`~/.lnpm/config.toml`)`
> `- [ ] Project config (`.lnpmrc`)`

lnpm's config is YAML-based, not TOML/`.lnpmrc`.

`README.md:71`:
> `- **[Roadmap](ROADMAP.md)** — Planned features and improvements`

This presents a stale design doc as the current, forward-looking roadmap.

Every checkbox in `ROADMAP.md` is unchecked (`- [ ]`), including for features that have been implemented and released for a long time: `lnpm publish` (line 77), `lnpm add` (line 87), `lnpm push` (line 120), `lnpm gc` (line 174), shell completions (lines 199-203), and `lnpm retreat` (line 244).

## Where to look

- `ROADMAP.md:3-23` — the dependency list.
- `ROADMAP.md:54-59` — the SQLite Database section.
- `ROADMAP.md:206-208` — the `config.toml`/`.lnpmrc` configuration section.
- `ROADMAP.md:77`, `:87`, `:120`, `:174`, `:199-203`, `:244` — representative unchecked items for features that have shipped.
- `README.md:71` — the roadmap link text.
- `go.mod:1-18` — the real dependency list (`cobra`, `bbolt`, `yaml.v3`, `xxhash/v2`, `doublestar/v4`, `ants/v2`).
- `go run ./cmd/lnpm --help` — shows `publish`, `add`, `push`, `gc`, `completion`, and `retreat` as real, working commands.

## How to fix

1. Decide the replacement approach: (a) rewrite `ROADMAP.md` to describe the real current implementation plus real future plans, or (b) replace its contents with a short, accurate summary plus a pointer to GitHub issues/milestones for planned work (recommended — a hand-maintained design doc drifts again otherwise).
2. If using approach (b): reduce `ROADMAP.md` to a short paragraph describing lnpm's current feature set (pointing to the README's command table for details) and a link to GitHub issues/milestones for planned work; remove the dependency list, SQLite section, and TOML config section.
3. If using approach (a): update the dependency list to match `go.mod`; replace "SQLite Database" with a section describing the actual `~/.lnpm/lnpm.db` bbolt store; replace the `config.toml`/`.lnpmrc` section with the real YAML config path and format (see `internal/config`); check off or remove every item for a feature that has already shipped (publish, add, remove, push, status, list, doctor, gc, retreat, completions).
4. Update `README.md:71`'s link text to accurately describe whichever replacement was chosen (e.g. "Project history and future plans" or "Open roadmap items on GitHub").

## Acceptance criteria

- [ ] `ROADMAP.md` no longer lists dependencies absent from `go.mod` (viper, sqlite, lipgloss, bubbles, fatih/color, go-toml).
- [ ] `ROADMAP.md` no longer describes a SQLite-backed store or a `config.toml`/`.lnpmrc` configuration scheme.
- [ ] `ROADMAP.md` does not present already-shipped commands (publish, add, push, gc, completions, retreat) as unchecked/pending work.
- [ ] `README.md`'s roadmap link text accurately describes what `ROADMAP.md` now contains.

## Testing

```
grep -n "viper\|modernc.org/sqlite\|lipgloss\|bubbles\|fatih/color\|go-toml" ROADMAP.md
```

Should return nothing. Confirm the real stack for comparison:

```
cat go.mod
```

Confirm shipped commands work, for cross-checking against whatever `ROADMAP.md` claims:

```
go run ./cmd/lnpm --help
```
