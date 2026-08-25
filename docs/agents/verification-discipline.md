# Verification Discipline

How an agent working this repo establishes that a claim is true, and what counts as evidence.

Every rule here was earned by a specific failure — a wrong claim that reached a commit, or a
test that passed for the wrong reason. Each one names the failure, so it can be judged rather
than followed on faith.

## A comment is a reviewable assertion

Comments in this repo carry load. `internal/pack/pack.go` and `internal/db/db.go` explain *why*
a decision was made, name the failure it prevents, and record rejected alternatives — a reader
relies on them the way they rely on the code.

That makes a wrong comment worse than no comment. Seven were caught false in a single week of
work, each having survived the author's own review:

- *"git is case-sensitive"*, used to justify not folding case in ignore patterns. Git folds
  whenever `core.ignorecase` is set, which `git init` and `git clone` set on a case-insensitive
  filesystem — the default on macOS and Windows, the two platforms the comment was arguing
  about.
- *"`strings.ToLower` does not catch the Kelvin sign"*, offered as a Unicode limitation. It
  does: `U+212A` maps to `k`. Dotless `ı` was the real example.
- *"the rule #233 set"*, attributing gc's count-what-you-reclaimed rule. `git log -S` shows it
  came from #273; #233 only moved the code and decoupled the prompts.
- *"the Windows and macOS CI jobs"* earn a separator test's keep. macOS builds `path_unix.go`
  like Linux, so only Windows can distinguish the spellings.
- *"this row catches `strings.HasPrefix(relPath, pattern)`"*. The pattern was `changes*`, and
  `HasPrefix("changes/secret.txt", "changes*")` is false — the row caught nothing of the kind.
- *"with `--fix-links`, deletes it"*, blaming a flag for gc's destructiveness. The arithmetic
  that collects the package is unconditional; the flag gates only the reporting.
- *"doctor's reported count is unchanged — verified, not assumed"*. Verified for one damage
  shape. `encoding/json` populates fields decoded before an `UnmarshalTypeError`, so the other
  shape moves the count from 0 to 1.

**The rule: if a comment says "X happens", run X.** If it cites an issue as the source of a
rule, check with `git log -S`. If it describes a standard-library behaviour, read that
library's source or run it — every one of the failures above was a confident inference from
plausible reasoning.

State what you actually established. "Run and confirmed" is worth writing down; so is "read
from the source, not executed", which is the honest form when a claim covers a platform you
cannot run.

## A test must go red for the reason you think

A new test passing proves nothing on its own. Remove the fix and watch it fail, and read the
failure — not just that it failed.

Three ways this has gone wrong here:

**A test that never exercised the code path.** An `add` regression test passed with the fix
removed, because it drove `runAddSingle`, which already returned the error. Only
`RunAddMultiple` reaches the loop that swallowed it. The test looked exactly like a working
one.

**A test held up by an unrelated barrier.** In `TestPackMainCannotDefeatHardReserved`, the
`node_modules` row stays green even under the refactor the test exists to catch, because the
walk prunes that directory before any per-file check runs. It is worth keeping, but it is not
the guarantee the `.npmrc` row is — and the test says so, so two green rows are not read as two
equal guarantees. #321 found a third shape of this: in `TestPackWarnsWhenFilesNamesHardReserved`
the `.git` row stays green with the hard-reserved check disabled outright, because
`filterGitFiles` strips `.git` from the finished set independently. Disabling the check and
reading which rows moved is what established that — the first draft of the comment asserted a
different split from reading the code, and was wrong.

**A revert experiment that never built.** This one does not look like a broken experiment. It
looks like a finished one with a clean answer: you remove the fix, run the suite, see zero
failures, and conclude that nothing pins the behaviour. What actually happened is that the
package did not compile, so no test ran at all.

In #321, reverting an ancestor-directory check meant replacing `softExcluded.covers(relPath)`
with `isDefaultExcluded(relPath)` at both call sites. That left the `softExcluded` variable
declared and unused, which Go rejects:

```
# github.com/pedrosousa13/lnpm/internal/pack [github.com/pedrosousa13/lnpm/internal/pack.test]
internal/pack/pack.go:601:2: declared and not used: softExcluded
FAIL	github.com/pedrosousa13/lnpm/internal/pack [build failed]
FAIL
```

The filter in use was `grep -E "^    ---|^--- |         got|        want"`, which pulls result
and diff lines out of a verbose run. No line above matches any of those four alternatives, so the
command printed nothing whatsoever — and the obvious reading of no output is "every row passed".
The experiment was re-run with `_ = softExcluded` added to keep the build honest, and the real
answer was one failing row: the exact opposite of the conclusion the silent run invited.

Note what composes here, because both halves are recommended practice. Test output is filtered
rather than read whole for the reason the environment notes give: `PIPESTATUS` misbehaves in a
zsh subshell, so exit status is checked separately and the output is grepped for the interesting
lines. That filter is built to match test-failure lines, and a compiler error is not one.

**The check: confirm the experiment built before believing a green result.** A revert producing
zero failures is not evidence until you have seen the test binary actually run. Two ways to
settle it, both measured:

- **Look for the package's result line.** A run that built prints `ok <package>` or
  `FAIL <package>` with a duration; a run that did not prints
  `FAIL <package> [build failed]` and never `ok`. Keep that line out of whatever filter you
  apply, or read the run unfiltered once.
- **`go vet ./...` before the test run.** It type-checks test files as well as the package, so
  it catches breakage on either side. `go build ./...` is *not* sufficient — with a deliberate
  unused variable added to `internal/pack/pack_test.go`, `go build ./...` exited 0 while
  `go vet ./...` reported `declared and not used: x` and exited 1.

Do not accept "no output" as "no failures".

**The general shape.** Any revert that deletes a call can leave something else unused or
mistyped: an import with no remaining reference, a variable whose only use was the deleted line,
a helper whose signature no longer matches, a receiver on a type that is now unreferenced. The
smaller and more surgical the revert, the likelier this is — a one-line deletion is exactly the
kind that orphans its own dependencies.

**When a fixture cannot express the thing you are testing, say so rather than adjusting the
assertion until it passes.** `DeleteLink` swallows every per-item error inside its transaction,
so a *partial* delete failure cannot be constructed without a fake database. Three real tests
plus one honest gap beat four with one built on a seam invented to make it possible.

## Revert checks have a depth, in `internal/pack`

The standard two directions are: remove the fix, and move it somewhere subtly wrong. In the
pack selection path the second has **two distinct depths**, and only one tests the security
boundary:

- **B** — hoist the change above the whitelist branch. Tests that it does not change
  behaviour for packages with no `files` field.
- **B-prime** — hoist it above `isHardReserved`. Tests that it cannot defeat the
  never-publishable list.

They are different refactors and they catch different bugs. **Use B-prime for anything touching
pack selection**: under it, `main: ".npmrc"` ships the auth token, and a change that only ran B
would have looked fully covered.

Since #321 there are two built-in lists and they are enforced in different places, so B-prime
has several spellings and you may need more than one. `hardReservedExcludes` is checked first in
the walk, so hoisting a force-include above `isHardReserved` is the classic B-prime.

`defaultExcludes` is seeded into the ignore chain instead, and *any* whitelist arm that does not
consult the chain overrides it by default. **Count the arms before writing the experiment.** As
of #321 there are two — `mainEntry` and `isIncluded` — and each calls back into the list on its
own line, so there are two guard sites, not one:

- Delete the `mainEntry` arm's call and `main: ".env"` under a `files` whitelist ships the
  secret. Measured: all four rows of `TestPackMainCannotDefeatDefaultExcludes`, plus
  `TestPackWarnsWhenMainIsNotPacked/held_back_by_defaultExcludes` and two rows of
  `TestPackMainNamedByFilesFieldIsPacked`. Every row of
  `TestPackFilesEntryOverridesDefaultExcludesOnlyByDirectMatch` stays green.
- Delete the `isIncluded` arm's call and `"files": ["dist"]` ships `dist/.env`, `dist/app.log`
  and `dist/pkg.tgz`. Measured: **ten red subtests, and eleven failing tests**, because one of
  them has no subtests at all. The ten are nine rows of eighteen in
  `TestPackFilesEntryOverridesDefaultExcludesOnlyByDirectMatch` plus
  `TestPackNegationDoesNotOverrideInWhitelistMode/negation_alone_does_not_re-include`; the
  eleventh is `TestPackDoubleStarSweepsWithoutConsentingToDefaultExcludes`, which #350 added.
  Every `main` test stays green.
  `TestPackNegationDoesNotOverrideInWhitelistMode`'s other row, `naming_the_path_in_files_does`,
  stays green and could not do otherwise: its `want` already holds `dist/.env`, so a guard whose
  deletion only ever adds paths leaves the expected set untouched. #403's
  `TestPackFilesEntryTrailingSlashRunOnAFileOverridesDefaultExcludes` stays green on all five of
  its rows for that same reason, at this guard site and at `mainEntry`'s, and both were re-run on
  2026-08-25 rather than reasoned about. A new test whose entries name a default-excluded path
  directly is the shape that most looks like it belongs in this list and does not — which is worth
  a run either way, since the failure this bullet keeps recording is a list going short.

  The figure was eight of sixteen until #346 added two `./` rows and both experiments were
  re-run. `TestPackNegationDoesNotOverrideInWhitelistMode` was missing from this list before
  that re-run, and had not changed — the list was simply short. It was then entered here as
  "both rows", from a run whose output had been filtered to top-level test names: the whole
  test showed red and the row count was inferred rather than read. #350 then added a test to
  this experiment's blast radius and did not enter it here, so the list went short a third time
  and a reviewer had to catch it. Note what makes that easy to do: a subtest-free test never
  prints a `--- FAIL: Test.../sub` line, so any command grepping only for subtests reports it as
  absent. **Read both `--- FAIL:` shapes — the subtest lines and the top-level ones — or read
  the run unfiltered.** The classification paragraph below lost the same test the same way.

An earlier draft of this section said only `mainEntry` called back, and a reviewer following it
would have deleted one line, watched one test go red, and missed the second site entirely — which
is how the `dist/.env` leak reached a second review round. Re-derive the count from the code each
time rather than trusting this paragraph's number.

Each guard site also has inner halves worth reverting on their own. Both read
`softExcluded.covers(relPath) && !isIncludedDirectly(...)`; swapping `covers` back to
`isDefaultExcluded` drops the ancestor-directory question and turns exactly one row red, and
dropping `!isIncludedDirectly` turns a different single row red at the `mainEntry` arm. The
classification behind `isIncludedDirectly` has a third: treating every glob hit as
naming a path, rather than only those whose final glob segment constrains the name, turns nine
subtests red, over three tests:

```
TestMatchFilesFieldDotSlashAgreesWithUnprefixedForm/bare_double_star_reaching_a_nested_path
TestMatchFilesFieldDotSlashAgreesWithUnprefixedForm/bare_wildcard_segment
TestMatchFilesFieldGlobsWithDoublestar/bare_double_star_reaches_a_nested_path
TestMatchFilesFieldGlobsWithDoublestar/bare_double_star_reaches_the_root
TestMatchFilesFieldGlobsWithDoublestar/bare_single_star_stays_at_the_root
TestMatchFilesFieldGlobsWithDoublestar/single_star_reaches_one_level
TestPackFilesEntryOverridesDefaultExcludesOnlyByDirectMatch/bare_wildcard_at_the_root_is_containment
TestPackFilesEntryOverridesDefaultExcludesOnlyByDirectMatch/bare_wildcard_segment_is_containment
TestPackFilesEntryOverridesDefaultExcludesOnlyByDirectMatch/double_bare_wildcard_at_the_root_is_containment
```

Those nine are subtests. **A tenth test fails and has no subtests**, so it prints no
`--- FAIL: Test.../sub` line at all: `TestPackDoubleStarSweepsWithoutConsentingToDefaultExcludes`
packs `[.env dist/.env dist/a.js index.js package.json]` against a `want` of
`[dist/a.js index.js package.json]`. It is the failure that matters most here — the revert makes
`"files": ["**"]` ship both `.env` files — and it is exactly the one a subtest filter drops.

That is the mirror image of the bullet above. There, output filtered to *top-level* names hid a
subtest count. Here, output filtered to *subtest* lines hid a whole test. During #350's review
this test was predicted red by one reading and reported green by another; the run settles it as
red, and the disagreement existed only because neither `--- FAIL:` shape was read on its own
terms.

The figure was three until #350, which moved the `files` matcher onto doublestar and added rows
pinning the same classification. It went stale inside the very sentence #350 edited: **a number
in this file is a measurement with a date on it, not a fact.**

A single-line revert that moves one row is still a real revert — but only if you
checked that the rows you expected to stay green actually did.

### The walk's error branch, added by #348

#348 put a second decision into `collectFiles`' walk callback: a directory whose read failed is
skipped when the package's rules already exclude it, and aborts otherwise. Its two standard
directions are "delete the block" and "skip every unreadable directory rather than only excluded
ones", and both are worth running. Measured on 2026-08-24, each preceded by `go vet ./...` and
each read for the package result line rather than for silence:

- **Delete the whole skip block.** Three rows over two tests —
  `TestPackSkipsAnUnreadableExcludedDirectory` (both rows, `no_files_field` and `files_field`)
  and `TestPackSkipsAnUnreadableHardReservedDirectory`.
- **Skip every unreadable directory**, by discarding the predicate's verdict. All six rows of
  `TestPackAbortsOnAnUnreadableDirectoryThePackageWouldHavePacked`.

Two narrower ones matter more than either, because each isolates a term whose obvious reading is
wrong:

- **Swap `filesFieldMayReach` for `matchFilesField(relPath, filesField) == filesMatchNone`.**
  This is the predicate a reader reaches for first, and it is wrong: `matchFilesField` answers
  how an entry reaches a path, not whether one could reach *past* it, so
  `matchFilesField("coverage", ["coverage/report.html"])` is `filesMatchNone` for a directory the
  entry plainly selects into. Two rows red —
  `TestPackAbortsOnAnUnreadableDirectoryThePackageWouldHavePacked/a_files_entry_reaching_a_path_inside_it`
  and `.../a_files_entry_reaching_it_by_double_star`. Note what stays green: every skip row, and
  the four other abort rows. A run that only checked "does anything fail" would have called the
  wrong predicate covered.
- **Delete the `isHardReserved` term from `unreadableDirIsExcluded`.**
  `TestPackSkipsAnUnreadableHardReservedDirectory` alone. It is the only term that answers for an
  unreadable `node_modules` under a package with no `.gitignore` naming it, since `node_modules`
  is in `hardReservedExcludes` and not in `defaultExcludes`.

The callback's guards each move exactly one test, and one of them does not fail so much as
crash: deleting `info != nil` panics inside `TestPackAbortsWhenAChildCannotBeStatted`, because
`filepath.Walk` passes a nil `FileInfo` on both of its lstat-failure shapes. Deleting
`relPath != "."` turns `TestCollectFilesAbortsWhenThePackageRootCannotBeRead` red and nothing
else.

The third guard is the interesting one, because **its revert is green and that is the answer, not
a gap**. Deleting `info.IsDir()` moves nothing: every error site `filepath.Walk` has passes
either a directory's `info` or nil, so `info != nil` already implies a directory. The guard is
kept for a shape a later Go release might add, in the sense `mainEntryPath`'s comment uses, and
the comment says so. Do not read the green as missing coverage and do not delete the guard on the
strength of it — but do re-run it, because the day it starts moving a row is the day the
enumeration above went stale.

One more, outside the walk entirely: the "a file-level read error is unaffected" criterion is
pinned by `TestPackAbortsOnAnUnreadableFile`, and the revert that moves it is swallowing
`hashErr` in `collectFiles`' second pass. Nothing in the skip logic is on that path — the walk
lstats the file successfully and selects it, and `HashFile` is what fails — so a reviewer looking
for that criterion in the walk will not find it.

Two things about reading these runs. Four of the six tests named above have **no subtests**, so
they print only a top-level `--- FAIL:` line — the same shape that went uncounted twice in the
section above. And **B-prime does not apply to this fix at all**: the walk's error branch can
only skip or abort, never select, so there is no placement of it that ships a path. Do not write
a hoist-above-`isHardReserved` experiment here and read its green as coverage.

The general lesson is the one that generalises past `pack`: **when a fix moves a check from an
implicit position to an explicit one, the revert direction moves with it.** Reverting the old
placement no longer tests anything, and a reviewer reading only the ADR would write the wrong
experiment.

This was found because a subagent reported that a revert check did not catch what it had been
told it would, instead of reshaping the test to fit the claim. That is the behaviour to copy.

### The git metadata tier, moved by #398

#398 moved `.gitignore` and `.gitattributes` out of `defaultExcludes` and into
`hardReservedExcludes`, and added `.gitmodules`, which had been on neither list. What a
maintainer can pack barely moved: `filterGitFiles` already dropped all three from the finished
set, by basename, at every depth.
What moved is what lnpm *says* — a `files` entry naming one now warns instead of being refused
in silence. Measured on 2026-08-25 on Linux, each direction preceded by `go vet ./...` and each
read for the `ok`/`FAIL <package>` result line rather than for the absence of output.

- **Remove the fix**, by putting the three names back where #398 found them. **Twenty-one red
  subtests over five tests**, all inside `internal/pack`; every other package still prints `ok`.
  The five are `TestGitMetadataTierAgreesWithTheGitSafetyFilter` (eight rows),
  `TestIsExcludedHardReservedCannotBeNegated` (three),
  `TestPackWarnsWhenFilesNamesHardReserved` (four),
  `TestPackWarnsWhenIgnoreNegationNamesHardReserved` (three) and
  `TestPackFilesEntryNamingAnIgnoreFile` (three). Read the last one's failures rather than
  counting them: all three fail on the *warn* assertion and none on the packed set, because the
  file was refused either way and only the silence moved. That is the defect stated as a
  measurement.

- **Disable the walk's `isHardReserved` check outright.** This one is far wider than #398 —
  it removes the whole tier, not the three names — so read it for its split rather than its
  size: `TestPackWarnsWhenFilesNamesHardReserved` goes **ten red of fifteen**, and the five
  that stay green are `.git` and the four git-metadata rows, all held up by `filterGitFiles`.
  It is also the one direction here that reaches a second package: `internal/pack` and `tests`
  both print `FAIL`, ten failing tests between them, `TestPublishExcludesTheRetreatSnapshot`
  and `TestPublishKeepsMixedCaseSecretsOutOfTheStore` among them. The split was eight of
  thirteen until #402 added the `./node_modules/dep` row to that table, and nine of fourteen
  until #403 added `//node_modules/dep`; both land in the red group, for the same reason.
  Two of the other four bullets here also cite this test's row counts — **Remove the fix**, at
  four, and **B-prime, the classic spelling**, at six — and both were re-run for #402 and again
  for #403, and neither has moved. **That is three revisions of one number in two days**; treat
  it as a measurement to re-run, not a fact to cite.

- **B-prime, the classic spelling** — hoist a direct `files` name above `isHardReserved`, as
  `isHardReserved(relPath) && !(useWhitelist && isIncludedDirectly(relPath, filesField))`.
  **Fifteen rows over three tests**: `TestPackHardReservedWinsInWhitelistMode`, which **has no
  subtests** and so prints only the unindented `--- FAIL:` shape; eight rows of
  `TestPackNeverPublishesLockfiles`, being the direct-`files`-entry route for each of the four
  lockfiles at the root and nested; and six of `TestPackWarnsWhenFilesNamesHardReserved`.
  `"files": [".npmrc"]` is what ships the auth token under this hoist.

- **B-prime for `main`** — exempt the entry point instead, as
  `isHardReserved(relPath) && !(useWhitelist && mainEntry != "" && relPath == mainEntry)`. **Two
  of `TestPackMainCannotDefeatHardReserved`'s four rows**: `npmrc` packs
  `[.npmrc dist/a.js package.json]` and `lockfile` packs
  `[dist/a.js package-lock.json package.json]`. The other two stay green on second barriers —
  `node_modules` on the walk's `filepath.SkipDir`, `.gitmodules` on `filterGitFiles`.

- **Comment `filterGitFiles` out of `Pack` entirely.** Everything stays green, and that is the
  answer rather than a gap: `go vet ./...` exits 0, and `internal/pack` and `tests` each print
  `ok` with a duration, not `[build failed]`. The selection path refuses all three on its own
  now, so the filter is a second answer rather than the only one.

**B-prime is blind to these three names, and a green packed-set row is not coverage for them.**
Under both B-prime spellings above, not one git row moves anywhere — not in
`TestPackWarnsWhenFilesNamesHardReserved`, not in `TestPackFilesEntryNamingAnIgnoreFile`, not in
`TestPackMainCannotDefeatHardReserved`. The reason is mechanical: `filterGitFiles` runs over the
finished set in `Pack` and removes whatever the hoist selected, so *no* placement of the
hard-reserved check can be caught by what these three names pack. Do not write a
hoist-above-`isHardReserved` experiment for a git-metadata row and read its green as coverage.
The tier assignment for these names is answerable only through the warning half and through the
list-level tests, `TestGitMetadataTierAgreesWithTheGitSafetyFilter` and
`TestIsExcludedHardReservedCannotBeNegated`, neither of which the filter can reach.

That is the same shape as the note ending the #348 subsection above, reached from the opposite
side. There the fix could never select, so B-prime had nothing to hoist. Here the fix does
select, and a later pass erases the difference before any assertion sees it.

Rows expected green and confirmed green, since a revert that moves a row proves nothing until
you check what did not move. Under remove-the-fix and under both B-prime spellings, every
package outside `internal/pack` prints `ok`, so #398's own blast radius stops at `pack`; the
disable-the-whole-tier direction is the exception, and its bullet says so.
And the packed-set half of `TestPackFilesEntryNamingAnIgnoreFile` stays green on all four of its
rows under the remove-the-fix direction, `.npmignore` included — that is the row that must not
move at all, since it stays in `defaultExcludes`, no safety pass covers it, and
`"files": [".npmignore"]` publishes it.

One more, about enumerating exceptions in prose. `isHardReserved` and `isGitRelatedPath` are
required to agree, and they do not agree everywhere; the first draft of
`TestGitMetadataTierAgreesWithTheGitSafetyFilter`'s header named one exception; there are three
disagreeing paths, from two causes. Measured on 2026-08-25: `isHardReserved("src/.git/config")` is false against a true,
`isHardReserved(".gitignore/foo")` is **true** against a false, and `isHardReserved("src/.git")`
is true against a false. The middle one was missed because no row in the table sat under a
`.gitignore/` directory, so neither the test nor the comment ever reached
`matchesIgnorePattern`'s directory-prefix branch — and the header's own claim that a widened
pattern spelling "would fail here first" was the claim that should have caught it. All three are
rows now, each carrying the pair it was measured to produce, and the loop also asserts that one
of the two predicates still refuses the path, which is what makes a disagreement harmless rather
than a hole. **An exception that lives only in prose is the short-list failure the section above
records three times.** It happened a fourth time in the same change:
`TestPackMainCannotDefeatHardReserved`'s header went on naming `.npmrc` as the one load-bearing
row and `node_modules` as the one weak one after #398 added a third weak row beneath it.

### The glob subtree expansion, added by #406

#406 let a `files` entry whose last segment is a bare wildcard expand a directory it
matches into the whole subtree, through an ancestor-directory walk in `matchFilesField`'s
default branch. Measured on 2026-08-25 on Linux, each direction preceded by `go vet ./...`
— clean, exit 0 — and each read for the `ok`/`FAIL <package>` result line rather than for
silence. Every package outside `internal/pack` prints `ok` under both.

- **Remove the fix**, by disabling the `matchesAncestorDir` branch. **Eight red subtests
  over two tests, plus one subtest-free test — nine failures, three tests.** Five rows of
  `TestMatchFilesFieldGlobsWithDoublestar`, three of `TestPackGlobExpandsAMatchedDirectory`,
  and `TestPackGlobSweepDoesNotConsentToDefaultExcludes`, which **has no subtests** and so
  prints only the unindented `--- FAIL:` shape. The `filesMatchNone` rows stay green and
  could not do otherwise: the branch only ever widens, so a row asserting that an entry
  selects nothing cannot move under its removal. That covers
  `only_a_bare_last_segment_expands` and its nested twin,
  `the_expansion_does_not_reach_a_root_file`, and the two `d*`/`dist/c*` rows of
  `TestPackGlobExpandsAMatchedDirectory`.

- **Classify the swept path `filesMatchDirect` instead of `filesMatchContains`.** This is
  the direction that publishes secrets, and its result is the reason this subsection
  exists. **One packed-set test catches it**: `TestPackGlobSweepDoesNotConsentToDefaultExcludes`
  packs `[dist/.env dist/a.js dist/app.log dist/cli/.env dist/cli/b.js dist/cli/deep.log
  dist/pkg-1.0.tgz index.js package.json]`. #406's issue named
  `TestPackDoubleStarSweepsWithoutConsentingToDefaultExcludes` and
  `TestPackFilesEntryOverridesDefaultExcludesOnlyByDirectMatch` as the guards for this
  criterion, and **both stay green** — the first runs `"**"`, which doublestar matches
  against every path in its tree directly, and every tree in the second puts its
  default-excluded file where the entry already reaches it, so neither ever runs the new
  branch. The same five `TestMatchFilesFieldGlobsWithDoublestar` rows go red, but they
  assert a classification rather than a packed set, so they state the rule and do not prove
  a secret stays out. A criterion whose named guards were taken on trust would have been
  reported covered by two tests that never execute the line.

  The root `.env` stays out under this direction too, and that is a control rather than a
  hole: `"*"` matches a root path outright, so the pre-#406 bare-wildcard rule answers it
  and the new branch is never reached.

- **Disable the walk's `isHardReserved` check outright**, for
  `TestPackGlobSweepCannotReachHardReserved`. It packs
  `[index.js node_modules/.package-lock.json node_modules/dep/index.js package.json]`: the
  `node_modules` paths arrive and the `.git` ones still do not, `filterGitFiles` having
  stripped them. So that test's `node_modules` rows carry the walk's check and its `.git`
  rows carry nothing of their own — the split the `#398` subsection above records, reached
  again from a `files` entry rather than from `main`.

## A read made strict changes its callers

Making a function return a real error where it used to swallow one is only half a fix. Every
caller that ignored the error now gets a different value — usually `nil` where it used to get
something usable.

#329 landed a second commit entirely for this. Making `GetLinksForPackage` strict surfaced an
error into three callers that only printed a warning, so `lnpm add` would materialise files into
a project, record no link row, and print success — leaving gc free to delete the store entry the
project was importing. The fix for a fail-open bug had created a second fail-open bug.

**After making any read strict, enumerate its callers and check what each does with the new
error.** A caller that swallows it puts the original bug back somewhere else.

## Some evidence only CI can produce

Anything filesystem-shaped is not established by a local run. This repo's CI runs Linux, macOS
and Windows, and the two case-insensitive filesystems behave differently from the machine most
work happens on.

**Never write two filenames into one test directory that collide when lower-cased.** On macOS
and Windows they are the same file, so the second write lands on the first, and a test asserting
both exist is unsatisfiable there *regardless of whether the product is correct*. This broke CI
once: the fixture had `secret.txt` and `SECRET.TXT` side by side. Split such cases across two
package directories.

When a claim can only be checked on a platform you cannot run, say which half you ran and which
half you read.

### `internal/shellcmd`, added by #375

This package is the sharpest case of a split like that, so it is worth naming what each half of
its revert check can and cannot settle. Measured on 2026-08-24 on Linux, each run preceded by
`go vet ./...` and each read for the `ok`/`FAIL <package>` result line rather than for silence:

- **Revert the fix.** One red row: `TestQuoteForCmdQuotesForCmdExe/a_double_quote`, on
  `quoteForCmd("a\"b") = "\"a\\\"b\""`. Every other row stays green, including all four of
  `TestCommandRunsAQuotedPath` — because the `SysProcAttr.CmdLine` half of the fix is invisible
  to a Linux run. **That half's revert was never run, on any platform.** Run 32634796756 shows
  `TestPublishDryRunRunsPrePublishButNotPostPublish` failing on Windows alone with
  `pre_publish hook failed: command failed: exit status 1`, but that run *predates* the fix and
  is not a revert of it; concluding that reverting would reproduce it is reasoning, and is
  labelled here rather than counted as a measurement.

  What is measured is the forward direction, on Windows CI run **32767343693**:
  `TestCommandRunsAQuotedPath`'s `a_space`, `a_single_quote` and `an_ampersand` rows PASS there
  rather than skip, `a_double_quote` SKIPs by design, and
  `TestPublishDryRunRunsPrePublishButNotPostPublish` passes with `shellcmd.QuoteArg` restored.
  Confirming the rows *ran* and did not skip is the point — see "Confirm the run you are
  reading" below.
- **A plausible wrong fix: quote for cmd.exe on every platform**, by dropping QuoteArg's
  `runtime.GOOS` branch. Two red tests —
  `TestQuoteArgMatchesTheShellCommandStarts`, which **has no subtests** and so prints only the
  unindented `--- FAIL:` shape, and `TestCommandRunsAQuotedPath/a_double_quote`. Note which rows
  stay green: `a space`, `a single quote` and `an ampersand` all pass, because `sh` accepts a
  double-quoted string too. Only the row whose fixture name contains the character the two
  shells disagree about moves.
- **Is `TestCommandRunsAQuotedPath` vacuous?** No — but the two directions above move only one
  of its four rows between them, which is why a third experiment was needed. Reverting the fix
  leaves all four green on Linux, because the bug is Windows-only; the cmd-quoting-everywhere
  direction moves `a_double_quote` and nothing else. So neither says anything about
  `a_space`, `a_single_quote` or `an_ampersand` — the three rows Windows CI actually runs.
  Making `quoteForSh` return its argument unchanged turns **all four rows red**, each for its
  own reason: three cannot open the marker, and `an ampersand` exits 127 with
  `sh: 1: and: not found`. The test measures the quoting, not the existence of a shell.

The lesson that generalises: a build-tagged fix has a revert direction per tag, and the one you
can run locally may not be the one the bug lives in. Split the pure part out — `quoteForCmd` and
`quoteForSh` are deliberately free of any build tag, and `QuoteArg` branches at run time instead,
so both compile and are exercised on every platform. That is what turned "Windows only, read from
the docs" into "Windows only for one of the two halves" — and that remaining half is the one
run 32767343693 settled, forward rather than by revert.

## Confirm the run you are reading

Before treating CI as evidence, confirm a run exists for the exact SHA you pushed
(`gh run list --branch <b> --json headSha,databaseId`), and confirm the tests you care about
actually *ran* rather than being skipped — a skipped test and a passing one look identical in a
summary.

The local counterpart is "A revert experiment that never built" above: same family, different
mechanism. Both are a green-looking result produced by tests that did not execute.

## Squash bodies are the permanent record

This repo squash-merges. The default squash body concatenates the branch's commit messages, so a
claim corrected in a later commit comes back as the first thing in the permanent history.

**Always pass an explicit `--body` when merging.** A correction commit exists precisely because
the first message was wrong; letting the default win republishes it.
