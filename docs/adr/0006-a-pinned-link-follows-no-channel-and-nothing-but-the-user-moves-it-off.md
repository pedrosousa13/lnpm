# A pinned link follows no channel, and nothing but the user moves it off

`lnpm add mylib@9f8e7d6c` links a project to one build of `mylib` rather than to
a channel. That link is pinned, and a pin is a fifth thing a link can say
alongside the four `db.Link` already records. `lnpm pull` leaves a pinned link
where it is, a publish does not carry it forward, `lnpm gc` keeps the build it
names for as long as it names it and with no time bound, and `lnpm restore` puts
it back pinned. Nothing moves a project off a pin but the user, deliberately —
which command they do it with is proposed here rather than settled.

Today none of that is true. An add by hash records `Tag: ""` — which
`Link.tag()` reads as `DefaultTag`, the same as an ordinary add — so `pull`
resolves the link through `latest`, relinks the project onto the current
release, rewrites `lnpm.lock` and repoints the database row off the historical
record, after which nothing reaches that build and the next `gc` collects it.
Bare `lnpm pull` refreshes every package in the lock, so pulling to update one
package undoes another package's rollback with no signal. That is #300; this
records the four decisions it is implemented against, and — in its own section,
because they must not be mistaken for the four — three further things that
checking those decisions against the code turned up, which nobody has ruled on.

## The pin is its own field on `Link`, not a reserved `Tag` value

`Tag` means *follow this channel*. Pinning means *follow nothing*, and there is
no string left in that field to say it with: `Link.tag()` already reads the
empty tag as `DefaultTag`, because that is what every link written before tags
existed meant. A reserved sentinel would therefore have to be a real, spellable
tag name — and tag names are whatever the user types at `lnpm tag <package>
<tag>`, which validates nothing and reserves nothing. `db.DeleteTag` refuses to
remove `latest`; that is the only name lnpm holds back.

So a sentinel lives in a namespace a user can collide with. Someone tags a build
`pinned`, `setTagTx` writes it, and `moveLinksTx` — which decides what a tag
move carries by comparing `l.tag()` against the tag that moved — starts moving
pinned links onto the version that tag now names, which is the exact carry-over
a pin exists to prevent. A separate field makes that impossible rather than
unlikely, and leaves `Tag` with one meaning.

The cost is that the field has to be written at every place a link is
constructed, and every one of them builds a `db.Link` literal field by field
rather than copying a struct: `internal/cli/add.go` twice,
`internal/cli/pull.go` in the repoint block, and `internal/cli/restore.go` in
`recordRestoredLink`. A literal that omits the new field silently unpins.
`db.InsertLink` needs it too, and needs it in a place easy to miss: when the
incoming link names the package record the project is already on, that function
updates the existing row in place and copies only `LinkType` and `Tag` across.
`Tag` is copied there because without it a project switching channel onto the
version it is already on kept following the old one. A pin has the same shape
twice over — `lnpm add mylib@<hash-of-current-build>` must pin, and a later bare
`lnpm add mylib` while that build is still current must unpin — and neither
happens unless that branch carries the field.

## `pull` skips a pinned package with a notice, and refuses when it is named

Bare `lnpm pull` refreshes every package in the lock, so one pinned package must
not stop the rest. It is skipped — and reported, because silence is what makes
the current behaviour a defect rather than a preference. `RunPull` already has
the shape: a package added with `--link` prints `Pulling <name>... skipped (live
link to source)` and is counted into a closing `Skipped N live-linked
package(s)` line, kept apart from the up-to-date count precisely so a skip is
never read as a comparison that was made.

`lnpm pull <pinned-pkg>` is different and behaves differently. Naming a package
is a request rather than a sweep, and the request cannot be honoured: a newer
build is sitting in the store and the link says not to take it. So it refuses,
and the message says how to unpin. This is deliberately not what the live-link
skip does — naming a live-linked package explicitly still skips it — and the
asymmetry is the point. A live link has nothing to refresh, so a skip is a
complete answer. A pinned link has something to refresh and a reason not to, so
the user who asked has to be told which of the two they are in.

The refusal has to point somewhere real, and no command unpins today. Which one
should is proposed rather than decided, under *What this ADR raises but does not
settle* below, along with the one constraint that binds whatever answer is
chosen.

## A publish must not carry a pinned link forward either

This section is not a fifth decision. It is a defect in the premises the four
were decided on, found while writing them down, and it is listed for
ratification under *What this ADR raises but does not settle*. It is stated here
rather than only there because the `gc` decision below does not hold without it:
a pin a publish has dragged forward still roots something, just not the build
the user pinned.

`moveLinksTx` filters on the tag alone:

```go
if err := json.Unmarshal(data, &l); err != nil || l.tag() != tag {
    stay = append(stay, id)
    continue
}
```

A pinned link reads as `latest` there, because `Tag` is empty. That does not
matter for a pin on a superseded build — `setTagTx` only walks the links of the
record the tag named immediately before, so a build two generations back is out
of reach — but it matters for a pin on the build `latest` currently names, which
is an ordinary thing to want and an ordinary thing to type: `lnpm add
mylib@1.3.0` resolves to the current record when the current record carries that
version, and `lnpm list mylib --versions` prints the current build's short hash
for anyone who would rather type that. The very next `lnpm publish` then moves
`latest` off it and drags the pin along, before `pull` is ever run.

So `moveLinksTx` has to skip a pinned link regardless of tag. #300's "what does
hold" section does not cover this: it reasons about generations, and this is the
case where the pinned build and the tag's previous build are the same record.
#300 has no acceptance criterion for a `moveLinksTx` change either, which is why
this is raised rather than recorded.

## `gc` keeps a pinned build with no time bound, and a pin does not expire

This costs `gc` no new rule. Its reachability arithmetic counts every link a
package has — `validLinks := len(links) - countLinksForPackage(linksToRemove,
pkg.ID)`, where `linksToRemove` holds only links whose project directory is gone
— and it never consults a link's tag. A pinned link is a link, so it is already
a root, and the reason a rolled-back build is collected today is not that `gc`
discounts the link but that `pull` moved it first. What this decision settles is
that no expiry is added on top.

A pin is an explicit statement that this build matters. Ageing it out would
collect deliberately-preserved data on a timer, which is the shape of data loss
#335 exists to prevent — that issue stopped `gc` deleting store content because a
project's drive happened to be unmounted, and a TTL is the same trade with the
clock in place of the mount. Reclamation stays deliberate: unpin, `lnpm remove`,
or `lnpm forget` (#382) once the project itself is gone for good.

"Indefinitely" is bounded by the link existing, and there is one window where it
does not. `lnpm retreat` calls `database.DeleteLink` for every package it
unlinks, so between a retreat and the matching restore the pinned build has no
root at all and a `gc` in between collects it — after which restore reports the
build is no longer in the store and asks for a re-publish. That is not new and
not specific to pins: it is true of every build a retreated project was on, and
restore already words the failure. It is stated here because a pin is the case
where the loss is least recoverable, the build being one nobody can re-publish
from a current source tree. Closing it means keeping a root across a retreat,
which is a larger change than #300 and is not decided here.

One naming point for the implementer, because the word is already taken in this
file: `gc.go` has `pinnedByTag`, which asks whether a tag other than the default
one names a version. That is the ADR-0002 rule, not this one. Whatever the new
predicate is called, it must not be mistaken for that one, and the two must not
be folded together — they answer different questions about different records.

## `restore` reinstates the pin, which means the lock file records it

`RunRestore` resolves each package through the content hash the snapshot
recorded rather than by name, and that is what makes a pin restorable at all:
the build comes back exactly. What does not come back is the link's state.
`recordRestoredLink` writes a `db.Link` with no `Tag` on purpose, because
nothing ever recorded the channel a consumer was on and guessing from the tags
that name the build today would be a guess about a decision made months ago. The
file-header comment on `restore.go` lists a pin's three siblings — whether the
package was added with `--link`, which dependency field it was in, and the
channel — as the parts of the pre-retreat state the lock file does not record
and restore therefore cannot rebuild; `warnIfOffTheDefaultChannel`'s comment
restates the list. (`recordRestoredLink`'s own comment argues only the channel,
which is why it is the wrong place to read the set from.) Without this decision
a pin becomes a fourth, and a project comes back following `latest` after being
restored onto a build it pinned. That is #300's own defect in another place: a
deliberate state silently undone.

The snapshot is not a separate format. `lnpm retreat` renames `lnpm.lock` to
`lnpm.lock.retreat` — `stashLockForRestore` does a plain `os.Rename`, or merges
into an existing snapshot entry by entry — so the snapshot is a lock file and
its entries are `lockfile.Package`. Recording a pin therefore changes
`lnpm.lock` itself, a file projects commit, and not only the snapshot. The field
is additive and `Load` already tolerates a missing `version:` key by reading it
as `currentVersion`, so an old lock file reads fine; the direction that loses
information is an older lnpm re-saving a newer lock, which drops the field and
unpins on the next write.

That leaves two places holding the pin, and the database is the authority. The
link row is what `pull`, `push` and `gc` read; the lock entry is the transport
that lets `restore` rebuild the row, exactly as the content hash already is.
`RunStatus` reads the lock for its "Current Project" block and would show the
pin from there, which is fine for a display and is not a licence for any command
that acts to read it from there.

`warnIfOffTheDefaultChannel` needs to know about pins too. It fires after a
restore when the restored build is not the one `latest` names, and when no tag
names that build it prints *has been published since the retreat* and tells the
user to run `lnpm pull`. For a restored pin that advice is exactly backwards —
and, after the second decision above, points at a command that will refuse.

## What this ADR raises but does not settle

Three things below are not among the four decisions. They came out of checking
those four against the code, each is argued for, and none of them has been ruled
on. A reader must not implement them as though they had been. They are carried
to #300 as questions.

**The `moveLinksTx` change, above.** The finding is a fact about the code and is
not in question: a pin on the record a tag is moving off is carried forward
today. What is in question is that fixing it is a fifth change to a function
#300 currently promises not to touch — its "what does hold" section cites
`moveLinksTx` as something the fix must preserve. Preserving its behaviour and
honouring a pin are not compatible, so one of the two has to give, and which is
not this document's call.

**Pinning by exact version, not only by hash.** #300's acceptance criterion says
`lnpm add <pkg>@<hash>` records a pinned link. This ADR says `lnpm add
<pkg>@1.2.0` does too. That is an inference from `resolveAddSpec`, which returns
an empty tag for `specVersion` and `specHash` alike because both name a build
rather than a channel, and from the README, which offers both identifiers as the
way to roll back — *Either identifier the listing prints works*. It is very
probably what was meant. It was not asked for, and pinning on a version is a
wider behaviour change than pinning on a hash: a version is what most people
type, so it is the spelling that decides how often a pin happens by accident.

**Which command unpins.** The `pull` decision above requires `lnpm pull
<pinned-pkg>` to refuse with a message naming the way to unpin, and nothing
unpins today. The proposal here is `lnpm add <pkg>` with no `@suffix`: it
resolves through `packages_by_name`, returns `specDefault`, and is already how a
user says *follow the default channel again*. The only alternative in the CLI as
it stands is `lnpm remove`, which unlinks the package as well and so is not an
unpin. That reasoning is a derivation from what exists, but which command
carries the meaning is a UX decision, and a dedicated `lnpm unpin` or an `lnpm
pull --unpin` are both defensible answers this ADR has no standing to reject.

Whichever is chosen, one constraint on it is not a matter of taste:
`db.InsertLink` updates in place when the incoming link names the record the
project is already on, copying only `LinkType` and `Tag` across. Unless the pin
field is copied there too, no add-shaped unpin works while the pinned build is
still the current one — the command would report success and change nothing.

## Why this is consistent with ADR-0002

ADR-0002 says `latest` is not a garbage collection root. This says a pin is one.
Those are the same rule read twice.

ADR-0002's reasoning is about who decided: every publish moves `latest` onto
what it just wrote, so `latest` always names something and names it without
anyone choosing to, while a tag someone set is a decision to keep a build.
Counting `latest` would leave `gc` unable to collect any current version of any
package — the command would run, report nothing and free nothing.

A pin is the strongest available form of the decision `latest` lacks. A tag says
*keep this build, and let people follow the channel to it*; a pin says *this
project is on this build and is not to be moved off it*. It is written by a
person, per project, naming one build, and nothing but that person changes it.
It does not have `latest`'s failure mode either: a pin exists only where someone
typed a hash or a version, so a store nobody has rolled back in has no pins and
`gc` reclaims exactly what it reclaims today.

## The obvious workaround does not exist

A reader reaching this point may propose the cheap alternative: tag the good
build, and have the consumer add under that tag. It is not available. `RunTag`
calls `database.GetPackageByName`, which resolves through the `packages_by_name`
index — the current build — and hands that record's content hash to `SetTag`.
There is no way to name a superseded build to `lnpm tag`, so the build a
consumer wants to stay on cannot be given a name to follow. Widening `lnpm tag`
to accept a hash is a real option and a different issue; it is not a substitute
for a pin, because a tag is still a channel and the next `lnpm tag` moves it.

## What already holds, and must keep holding

The rollback survives everything the *publisher* does, and that is worth stating
because #300's fix must not quietly cost it. Verified against the current code:

`setTagTx` reads the hash the tag named before the write, resolves it to a
record and hands only that record to `moveLinksTx`, so a link two or more
generations back is never a candidate to be carried forward. A pin on such a
build is safe from a tag move for that reason alone — which is why the case that
is *not* safe, a pin on the record the tag is moving off, is called out above.

`push` and `publish --push` both enumerate consumers with
`database.GetProjectsForPackage(pkg.ID)`, where `pkg` is the record just
written, so a project linked to an older record is not a push target.
(`pushToLinkedProjects` in `publish.go`, and the same call in `push.go` after
the store and database writes.)

`restore` resolves through the snapshot's content hash, falling back to the name
only for an entry with no hash — which no lnpm has ever written, since
`lockfile.Package` has carried `Hash` since the file's first commit.

## Consequences

`lnpm add mylib@9f8e7d6c` and `lnpm add mylib@1.2.0` both pin. Resolution by
content hash and resolution by exact version are the same act — both name a
build rather than a channel, and `resolveAddSpec` returns an empty tag for both
— so both must set the field. `lnpm add mylib` and `lnpm add mylib@beta` do not
pin, and `lnpm add mylib` on a pinned package unpins it. Two halves of that
paragraph are proposals rather than decisions — pinning on a version, and `lnpm
add` being the unpin — and both are listed above under *What this ADR raises but
does not settle*. Only pinning on a hash was asked for.

Bare `lnpm pull` in a project with a pinned package refreshes everything else
and prints that it left that one alone and why. `lnpm pull mylib` on a pinned
`mylib` fails, tells the user it is pinned, and names the command that unpins
it. Nothing in either case rewrites the lock entry or repoints the database row.

`lnpm gc` keeps a pinned build for as long as the pin exists, however long that
is. Reclaiming it takes two steps, as ADR-0002 already made it take two steps
for a tagged build: drop the pin, then collect.

A publish of a package some project has pinned to the build being superseded no
longer drags that project forward, and no longer includes it in the `--push`
consumer list — which it already did not, since the pin keeps the link on the
older record.

`lnpm retreat` followed by `lnpm restore` returns a pinned project pinned. The
pin travels in `lnpm.lock`, so the lock file gains a field; it is optional and
absent from every lock file written so far, which reads as unpinned.

`lnpm status` gains a way to see that a package is pinned. It shows no channel
today — neither the "Active Links" table nor the "Current Project" block prints
a link's tag — so this is a new column rather than an extension of one. The
state is currently unnamed anywhere: `lnpm list <pkg> --versions` does show a
project sitting on a superseded build, in its `LINKED IN` column, but that is
the symptom rather than the pin, and nothing distinguishes a project that chose
to be there from one that has simply not pulled.

`README.md`'s "Version History and Rollback" section currently ends with *There
is no pinned link yet: re-run `lnpm add mylib@<hash>` to go back.* That sentence
goes when #300 lands.

The surfaces a pin touches, in full, so the implementer has the list rather than
finding it a command at a time: `add` writes it, `pull` honours it two ways,
`push` and `publish --push` must not move it, `gc` roots on it, `restore` and
`retreat` carry it, `status` shows it, `remove` drops it with the link, and in
`internal/db` both `InsertLink` and `moveLinksTx` have to know about it. `check`
and `doctor` do not: neither reads a link's channel.

## Considered options

**A reserved `Tag` value.** Smaller — no schema change, no new field to thread
through four construction sites and two database functions. Rejected because tag
names are unvalidated user input, so the sentinel is collidable, and because
`Tag`'s meaning is "channel to follow": a value meaning "follow nothing" makes
every reader of that field ask which kind of tag it is holding. `Link.tag()`
already spends the empty string on the default channel, so the sentinel would
have to be a plausible word someone might genuinely type.

**`pull` skips a pinned package silently.** This is the smallest change and it
is the current bug's own failure mode. #300 is a report about a deliberate state
being undone without a signal; a fix that leaves the signal out fixes the
outcome and keeps the cause.

**`pull` refuses in both cases, with a flag to move a pin.** Consistent, and
rejected because bare `lnpm pull` is a sweep over the whole lock. One pinned
package would fail the command and stop the other twenty being refreshed, and
the flag would be the thing a user reaches for reflexively — at which point it
moves every pin in the project, which is worse than the state we started in.

**Pins expire after some interval.** The argument for it is that space is
otherwise reclaimed only by hand, and a pin left behind by someone who has
forgotten about it holds a build forever. Rejected: an expiry deletes data a
user deliberately preserved, on a schedule they did not set, at a moment they
are not watching — the failure shape #335 exists to prevent. The leak it avoids
is bounded by the number of rollbacks anyone actually performs, and #382's `lnpm
forget` addresses the case that actually accumulates, a project that no longer
exists.

**`restore` leaves the pin off and warns.** This is what restore already does
for the channel, and the precedent is real: `recordRestoredLink` declines to
guess a tag because nothing recorded one. But the two are not alike. A channel
was never written down anywhere, so restoring one would be an invention; a pin
is a fact the lock file can carry, alongside the content hash it already carries
for the same purpose. Declining to record a fact that is available is not
caution.
