# The store's content hash is a consistency control, not tamper evidence

> **Superseded in part by 4.0.0.** The decision this document makes — keep
> xxhash64 for addressing, do not migrate to a cryptographic hash — still
> stands. Two things it describes as open have since been fixed, and the text
> below is left as it was written rather than edited in place, because the
> reasoning is what the later issues cite. Read it with these corrections:
>
> - **#453 is fixed.** `HashFiles` length-prefixes each field as of 4.0.0, so
>   the free collision described under *The birthday bound is not the binding
>   constraint* is closed. The 2^32 birthday bound is now the operative figure.
> - **#447 is fixed.** The lifecycle-script strip runs before the packed set is
>   hashed, so the exception described under *Content addressing already has one
>   exception* is gone, and `doctor --verify-content` verifies the root manifest
>   like any other file.
> - **The migration was paid.** Both fixes changed every package hash. What a
>   4.x binary does with 3.x state is ADR-0009.
> - **The metadata digest is still unbuilt**, and its precondition — that #447
>   be closed first — is now met.

lnpm addresses store entries by a 64-bit xxhash and keeps doing so. `HashFile`
is xxhash over a file's bytes; `HashFiles` sorts the packed set by `RelPath` and
folds each file's path, that per-file hash and its `Mode.Perm()` into a second
xxhash; both render `%016x`, and the sixteen characters that come out are the
directory an entry is filed under at `~/.lnpm/store/{name}/{hash}`. xxhash is
not collision resistant and was never meant to be. That is recorded here as a
decision rather than left as an assumption, because #331 asked for it and
because #330, #332, #333 and #439 all reason about store integrity and need
something to cite.

What the hash establishes is that two things that should be the same content
are: the same input packs to the same entry, a changed file changes the entry it
lands in, and a stored file that no longer hashes to what was recorded for it
has been damaged. What it does not establish is that nobody chose the
replacement bytes. Those are different claims and the difference is the whole
subject of this document.

## What the hash actually covers

The function is XXH64, from `github.com/cespare/xxhash/v2`. `pack.HashFile` opens
the file and hashes its bytes and nothing else — no path and no mode.
`pack.HashFiles` is the package-level hash, and it covers three fields per file:
`RelPath`, the per-file `ContentHash`, and
`Mode.Perm()`. Size and modification time are in neither. Only the permission
bits are in the package hash, so ownership and the file type are outside it too.

This is worth stating precisely because two shipped checks depend on the split,
and would be wrong if it were read the other way round. `lnpm doctor
--verify-content` re-hashes stored files with `HashFile` and compares against
the hashes the database recorded, and it can only do that because `HashFile`
carries no mode: store content is write protected after it is hashed (#333), so
the modes on disk deliberately no longer match the modes that went into the
package hash. `fileManifestHash` in `internal/cli/add.go` runs `HashFiles` over
the database's file rows to ask whether a recorded manifest describes the
generation of the package the entry is named for, which works for the opposite
reason — every field it feeds in comes out of the database, never off the disk.

## Why not migrate to a cryptographic hash

The store's path *is* the hash. Changing the function renames every entry in
every existing store, and the entries are not the only place the value is
written down: `lnpm.lock` records a `hash` per package, `lnpm restore` resolves a
retreated project through the content hash the snapshot recorded rather than
through the name, and lock files are committed to consumers' repositories. So
this is a user-facing migration reaching files lnpm does not own, proposed
immediately after 2.0.0, and justified by nothing anybody has observed: #331 was
filed off code reading, no collision was constructed for it, and every path the
sweep traced fails toward stale content rather than attacker-chosen content.

The cost is certain and the benefit is smaller than it looks. A stronger
function would close the collision, but it would not on its own make the store
tamper-evident: no read path re-hashes a store entry — `GetFiles` asks only
whether the entry is complete, and #332's verification reads the *consumer's*
files rather than the store's — so the only thing that would consult the
stronger digest is the check the user has to ask for by name. What stops a
poisoning write is the write protection, not the width of the hash.

## Three checks have landed that a reader could mistake for tamper evidence

When #331 was filed the store checked nothing about content. Since then:

- **#333** write protects store content. Every regular file in a committed entry
  — the completeness marker aside, which is the store's own bookkeeping — is
  chmodded `mode &^ 0222` before the entry is renamed into place, so a write
  through a consumer's hard link fails with `EACCES` instead of rewriting the
  entry every later `add` of that version serves.
- **#332** stops a relink trusting the previous link's manifest. A file the
  incremental path would have carried over unread is re-hashed first, and one
  whose bytes have changed since it was linked is materialised out of the store
  instead.
- **#439** made `lnpm doctor --verify-content` genuinely re-read the store and
  compare every stored file against the hash recorded for it.

A reader arriving at that list — doctor verifies hashes, relink verifies hashes,
the store is content-addressed — could reasonably conclude the store is
tamper-evident. **It is not.** What resists tampering here is #333's write
protection: it makes the poisoning write fail at the filesystem, before any hash
is consulted. The hashes detect corruption, accident, and an ordinary edit by
someone not thinking about the hash. None of them detects a replacement chosen
to hash to the same value, and #439's own comment says so where the check is
implemented, so that the code and this document do not drift apart on it.

The protection is also a lock and not a repair. An entry poisoned before the
upgrade to #333 is protected in its tampered state, and it is `doctor` rather
than the store's own passes that is expected to find one.

## What a collision would actually do

The "fails toward stale" claim is load-bearing, so here is the path, read from
the code rather than assumed.

`Store()` asks whether the entry is already there before it writes anything. It
validates the package name, builds `finalPath` from the name and the hash, and
returns that path untouched if `CheckComplete(name, hash)` reports the entry
complete — so on a hash the store already holds, nothing is written and nothing
is compared. Nothing further down deletes or overwrites an entry either:
`finalize` never removes `finalPath`, and on a losing rename it re-checks
completeness and treats the destination as authoritative. So a second publish
that collided with a first would leave the first publish's bytes exactly where
they are, and consumers of the second would be materialised from them. The
attacker's content never enters the store. The harm is a version that silently
does not update.

The database does move. `insertPackageTx` addresses a record by name and content
hash, so a colliding publish finds the existing record and updates it in place —
overwriting `Version`, `SourcePath` and the size and file counts — and
`insertFilesTx` replaces its file rows with the new publish's. The store then
holds the old bytes while the rows describe the new ones, which is a state
`lnpm doctor --verify-content` reports: `fileManifestHash` still reproduces the
package hash, because that is what was collided, and the per-file comparison
underneath it then fails on every file whose content really differs. So a
package-level collision is loud rather than silent, to anyone who runs that
command. That is a consequence of where the collision sits, not a defence:
colliding each differing file's own `HashFile` value as well defeats the
per-file comparison, and nothing runs `doctor` on its own.

## The birthday bound is not the binding constraint

64 bits puts a birthday collision at roughly 2^32 hashed inputs. That
approximation is the number #331 was filed on, and it is where a reader should
stop treating it as the operative figure: it is an *upper* bound on the work a
collision takes, not the expected cost, because the package-level hash can be
collided with no cryptanalysis and no search at all. Nobody needing a collision
here would pay 2^32 for one. That is #453.

`HashFiles` writes `RelPath`, `ContentHash` and the octal permission bits into
the hash back to back with no separators and no lengths. `ContentHash` is a
fixed sixteen characters, but `RelPath` is arbitrary and the permission field is
one to three octal digits, so the boundary between one file's record and the
next is not recoverable from the stream — and a filename can be chosen to
contain it. Constructed and run against `pack.HashFiles` while writing this, and
reproduced independently before it was filed: a package holding `zfoo` (mode
0644) and `zzbar` (mode 0755) hashes identically to a package holding a single
file named `zfoo<zfoo's 16-hex hash>644zzbar` with `zzbar`'s bytes and mode. The
two sets differ in file count, both are made only of ordinary readable files
with legal names, and the hashes involved are the real ones the files produce.

#453 tracks it rather than this document accepting it, and it does not reopen
this decision: the construction still needs a publisher who controls the package
name — an entry is filed at `{name}/{hash}` and `findPackageByHashTx` matches on
name and hash together, so two names never share an entry or a record — which is
the same actor #331 already assumed, and it still fails in the same direction — the colliding publish is the one that gets
ignored. What it changes is which fix anyone reaching for one should reach for.
Frame the fields, do not widen the hash: length-prefixing `RelPath` and the
permission field closes the whole class for a few bytes per file, where a
cryptographic function costs a rewrite of every store.

The uncomfortable part, which #453 carries and this document should not hide:
that cheap fix is a store-format migration too. Changing the framing changes
every package hash ever computed, which is the same blast radius — store paths,
committed `lnpm.lock` entries, retreat snapshots — that this document just
declined to accept for the weaker version of the same problem. So the cheap fix
is cheap to write and not cheap to land, and the metadata-digest route below is
the one that avoids the migration entirely.

## Content addressing already has one exception, and it is on the manifest

An entry's directory name is a claim about the bytes inside it, and there is one
place today where the claim is false by construction. `Store()` runs
`stripLifecycleScripts` on the entry's root `package.json` *after* publish has
hashed the packed files, removing `prepare` and `prepublish`. For any package
that defines either — and `prepare` is the standard place a build step is wired
in — the stored manifest is not the manifest the content hash was computed over.

That is #447, it is open, and it is not caused by anything in this document.
`doctor --verify-content` has to carve the root manifest out of verification
because of it, reporting such a manifest as unchecked rather than as damage or
as sound, and it narrows the carve-out to bytes that could have come from the
rewrite — a check reconstructing an expectation, not the store holding what it
says it holds. Any statement about what content addressing guarantees has to
carry this exception, and the exception sits on the one file most worth
tampering with: the strip touches `prepare` and `prepublish` only, so `main`,
`bin` and `postinstall` all pass through it untouched.

## If tamper evidence is ever wanted, it does not need a migration

The triage on #331 framed this as document-or-migrate with nothing in between.
There is something in between, and it is recorded here as the route to take if
the requirement ever arrives: **keep xxhash64 for content addressing and record
a cryptographic digest as metadata beside it.** The entry stays at
`{name}/{hash}` computed exactly as it is now, so no path changes, no lock file
is invalidated and no existing store needs rewriting. The digest is an added
field on the package row — and, if per-file verification is wanted, on the file
rows — computed at publish and checked by whatever wants to check it.

It would be additive in the way the pin field of ADR-0006 was additive: written
by the publish path, absent from every record written before it, and read by
code that has to tolerate its absence rather than treat it as damage. The cost
is a second hash over the packed bytes at publish time and a second column; the
cost it avoids is the one the section above priced.

Two things whoever builds it should know before starting. It does not make the
store tamper-*proof*, only tamper-*evident*, and it is evidence of a weaker kind
than it looks: the digest lives in the same database, so anyone able to rewrite a
store entry can rewrite the digest recorded for it. What it catches is a
replacement made without touching `~/.lnpm/lnpm.db` — which is what the hard-link
poisoning of #333 was, a write that reached the store through a consumer project.
And #447 has to be closed first, or the digest inherits the same exception on the
same file and becomes evidence about everything except the manifest.

## Consequences

`same hash = same content` stays true as a statement about accidents and false
as a statement about adversaries, and the comment in `internal/store/store.go`
that carries that phrase points here so a reader meets the distinction where the
assumption is made.

`lnpm doctor --verify-content` keeps its value and does not get oversold. It is
the check that tests the store's central claim, and what it establishes is that
the bytes are the bytes that were recorded — corruption, a truncated write, a
bad disk, an edit by someone not trying to hide it. Its output is not evidence
that an entry was not deliberately replaced, and its documentation should not
imply that it is.

Anything that wants a *security* guarantee about store content builds on #333's
write protection, not on the hash. The two are not interchangeable and a future
issue that treats them as such is proceeding from a false premise.

`SECURITY.md` states the same thing in the terms a reader looking for security
properties will be looking for: a non-cryptographic 64-bit hash, what it
guarantees, and what it does not.

No behaviour changes. One comment in `internal/store/store.go` gained a pointer
here; the hash function, the framing, the store layout and every existing
entry's path are exactly as they were.

## Considered options

**Migrate the store to a cryptographic hash (SHA-256 or BLAKE3).** The direct
fix, and rejected on cost against a benefit nothing observed calls for. Every
entry's path changes, so every existing store needs rewriting or re-publishing;
`lnpm.lock` records the hash per package and those files are committed to
consumers' repositories, so the blast radius reaches beyond `~/.lnpm`; and
`restore` resolves a retreated project by content hash, so a half-migrated
machine has snapshots it cannot resolve. If the requirement ever arrives, the
metadata-digest route above delivers the same evidence without any of that, and
should be the first thing tried.

**Length-prefix the `HashFiles` fields, keeping xxhash.** Closes the framing
ambiguity above for the price of a fixed prefix per field, and is the right
shape of fix — it treats the parsing hole as a parsing hole rather than throwing
a stronger hash at it. Not decided here, because it is not this document's to
decide: it is #453, it changes every package hash ever computed, and it
therefore carries the same migration cost this ADR just declined to pay for a
weaker version of the same problem. Whoever rules on #453 should read this
document's *Why not migrate* section first, and should weigh doing it at the
same time as any other change that has to move the hash, since the migration is
paid once whether one thing changes or three do.

**That ruling has since been made: deferred to 4.0.0**, on 2026-08-27, with
#447 alongside it and for the reason this section anticipated — the migration
is paid once, so the framing fix and the strip-before-hashing fix travel
together. #464 tracks the pair and blocks both, so neither can be picked up
before the migration is actually wanted. 3.0.0 shipped earlier the same day and
the decision came after it, so the carrier is the next major rather than that
one.

**Both landed in 4.0.0, together, as this section said they would.** The fields
are length-prefixed and every manifest rewrite now runs in front of the hash.
ADR-0009 records what a 4.x binary does with the state a 3.x binary wrote, and
why that is a refusal rather than a rewrite.

The deferral ended in 4.0.0. What made it defensible while it lasted was the
failure direction rather than any difficulty, and the
distinction is worth keeping straight: framing costs nothing to break, but
`Store()` returns the existing entry when the hash is already present and never
overwrites, so a collision still serves stale content rather than
attacker-chosen content, and the actor is still someone who controls two
versions of a package they publish. That is an argument for scheduling it, not
for leaving it. If either half of it stops holding — if any write path learns
to overwrite an existing entry, or a collision becomes reachable by someone who
does not already control the name — the deferral expires with it.

**Record the decision only in the code comment, with no ADR.** Rejected because
the comment is at the place the assumption is made and cannot carry the reason
it is safe there, and because #330, #332, #333 and #439 each needed to cite
something. A one-line comment is where a reader ends up, not where they can
learn what the hash is for.

**Close #331 as not-a-defect and write nothing.** This is what the sweep itself
expected might happen, and the reason not to is the reason it was filed: an
unwritten trade-off is indistinguishable from an unnoticed one, and the next
person to read `Same hash = same content` has no way to tell which it was.
