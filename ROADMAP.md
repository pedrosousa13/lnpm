# lnpm Roadmap

## What lnpm does today

lnpm publishes a package into a local store and links it into other projects on
the same machine, so you can develop a library against its consumers without
going through a registry. The shipped commands cover:

- **Publishing and consuming** — pack a package into the store, add it to a
  project either as a store snapshot or as a live link to the source directory,
  and remove it again.
- **Syncing** — push a package's changes out to every project linked to it, or
  pull a project's linked packages back into line with the store.
- **Inspecting** — show what the current project has linked, what the store
  holds, which projects consume a given package, and diagnose common problems.
- **Maintaining** — collect unreferenced store entries, undo every change lnpm
  made to a project, fail if a `package.json` still carries lnpm references
  before you publish to npm, read or change lnpm's own settings, generate shell
  completions, and update lnpm itself.

The [command table in the README](README.md#commands) is the authoritative list,
with the flags for each command. [ARCHITECTURE.md](ARCHITECTURE.md) covers the
storage layout, the linking strategy and the data flow, and its Command Design
Notes record why particular commands behave as they do rather than repeating
that table.

Where state lives:

| What | Where |
|------|-------|
| Published package contents | `~/.lnpm/store/{name}/{hash}/` |
| Packages, projects, links and files | `~/.lnpm/lnpm.db`, a [bbolt](https://github.com/etcd-io/bbolt) database |
| Linked packages inside a project | `.lnpm/{name}/` — a copy of the store entry, or a link to the source directory; `node_modules/{name}` symlinks to it |
| Which packages a project has linked | `lnpm.lock` in the project, YAML |
| Configuration | `~/.lnpm/config.yaml`, YAML — see [Configuration](README.md#configuration) |

The store root holds the first two rows — the store directory and the database.
It is `~/.lnpm` by default; `store_path` in the config moves it, and the
`LNPM_STORE` environment variable overrides both. Nothing else in the table
follows it: `.lnpm/` and `lnpm.lock` belong to the consuming project, and the
config file stays at `~/.lnpm/config.yaml` wherever the store goes (`LNPM_CONFIG`
is what moves that one).

## Planned work

Planned work is not listed in this file. It lives in the issue tracker, so it
stays current as issues are filed, triaged and closed:

- **[Open issues](https://github.com/pedrosousa13/lnpm/issues)** — one issue per
  planned change. Some are still untriaged rather than queued.
- **[Milestones](https://github.com/pedrosousa13/lnpm/milestones)** — those
  issues grouped by theme, each milestone showing its own open/closed progress.

The repo's [label list](https://github.com/pedrosousa13/lnpm/labels) shows the
categories and priorities issues are filed under, if you want to filter.

## History

Until [#202](https://github.com/pedrosousa13/lnpm/issues/202) this file was
lnpm's implementation plan. It was written alongside the first code rather than
before it: commit `aaf2d65` added `ROADMAP.md` together with the initial
`cmd/lnpm`, `internal/cli`, `internal/db`, `internal/link`, `internal/pack`,
`internal/store` and `pkg/lockfile`. What made it stale was the divergence that
followed — it planned a storage engine lnpm never used, and of the ten Go
dependencies it listed only four are in `go.mod` today, with all 135 of its
checkboxes still unchecked. It is left in git history rather than reproduced
here — `git show f053f57:ROADMAP.md` — because it records an intended design
rather than the tool as built.
