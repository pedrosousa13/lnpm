# The manifest is unexcludable, and a pack that lost it fails rather than ships

An `.npmignore` line reading `package.json` really did remove the manifest from
the pack, and so did a `files` field that did not list it. `lnpm publish`
reported success and produced a tarball nothing downstream can read a name or a
version out of (#301). The damage did not stop at the tarball: the manifest is
the only file carrying the version string into the hashed content, so with it
dropped, two releases of the same tree hashed identically, and `insertPackageTx`
treated them as one record and overwrote its version in place (#196). npm
permits neither route. `collectFiles` now force-includes the package root's own
`package.json` past every selection rule, and `Pack` refuses outright if the
finished set somehow holds none.

Force-including past the maintainer's own `.npmignore` means a publish can ship
a file they excluded by hand, which is a publish doing more than it was asked,
and `docs/adr/0001` calls that direction a bug in as many words: *"publishing a
package the maintainer deliberately excluded is not recoverable by the person it
surprises, while publishing too few is visible to the one running the command."*
This is not a swallowed error, so 0001 does not literally govern it, but it runs
against 0001's direction rule and a reader deserves to hear that here rather
than discover it in a tarball.

It is accepted for a stronger form of the reason `docs/adr/0004` accepted the
same widening for `main`. 0001 tolerates the fail-closed direction because
publishing too few is *visible to the one running the command*. Here it is
visible to nobody: no check on the publish path asks the packed set whether it
holds a manifest, `lnpm publish` prints success, and the defect surfaces when a
consumer tries to resolve the package. The other side of the trade is unusually
cheap, too. The file this newly ships is the one file every package must have,
so a maintainer who genuinely meant to exclude it was asking for something that
is not a package, and nothing about the intent is worth preserving.

> **Amended by #321.** In the next two paragraphs read `hardReservedExcludes`
> for `defaultExcludes`, `lowerHardReservedExcludes` for `lowerDefaultExcludes`,
> `isHardReserved` for `isDefaultExcluded`, and
> `TestPackManifestCannotDefeatHardReserved` for the test they name. Read
> "appending `package.json` to both slices" for "recomputing": the test appends
> the name to the plain slice and to the lowered one, rather than rebuilding
> either. The boundary the paragraphs describe is unchanged; the list it is drawn
> against was split. See "Amendment: #321 splits the built-in list" at
> the end of this document.

What this does not override is `defaultExcludes`. That boundary is the one
`isDefaultExcluded` already argues and 0004 restates for `main` — the user's
ignore patterns are a preference the user expressed, the built-in list is a
guard lnpm applies on the user's behalf, and a guard anything can step around is
not a guard. The force-include therefore sits *below* the `isDefaultExcluded`
check in the walk, never above it.

That ordering is invisible to an ordinary fixture, because no `defaultExcludes`
entry matches `package.json`, and an invariant nothing can observe is one a
refactor will quietly break. `TestPackManifestCannotDefeatDefaultExcludes` puts
`package.json` into `defaultExcludes` for its own duration — recomputing
`lowerDefaultExcludes`, which is what `isDefaultExcluded` actually reads — and
pins that the guard wins: the manifest is dropped and `Pack` then refuses. It
goes red when the force-include is hoisted above the guard, packing
`[index.js package.json]` in both whitelist and non-whitelist mode. It mutates
package-level state, so it does not run in parallel.

## Consequences

`Pack` can now fail where it previously could not, and that is a change to a
library entry point rather than to the publish contract alone. `docs/adr/0004`
rejected exactly this for `main`, and the two are not in conflict. A missing
`main` leaves a package that is present and does not load, and
`validation.ValidatePackage` already refuses a `main` that is not on disk before
an ordinary publish packs anything, so an abort there would have duplicated a
check while changing the contract for callers with no publish semantics. A
missing manifest leaves something that is not a package at all, and *no* existing
check catches it: `readPackageJSON` reads the manifest from disk rather than
from the packed set, so it passes in precisely this case. 0004's reasoning about
`main` stands; its rejected-abort option is superseded for the manifest only.

The backstop is not the mechanism. `collectFiles` is what makes the manifest
unexcludable; `requireManifestPacked` catches the routes that never reach a
selection rule. One such route exists today and needs no contrivance: a
symlinked `package.json` reads and parses fine, because `readPackageJSON` goes
through `os.ReadFile`, while the walk skips every symlink above any include
check, so the force-include cannot put it back.
`TestPackFailsWhenManifestIsNotPacked` is the fixture.

The exemption is narrower than the always-included set it sits beside, and
narrower again than `main`'s. `README*`, `LICENSE*` and the rest stay exempt
from the `files` whitelist only, and an ignore pattern still drops them; `main`
beats the whitelist and the ignore patterns under a `files` field; the manifest
beats everything except `hardReservedExcludes`, in every mode — since #321 it
outranks `defaultExcludes` too, inertly, as the amendment at the end of this
document records. A reader who finds
three treatments in one `switch` should not make them consistent without
deciding, for each, whether losing the file breaks the package, impoverishes it,
or leaves it unresolvable.

It is anchored at the package root by exact equality, not by basename. A
`sub/package.json` is another package's manifest or a fixture, and the user's
patterns still govern it — the same anchoring `isDefaultInclude` applies after
#320. `TestPackManifestForceIncludeIsRootAnchored` pins it.

One test fixture depended on the bug.
`TestPrepareManifestWithoutPackedManifestFails` built its manifest-free set by
writing an `.npmignore` naming `package.json`, which worked only because the
manifest really was droppable. It now filters the slice `Pack` returned, which
is the same value any caller of `PrepareManifest` hands over. Both names are
what 4.0.0 renamed them to; the test is at
`internal/pack/workspacedeps_test.go:523` and the function at
`internal/pack/workspacedeps.go:77`.

## Considered options

**Force-include the manifest past both the whitelist and the ignore patterns, in
every mode, and abort when it is missing anyway.** Chosen. It is the only option
that closes #301 on every route, and the widening it costs is confined to one
path that every package must contain.

**Force-include all of `defaultIncludes` past the ignore patterns, not just the
manifest.** Rejected. #301 puts the rest of the set out of scope in as many
words, and the two are not the same question: a package missing its `README` is
poorer, a package missing its manifest does not resolve. Widening to the whole
set would override ignore patterns for a dozen name globs to fix a bug reported
against one exact path.

**Treat the manifest as `main` is treated — force-included under a `files`
whitelist only.** Rejected. Two of #301's three reproductions have no `files`
field at all: an `.npmignore` or a `.gitignore` naming `package.json` drops it
during the non-whitelist prune. Scoping the fix to whitelist mode would close
the reported symptom on one route and leave two open.

**Warn instead of aborting, matching `warnMainEntryNotPacked`.** Rejected. A
warning is the right answer when something downstream still refuses the bad
result, and for `main` something does — `validation.ValidatePackage`. Nothing
refuses a manifest-free pack, so a warning would leave `lnpm push` publishing an
unresolvable package with a line of output the user may not be watching for,
which is the outcome `docs/adr/0001` rejects for warnings generally.

**Put the abort in `lnpm publish` rather than in `Pack`.** Rejected for the
reason 0004 gives for putting `warnMainEntryNotPacked` in `pack`:
`internal/cli/publish.go` is one of three callers, `internal/cli/push.go` packs
twice with no validation at all, and `publish --skip-validation` turns the
existing checks off. A check wired into the publish path alone would leave the
other routes exactly as silent as #301 found them.

**Exempt the manifest from `defaultExcludes` too, so the force-include can sit
anywhere in the walk.** Rejected. No `defaultExcludes` entry matches
`package.json`, so it buys nothing today, and it costs the invariant that
`isDefaultExcluded` is evaluated first and alone. Keeping every force-include on
the same side of that check is what stops a reader concluding the side does not
matter.

## Amendment: #321 splits the built-in list

#321 split `defaultExcludes` into `hardReservedExcludes` — the half nothing
publishes — and a `defaultExcludes` that any explicit selection outranks. The
manifest force-include still sits below the first and now sits above the second.

That second half is a real precedence move and an inert one. No entry in either
list matches `package.json`, so nothing observable changed for any package; what
changed is where the invariant now sits, and a reader restoring the old
placement would be adding a fresh explicit check for no effect.

It is not an exemption carved out for the manifest either. After the split, a
`files` entry and an `!` negation both outrank `defaultExcludes` by
construction, and force-including the manifest is one more selection made
explicitly. `main` is the deliberate exception: it carries an explicit
`isDefaultExcluded` check in its own arm, because a `main` naming a `.env`, a
log or a `*.tgz` would otherwise publish it, and `docs/adr/0004` keeps that
failing toward a warning rather than toward a leak. The manifest needs no such
check, because `package.json` is not a name either list can plausibly grow.

`TestPackManifestCannotDefeatDefaultExcludes` became
`TestPackManifestCannotDefeatHardReserved` and now seeds `package.json` into
`hardReservedExcludes`. Seeding it into `defaultExcludes` instead would leave
`Pack` succeeding — correctly, by the rule above — so the old spelling would
have failed for the new right behaviour rather than for the bug it exists to
catch.

The rejected option in "Considered options" above reads differently after the
split. "Exempt the
manifest from `defaultExcludes` too" is now the shipped behaviour, arrived at as
a consequence of moving that list into `ignoreLoader.excludes` rather than as a
fresh decision. What stays rejected is exempting the manifest from
`hardReservedExcludes`, and the reason is unchanged — keeping every
force-include on the same side of that one check is what stops a reader
concluding the side does not matter.
