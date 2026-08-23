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

Two ways this has gone wrong here:

**A test that never exercised the code path.** An `add` regression test passed with the fix
removed, because it drove `runAddSingle`, which already returned the error. Only
`RunAddMultiple` reaches the loop that swallowed it. The test looked exactly like a working
one.

**A test held up by an unrelated barrier.** In `TestPackMainCannotDefeatDefaultExcludes`, the
`node_modules` row stays green even under the refactor the test exists to catch, because the
walk prunes that directory before any per-file check runs. It is worth keeping, but it is not
the guarantee the `.env` and `.npmrc` rows are — and the test says so, so three green rows are
not read as three equal guarantees.

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
- **B-prime** — hoist it above `isDefaultExcluded`. Tests that it cannot defeat the built-in
  force-exclude set.

They are different refactors and they catch different bugs. **Use B-prime for anything touching
pack selection**: under it, `main: ".env"` ships the secret, and a change that only ran B would
have looked fully covered.

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

## Squash bodies are the permanent record

This repo squash-merges. The default squash body concatenates the branch's commit messages, so a
claim corrected in a later commit comes back as the first thing in the permanent history.

**Always pass an explicit `--body` when merging.** A correction commit exists precisely because
the first message was wrong; letting the default win republishes it.
