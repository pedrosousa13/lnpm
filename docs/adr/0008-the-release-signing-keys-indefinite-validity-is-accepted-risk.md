# The release signing key's indefinite validity is accepted risk

`internal/releasekeys/keys/release.pem` carries no expiry, and lnpm adopts no
rotation cadence for it. A release key is replaced when there is a reason to
replace it — a suspected compromise, a change of custody, a move to a different
algorithm — and not on a schedule. That is a decision rather than an omission,
which is the whole reason this document exists: #492 was filed because nothing
in the repository said which of the two it was, and an unwritten trade-off is
indistinguishable from an unnoticed one.

This is not a decision that lnpm cannot rotate. It can, today, with no code
change, and the distinction between *no cadence* and *no ability* is the one
this document is most concerned a reader gets right.

## What the key is and what trusts it

There is one key. `internal/releasekeys/keys/` holds a single file,
`release.pem`, and `openssl pkey -pubin -in internal/releasekeys/keys/release.pem
-noout -text` reports a 256-bit key on `prime256v1` — P-256, which is what
`TestTrustedYieldsP256KeysOnly` in `internal/releasekeys/releasekeys_test.go:15`
asserts of every embedded key. Its private half is the `RELEASE_SIGNING_KEY`
repository secret, written to the runner at
`.github/workflows/release-please.yaml:103` and handed to goreleaser through
`.goreleaser.yaml:66`.

The public halves are compiled in. `internal/releasekeys/releasekeys.go:28`
embeds the directory as a glob — `//go:embed keys/*.pem` — and `Trusted()` at
`internal/releasekeys/releasekeys.go:42` globs `keys/*.pem` out of that embed,
parses each as an SPKI PEM, and returns the ECDSA public keys. An embed holding
nothing is an error rather than an empty slice
(`internal/releasekeys/releasekeys.go:61`), because a build trusting no key
silently refuses every release.

One path consumes them. `internal/cli/update.go:114` binds
`trustedReleaseKeys = releasekeys.Trusted`; `fetchSignedChecksums` calls it at
`internal/cli/update.go:612` and refuses the update unless `verifySignature`
accepts the release's `checksums.txt.sig`
(`internal/cli/update.go:618`). `verifyRelease` at
`internal/cli/update.go:574` then matches the downloaded archive against the
signed `checksums.txt`, and `downloadBinary` runs it before anything is
extracted (`internal/cli/update.go:507`). So the key's authority is exactly
this: it decides which bytes `lnpm update` will replace the running binary with.

## Multiple trusted keys are already a supported state

The glob is the load-bearing detail. `verifySignature` at
`internal/cli/update.go:662` loops over every trusted key and returns true if
any one of them verifies the signature, and its own comment says why: "so that
rotating the signing key does not break updaters built while an older key was
the only one embedded". `releasekeys.go:10`'s *Why a list* section says the same
thing from the other end — publish releases signed by the new key while the old
key is still embedded, and drop the old key only once nobody is running a build
that lacks the new one.

The release pipeline already treats that directory as a set. The pre-flight step
at `.github/workflows/release-please.yaml:147` derives the public half of
`RELEASE_SIGNING_KEY` and fails the release unless it matches *some*
`internal/releasekeys/keys/*.pem`; the post-sign step at
`.github/workflows/release-please.yaml:224` re-checks the signature goreleaser
actually produced against the same set. The comment at
`.github/workflows/release-please.yaml:142` states the intent outright: "that
directory is a set for rotation and any one of them verifying is a release the
fleet can consume."

`SECURITY.md` already tells users to read it that way. Its Release Integrity
section, at `SECURITY.md:299`, says more than one key may be present while a key
is being rotated, that a release is valid if any one of them verifies it, and
that a reader verifying a download by hand should list the keys for the tag they
are verifying at
`https://github.com/pedrosousa13/lnpm/tree/$TAG/internal/releasekeys/keys` — at
that tag, "not from `main`, which may already have rotated". The hand-verification
recipe at `SECURITY.md:288` fetches `release.pem` by name, which is why
`internal/releasekeys/releasekeys.go:6` tells anyone touching the package to
keep that filename;
nothing in the code depends on it, only the documented URL does.

So rotating is: generate a key, commit its public half beside the existing one,
replace the `RELEASE_SIGNING_KEY` secret, ship a release, and delete the old PEM
once the fleet has moved. No code changes at any step. What this decision
declines is the calendar, not the capability.

## Nothing is deployed that trusts this key yet

Re-verified while writing this, on 2026-08-28, rather than carried over from
#492's claim:

- `gh release view -R pedrosousa13/lnpm v3.0.0` lists `checksums.txt` and twelve
  archives and packages. There is no `checksums.txt.sig`. `gh release list`
  shows v3.0.0, cut 2026-08-27, as the latest release.
- `git ls-tree v3.0.0 internal/releasekeys/keys/` prints nothing and exits 0,
  while `git ls-tree v3.0.0 --name-only internal/` lists the other packages — so
  the path is absent from the tag rather than the command being wrong.
- `git log --diff-filter=A -- internal/releasekeys/keys/release.pem` names one
  commit, `2446032`, which is #467, and `git tag --contains 2446032` prints
  nothing. Signing landed in #467 and #467 is unreleased.

That is the fact this decision is cheapest against: there is no installed base
trusting any key, because no shipped binary carries one and no published release
is signed. The first key to acquire an installed base will be this one, at the
first release after #467, and it will acquire it under this decision.

## The argument, stated rather than papered over

A cadence is a promise about future maintainer behaviour, and lnpm has one
maintainer. A schedule nobody is on call for is not a control; it is a note in a
document that goes stale in the direction nobody notices. The failure mode is
worse than doing nothing, because a cadence expressed as an expiry has teeth
that bite the wrong party: a key that expires on a date nobody rotated on breaks
`lnpm update` for every installed binary, which is precisely the outcome
`.github/workflows/release-please.yaml:129` calls the worst thing the pipeline
can do — "recoverable only by reinstalling by hand". An expired key provides no
security benefit while it does that. Nobody's compromise is undone by the fleet
refusing to update.

What actually protects anyone is that rotation is available the moment it is
wanted, and the case that wants it is compromise. That case does not arrive on a
schedule, and preparing for it means having a working mechanism and a documented
procedure — which the previous section establishes exists — not having exercised
the mechanism on unrelated dates beforehand.

The comparison with #472 is worth making explicit, because #492 was filed as its
unowned half and the two credentials do not behave alike. #472 was closed
`wontfix` as accepted risk on 2026-08-28. Its own body notes that the App key
"is also addressable by rotating the App key on a schedule, which does nothing
for the signing key" — a periodically rotated App key shrinks the window in
which a stolen copy is useful, because tokens are minted continuously from it.
The release signing key is used a handful of times a year, offline from the
attacker's point of view, and its stolen copy stays useful for exactly as long
as binaries embedding its public half are in circulation. Rotating it on a
timetable does not shorten that; only removing the old PEM and waiting out the
fleet does, and that is a deliberate act either way.

## What this gives up

Plainly, and without softening it:

**A key compromised without anyone noticing is trusted indefinitely.** Every
binary already shipped carries the public half it was built with, and
`verifySignature` accepts a signature from any embedded key with no notion of
when it was issued. There is no scheduled event that would retire the current
key, so a silent compromise persists until somebody observes it and acts —
detection is doing all the work, and lnpm has no detection for this.

**Retirement reaches only future builds.** Deleting a PEM from
`internal/releasekeys/keys/` changes what the *next* build trusts. Copies in the
field keep trusting what they were compiled with, forever, since nothing revokes
a key at run time and `lnpm update` consults no revocation list. So even the
deliberate rotation this document keeps available is slow at the tail, and the
tail is made of installs nobody updates.

**No key has an intended lifetime, so nothing is stale.** There is no date after
which the current key is due for replacement, and therefore no state in which
anyone can say the key is overdue. That is the direct cost of the ceremony being
declined.

**The compromise procedure is documented here and nowhere else.** `SECURITY.md`
describes rotation as a state a verifier may observe — more than one key may be
present — not as a runbook. Whoever rotates under pressure gets the four steps
in the section above and the pipeline's two guards, and nothing more rehearsed
than that.

## Consequences

No code changes and no configuration changes. `internal/releasekeys` keeps its
glob, `internal/cli/update.go` keeps accepting any embedded key, and the release
workflow keeps both of its guards. This document is the whole of the change.

`SECURITY.md` stays as it is. Its Release Integrity section already describes
the multi-key state and where to list the keys for a tag, which is what a
verifying user needs; it deliberately does not gain a cadence sentence, because
there is no cadence to state and a sentence saying so belongs in a decision
record rather than in a user-facing verification recipe.

#492 closes as `wontfix`, citing this document. It is not closed as
not-a-defect: the request was for a decision and a written record of one, and
this is that record. A future issue asking again for a rotation schedule should
be read against this document first, and specifically against the *What this
gives up* section, which already concedes everything such an issue is likely to
argue.

The key's own lifetime is unchanged and unbounded. The first signed release will
be signed by `release.pem` as it stands, and every binary from that release
onward trusts it until a build ships without it.

## Considered options

**Adopt a fixed rotation cadence — annually, or per major release.** The
conventional answer, and the one #492 anticipated. Rejected on the argument
above: a schedule on a single-maintainer project is a promise about attention
that nothing enforces, and the enforcement mechanisms available all fail toward
breaking updates rather than toward being rotated. Rotating on a cadence also
costs each time it is honoured — a second PEM committed, a secret replaced, and
a window in which two keys are trusted, which is strictly more trusted material
than one. Against that, the benefit is limited to shrinking the exposure window
of a compromise nobody has detected, and the window is bounded by the installed
fleet rather than by the schedule, so the shrinking is far smaller than the
cadence suggests. If the project ever grows a second maintainer with release
duties, this is the option to revisit first.

**Add an expiry field to the key format, so the code enforces the cadence.**
The version with teeth: carry a not-after date beside each PEM and have
`Trusted()` or `verifySignature` refuse an expired key. Rejected because the
teeth bite the fleet, not the attacker. A key that lapses because nobody rotated
on time turns every installed binary into one that refuses every release, and
the failure appears at `lnpm update` on the users' machines rather than in CI
where a maintainer would see it. It is also a format invention with no consumer:
SPKI PEM carries no validity period, so this means either X.509 certificates —
a chain, a trust anchor, and a set of questions this project has no reason to
answer — or a bespoke sidecar file that only lnpm understands. The mechanism
that already exists, deleting a PEM and letting the fleet roll forward, achieves
retirement without any of it.

**Record nothing, and close #492 as not-a-defect.** Rejected for the reason
ADR-0007 gives for the same option in its own last entry, which applies here in
a sharper form. There, the concern was that the next reader of `same hash = same
content` could not tell an accepted trade-off from an unnoticed one. Here the
ambiguity is already documented as real: #492 exists precisely because #472's
`wontfix` covered one of two bundled concerns and the second was silently
inherited. Closing the second the same way, with no record, reproduces the
condition that produced the issue — and does it on the credential with the
longest blast radius in the project, where a future reader finding no policy
would be right to file it a third time.

**Deferral expiry.** This decision rests on two facts, and it should be reopened
if either stops holding, in the way ADR-0007's framing deferral is written to
expire. First, one maintainer signs releases; a second person with release
duties makes the key shared custody, and shared custody is the case where a
departure is a rotation trigger with a date on it. Second, retirement is
maintainer-driven and fleet-paced, because nothing revokes a key at run time; if
`lnpm update` ever learns to fetch a key set or a revocation signal rather than
relying solely on what it was compiled with, retiring a key stops being slow at
the tail and the cost side of the cadence argument changes. A suspected
compromise reopens nothing — it triggers the rotation this document keeps
available, which is the outcome it is designed for.
