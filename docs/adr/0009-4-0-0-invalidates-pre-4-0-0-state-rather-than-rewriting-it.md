# 4.0.0 invalidates pre-4.0.0 state rather than rewriting it

4.0.0 changes every package hash. Two defects made that unavoidable, and they
travel together because the cost is paid once: `pack.HashFiles` wrote its
per-file fields with no lengths and no separators, so a filename could absorb a
record boundary and two different packages could be made to hash identically
(#453); and the lifecycle-script strip ran inside `store.Store`, after publish
had hashed the packed files, so for any package defining `prepare` or
`prepublish` the store did not hold what its hash described (#447).

The fixes are a few lines each. What needed deciding was not how to fix them but
what a 4.x binary does when it meets state a 3.x binary wrote — and that state
is not all lnpm's own: `lnpm.lock` records a `hash` per package, `lnpm restore`
resolves a retreated project through it, and those files are committed to
consumers' repositories.

**A 4.x binary treats pre-4.0.0 state as absent, says so, and changes nothing.**

## What that means in practice

Three version markers already existed, one per artifact, and each is bumped so
the old format is recognisable rather than guessed at:

| Artifact | Marker | 3.x | 4.0.0 |
| --- | --- | --- | --- |
| Store entry | `.lnpm-complete` `schemaVersion` | 1 | 2 |
| Database | bolt `schema_version` | 2 | 3 |
| Lock file / retreat snapshot | `version:` | 1 | 2 |

- A store entry whose marker records an older schema fails `CheckComplete`, so
  every read path treats it as absent. It is **not deleted**: `lnpm gc` reclaims
  it once nothing points at it, and a re-publish hashes to a different directory
  and never collides with it.
- The database rows are **kept**. They carry the project-to-package links, which
  is the one part of a user's state a re-publish cannot reconstruct. The stale
  hashes in them are harmless because the store refuses the entries they name.
- `restore` and `pull` refuse a version 1 lock file outright, naming the file and
  the remedy, rather than resolving package by package and reporting each one as
  missing from a store that is perfectly intact.

The user-visible cost is one re-publish per package after upgrading.

## Why not rewrite the store in place

The obvious alternative is a migration pass: walk the store on first 4.x run,
recompute each entry's hash under the new rules, rename the directory, update the
row. It is rejected on three grounds, in increasing order of weight.

It is a mutating pass over every entry a user has, and it has to be crash-safe
and idempotent to be worth having. That is real work to get right for a
one-time operation, and the failure mode of getting it wrong is a store that is
neither the old format nor the new one.

It cannot recompute anything for #447. The stored `package.json` had its scripts
stripped after hashing; rehashing what is on disk would produce a hash for the
stripped bytes, which is the right answer only by coincidence — the entry would
then be addressed correctly but no longer be reproducible from the source it was
published from.

And it does not solve the part that actually reaches users. A rewrite fixes
`~/.lnpm`, which is one machine. It cannot touch a `lnpm.lock` committed to a
repository, which is where the hashes are shared, so a user with a rewritten
store still meets a stale hash the moment they `restore` a project cloned from
git. Any option has to have an answer for that case, and once the answer is "fail
with an instruction", having a second and different answer for the local store
buys inconsistency rather than convenience.

## Why the cryptographic digest did not ride along

ADR-0007 records a route to tamper evidence that needs no migration: keep
xxhash64 for addressing and record a cryptographic digest beside it, as an
additive field. It would have been defensible to take it here, on the argument
that the migration is paid once.

It was deliberately left out. The digest is additive by construction, so it can
land in any later 4.x minor at no extra cost to users, while the migration is the
part of this release with the potential to lose someone's state. Keeping the two
apart keeps the risky release small. ADR-0007's precondition still holds and is
now met: #447 had to be closed first, or the digest would have been evidence
about everything except the manifest.

## What this is not

It is not a claim that the store is now tamper-evident. #453 closes a framing
hole that made a collision free; it does not make xxhash64 collision resistant,
and ADR-0007 remains the statement of what the hash is for. What resists a
poisoning write is still #333's write protection.

## Consequences

`lnpm doctor` reports pre-4.0.0 entries under their own heading, as a warning
rather than an issue, and does not fail. An upgraded store holds one for every
package the user ever published, so failing on them would fail on every store
that predates the upgrade — and the remediation doctor prints for real damage
("delete any directory listed above") is the wrong instruction for an entry a
re-publish will not collide with. `IncompleteEntryError.Outdated` is what keeps
the two apart, and `lnpm add` branches on the same field.

A lock file that records no `version:` key at all normalises to the *oldest*
format, not the current one. Every such file predates the key, so its hashes are
3.x hashes; calling it current would hand `restore` a hash it could only fail to
resolve, with nothing left to explain why.

The bolt schema is bumped even though no bucket changed shape, so that a 3.x
binary run after a 4.x one meets the existing "written by a newer lnpm" refusal
rather than reading 4.0.0 hashes as its own.
