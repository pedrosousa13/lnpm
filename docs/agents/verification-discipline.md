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
  whenever `core.ignorecase` is set, which it sets by default on macOS and Windows — the two
  platforms the comment was arguing about.
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
  and `dist/pkg.tgz`. Measured: eight rows of sixteen in
  `TestPackFilesEntryOverridesDefaultExcludesOnlyByDirectMatch`, and every `main` test stays
  green.

An earlier draft of this section said only `mainEntry` called back, and a reviewer following it
would have deleted one line, watched one test go red, and missed the second site entirely — which
is how the `dist/.env` leak reached a second review round. Re-derive the count from the code each
time rather than trusting this paragraph's number.

Each guard site also has inner halves worth reverting on their own. Both read
`softExcluded.covers(relPath) && !isIncludedDirectly(...)`; swapping `covers` back to
`isDefaultExcluded` drops the ancestor-directory question and turns exactly one row red, and
dropping `!isIncludedDirectly` turns a different single row red at the `mainEntry` arm. The
classification behind `isIncludedDirectly` has a third: treating every `filepath.Match` hit as
naming a path, rather than only those whose final glob segment constrains the name, turns exactly
three rows red. A single-line revert that moves one row is still a real revert — but only if you
checked that the rows you expected to stay green actually did.

The general lesson is the one that generalises past `pack`: **when a fix moves a check from an
implicit position to an explicit one, the revert direction moves with it.** Reverting the old
placement no longer tests anything, and a reviewer reading only the ADR would write the wrong
experiment.

This was found because a subagent reported that a revert check did not catch what it had been
told it would, instead of reshaping the test to fit the claim. That is the behaviour to copy.

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
