# Releasing

A release is cut by release-please, built by GoReleaser, and has its notes synced from
`CHANGELOG.md` — three jobs in `.github/workflows/release-please.yaml`, all triggered by a push to
`main`.

One step needs a person: writing the upgrade notes. Everything after the merge is automatic.

Approving a held workflow run used to be a second one. It is not any more — see
[The App token that keeps checks running](#the-app-token-that-keeps-checks-running), which is also
where to look when checks stop reporting on a release pull request again.

## The flow

1. **Land work on `main`.** Merges are squashed, so a pull request's title becomes the commit
   subject release-please parses. `.github/workflows/pr-title.yaml` enforces the conventional form
   for exactly that reason — a non-conventional title is dropped from the changelog and from the
   version bump.

2. **release-please keeps one release pull request open**, on the `release-please--branches--main`
   branch, titled `chore(main): release X.Y.Z`. It rewrites that branch and that pull request body
   every time a commit lands on `main`.

3. **Write the upgrade notes into `CHANGELOG.md` on that branch**, as a commit of your own. They go
   under the version's `## [X.Y.Z](...)` heading and above the generated `### Features` and
   `### Bug Fixes` lists. 2.2.1 and 2.3.0 both used a blockquote opening with the question the
   reader would actually have — "Relocated your `node_modules`?", "Using a `files` field?" — and
   naming the config key that restores the previous behaviour.

   This is the part release-please cannot derive. A commit subject cannot say that a change moves
   the packed set of an existing package in both directions.

   **A commit landing on `main` before you merge discards these notes.** Step 2's rewrite takes the
   whole branch with it — the same rewrite that rules out editing the pull request body below. So
   write the notes when you are ready to merge, and if anything lands on `main` in between, check
   that your commit is still the branch's head before merging:

   ```bash
   git fetch origin
   git log --oneline -1 origin/release-please--branches--main
   ```

   If the head is release-please's `chore(main): release X.Y.Z` rather than your notes commit, the
   notes are gone; write them again. Nothing downstream will notice: the squash carries whatever
   `CHANGELOG.md` the branch holds at merge time, and step 5 publishes that. A missing note is
   exactly the failure this runbook exists to prevent.

4. **If `X.Y.Z` is a new major, update `SECURITY.md`'s supported-versions table on that same
   branch.** Do this from the version number rather than from waiting on a red tick. The
   `Check supported versions` job does now run unattended on this pull request, but the check is the
   backstop for forgetting, not the prompt. It fails deliberately, and only on this pull request.
   See the check below.

5. **Merge, squashed.** release-please tags `vX.Y.Z` and creates the GitHub release. The
   `goreleaser` job builds, signs and uploads the archives, then pushes the taps. The
   `sync-release-notes` job overwrites the release body with the version's `CHANGELOG.md` section,
   hand-written notes included. That last job is ordered after `goreleaser` but does not depend on
   it succeeding: the `goreleaser` job re-runs the test suite and the linter, and a flake there
   costs the archives, not the notes.

   **The signing key can fail the release, and it is worth knowing that before it does.** Four
   steps in the `goreleaser` job exist for it, all in `.github/workflows/release-please.yaml`.
   `Write release signing key` at `:103` writes the `RELEASE_SIGNING_KEY` repository secret to the
   runner, and refuses to publish an unsigned release if the secret is empty. `Check the signing
   key matches an embedded public key` at `:147` derives its public half and fails before anything
   is built unless it matches some `internal/releasekeys/keys/*.pem`. `Remove the release signing
   key` at `:211` deletes it, under `if: always()`. `Verify the release signature against the
   embedded public keys` at `:224` re-checks the `dist/checksums.txt.sig` GoReleaser actually
   produced against that same key set.

   **A `RELEASE_SIGNING_KEY` that matches no embedded public key stops the release, deliberately.**
   `lnpm update` verifies `checksums.txt.sig` against the keys compiled into the running binary, so
   a rotated or mistyped secret would otherwise publish a green release that every existing install
   then refuses to update from, recoverable only by reinstalling by hand. That is what
   [#297](https://github.com/pedrosousa13/lnpm/issues/297) cost. Rotating the key is ADR-0008. Line
   numbers read from the workflow at 4.1.0, not executed.

## Why the notes need a job to reach the release body

Measured on 2026-08-25 against v2.3.0, for [#410](https://github.com/pedrosousa13/lnpm/issues/410).
The short version: release-please never reads `CHANGELOG.md` at release time, so notes written into
that file cannot reach the body no matter when they are written.

- **The body comes from the merged pull request's body.** `Strategy.buildRelease` in
  release-please's `src/strategies/base.ts` calls `this.parsePullRequestBody(mergedPullRequest.body)`
  and takes the release notes from what that returns. Read from the source, not executed.

- **That body was computed from commit subjects when the pull request opened.** Release pull request
  [#394](https://github.com/pedrosousa13/lnpm/pull/394)'s body holds release-please's own preamble
  (`:robot: I have created a release *beep* *boop*`, then a `---`), the `## [2.3.0](...)` heading
  with the derived `### Features` and `### Bug Fixes` lists, and its footer (a `---`, then "This PR
  was generated with [Release Please]…"). What it does not hold is the 2.3.0 blockquote: `gh pr view
  394 --json body` has zero lines beginning with `>`, and so does `gh release view v2.3.0 --json
  body`. That absence is the finding; the preamble and footer are release-please's own furniture.

  The published body is the pull request's body *minus* that furniture rather than a copy of it —
  2486 bytes against 2717. Deleting the two preamble lines, the two footer lines and the blank lines
  that bracketed them leaves 2486 bytes that `cmp` reports byte-identical to the release body. That
  is what `PullRequestBody.parse` does in release-please's `src/util/pull-request-body.ts`: it
  splits on `---` and keeps the middle, and the two strings above are its `DEFAULT_HEADER` and
  `DEFAULT_FOOTER`.

- **Writing the notes earlier does not help, and this was tested rather than reasoned.** The notes
  were already on the branch before the merge: commit `25789a79`
  (`docs(changelog): hand-write the 2.3.0 upgrade notes`) is dated 17:13:16 and the pull
  request merged at 17:13:58, and the squash commit `b0bdcbe` carries the blockquote in
  `CHANGELOG.md`. The published body still does not. So the process was not merely mis-sequenced.

  That 17:13:16 is the *commit* timestamp, not a push time — git records no push time.
  `git log -1 --format=%cI 25789a79` gives `2026-08-24T19:13:16+02:00`, which is 17:13:16 UTC. The
  17:13:58 merge time is a real event time, from the GitHub API.

Two things that look like fixes and are not:

- **A configuration option.** The workflow passes `release-type: go` and no config file, so nothing
  here had been ruled out by inspection. The action's inputs are `token`, `release-type`, `path`,
  `target-branch`, `config-file`, `manifest-file`, `repo-url`, `github-api-url`,
  `github-graphql-url`, `fork`, `include-component-in-tag`, `proxy-server`, `skip-github-release`,
  `skip-github-pull-request`, `skip-labeling`, `changelog-host`, `versioning-strategy` and
  `release-as` — all eighteen of them, read from `action.yml` on the `v5` tag, which is what the
  workflow pins. The same eighteen are on `v4`, so the conclusion below did not turn on the tag.

  A config file could be passed even though none is, so its keys were read from release-please's
  `schemas/config.json` on `main` rather than from memory. It declares 46 properties. Nine of them
  touch the body or the changelog — `draft`, `prerelease`, `skip-changelog`, `changelog-path`,
  `changelog-sections`, `changelog-host`, `changelog-type`, `pull-request-header` and
  `pull-request-footer` — and that nine is a filter applied here, not a category the schema names,
  so re-read the schema rather than trusting the count. None of the nine makes the body derive from
  the changelog file.

  `changelog-type` is the one that looks like it should, because it selects the notes builder.
  `src/factories/changelog-notes-factory.ts` registers exactly two: `default`, which templates the
  parsed conventional commits, and `github`, whose `buildNotes` in `src/changelog-notes/github.ts`
  calls GitHub's generate-release-notes API and prefixes a `## <version> (<date>)` heading. Neither
  opens `CHANGELOG.md`, so setting it to `github` would change where the *generated* text comes from
  and still lose the hand-written note. Read from the source, not executed.

  `pull-request-header` and `pull-request-footer` only rename the furniture measured above: they
  override `DEFAULT_HEADER` and `DEFAULT_FOOTER` in `src/util/pull-request-body.ts`, which are
  exactly the two strings the release body is the pull request body minus.

  Read the action's README with care here: it describes its own `body` output as "Release notes for
  the current version extracted from the CHANGELOG.md". The measurement above says otherwise, and
  the source agrees with the measurement.

- **Editing the release pull request's body instead.** It would work, and it would not last:
  release-please rewrites that branch and that body whenever another commit lands on `main`.
  [googleapis/release-please#877](https://github.com/googleapis/release-please/issues/877), open
  since 2021 and labelled `needs design`, is that exact complaint.

GoReleaser is not the culprit and never was. Its v2 `release.mode` defaults to `keep-existing` —
"keep the existing notes" — and `.goreleaser.yaml` sets no `mode`. `sync-release-notes` still runs
*after* the goreleaser job rather than beside it, so that stays a preference rather than something
the release body depends on.

That ordering is not a gate, though, and the `if` has to say so. `needs` names `goreleaser`, so
GitHub would otherwise skip the sync when goreleaser fails — "If a job fails or is skipped, all jobs
that need it are skipped unless the jobs use a conditional expression that causes the job to
continue" — and a bare `needs.release-please.outputs.release_created` is not such an expression,
because "A default status check of `success()` is applied unless you include one of these
functions": `success`, `always`, `cancelled`, `failure`. The job's `if` therefore starts with
`!cancelled()`. Without it, one flaky `go test -race` in the goreleaser job would leave the version
released, tagged and note-less, with `sync-release-notes` *skipped* rather than failed — #410
recurring inside the fix for #410, on a run that still looks green.

## The App token that keeps checks running

The `release-please` job mints a GitHub App installation token and passes it to
`googleapis/release-please-action` as `token:`. That is the only reason checks report on the release
pull request without anyone clicking anything, and it is the part of this pipeline most likely to
break quietly. [#457](https://github.com/pedrosousa13/lnpm/issues/457) is the history.

### What it replaced

Without a `token:`, the action defaults that input to `${{ github.token }}` — its `action.yml` says
so — and the release pull request is then opened by `github-actions[bot]`. GitHub does not start
workflow runs from events raised by `GITHUB_TOKEN`, so that a workflow cannot retrigger itself
forever. The run is created and parked, having executed nothing, so the required
`Conventional Commit` check does not report "pending": it does not report at all.

Measured on 2026-08-25, before the fix:

- Open release pull request [#412](https://github.com/pedrosousa13/lnpm/pull/412) had
  `statusCheckRollup: []` and `mergeStateStatus: BLOCKED`. That empty rollup is the "no checks
  reported" the merge box shows.
- Its run `32816558361` had `conclusion: action_required`, `triggering_actor: github-actions[bot]`,
  and `created_at`, `run_started_at` and `updated_at` all equal to `2026-08-25T06:20:43Z` — created
  and finished in the same second, having run nothing.
- The equivalent run for 2.3.0, `32755038068`, was created at `17:09:59` and has
  `run_started_at: 17:11:36` with `triggering_actor: pedrosousa13` and `conclusion: success`. The
  ninety-seven second gap is a human clicking **Approve workflows to run**. Approving is what
  started the run.

Each parked run needs its own click, and by 2026-08-27 a single pushed commit parked two of them
rather than one, because `Security Versions` had been added alongside `PR Title`. Runs
`33071277230` (`PR Title`) and `33071277248` (`Security Versions`) were both created at
`2026-08-27T12:18:55Z` on the same SHA `1af4a2b`, both `action_required`.

The cost scales with commits, not with pull requests: three separate SHAs on 2026-08-26 parked one
run each (`32963252535`, `32970558695`, `32972580212`), all `PR Title`. A release pull request that
takes several commits therefore accumulates clicks, and the release stalls until every one of them
is made.

A repository setting cannot fix this. The fork-approval policy accepts only
`first_time_contributors_new_to_github`, `first_time_contributors` and `all_external_contributors`,
none of which turns the guard off, and it governs fork pull requests in any case — the release pull
request has `isCrossRepository: false`.

An App installation token is a distinct identity, so the guard does not apply to events raised with
it. The App was chosen over a personal access token because it does not expire and is not tied to
one person's account.

### What it needs

Two repository secrets, both set on 2026-08-27:

- `RELEASE_APP_ID` — the App's numeric App ID. The action's newer input is `client-id`; the workflow
  passes the value as `app-id`, which the action still reads as a fallback, because the secret holds
  the App ID and switching inputs would mean reissuing it.
- `RELEASE_APP_PRIVATE_KEY` — a PEM private key generated for that App.

The App must be **installed on this repository**, with these repository permissions and no others:

| Permission      | Level        | Why                                                            |
| --------------- | ------------ | -------------------------------------------------------------- |
| `Contents`      | read + write | Push the release branch; create the tag and the GitHub release |
| `Pull requests` | read + write | Open, update and label the release pull request                |

`Metadata: read` comes with every installation and is not something you grant. Nothing else is
needed: the token is used by one action, in one job, and that job does not check out the repository,
touch workflow files, or read Actions state.

The token is minted per run and revoked when the job ends. It is not stored anywhere.

**Every check reports on a release pull request now, `CI` included.** `ci.yaml` used to carry
`paths-ignore: '**.md'` on `pull_request`, and a release pull request whose only change is
`CHANGELOG.md` started no `CI` run at all. Not a parked one, none.
[#500](https://github.com/pedrosousa13/lnpm/pull/500) removed that filter, because `CI`'s five jobs
are required checks and a required check that never reports blocks a pull request forever rather
than passing it. The header comment in `ci.yaml` records the release pull request that proved it,
#499, which sat blocked with two of seven checks green and no way to get the other five.

So an absent `CI` on a markdown-only release pull request is now a symptom rather than the path
filter doing its job. What the App token restores is the checks that would otherwise sit parked
awaiting approval, and that is all of them. An absent or parked `PR Title` is the credential having
gone stale. So is an absent or parked `CI`.

### What breaks when it goes stale, and what that looks like

This is the part worth knowing before it happens, because **the failure looks exactly like the bug
the token fixes**: no checks on the release pull request, or no release pull request at all. The
merge box says the same thing either way.

Two shapes, and they fail differently:

- **Loud, on `main`.** If `RELEASE_APP_ID` or `RELEASE_APP_PRIVATE_KEY` is missing or empty, the
  mint step fails with a message naming the input. If the key has been rotated in the App's settings
  and the secret still holds the old one, or the App has been uninstalled from this repository, the
  token request is rejected and the step fails too. In all of these the `release-please` job goes
  red on a push to `main`.

  Red, but easy to miss. Nobody watches a green `main`, and the *visible* consequence is on the pull
  request side: no release pull request is opened, or an existing one silently stops being updated
  as commits land. **If `main` has moved and the release pull request has not, look at the last
  `Release Please` run before looking at anything else.**

- **Quiet, on the pull request.** If the App is still installed and its key is still valid but its
  permissions have been narrowed — `Pull requests` dropped to read, say — the mint step succeeds and
  hands release-please a token that cannot do the work. The failure then surfaces inside the
  release-please step as an API error, or as a pull request that is opened but not updated.

  A permission added to an App after installation is **not** granted until the installation is
  reviewed and accepted. Changing the App's permissions is therefore not enough on its own.

Neither shape is caught by anything automatic. There is no check that the App is installed, no
expiry to watch — an App installation token is minted fresh each run and has no long-lived
credential to lapse — and no alert when a release pull request stops receiving commits. The
diagnosis is the run log of the `release-please` job.

If the token cannot be restored quickly, the release is not blocked: remove the `token:` line and
the mint step, and the pipeline reverts to the behaviour above — a release pull request that works,
at the cost of one approval click per parked run. Three workflows trigger on `pull_request` today,
`ci.yaml`, `pr-title.yaml` and `security-versions.yaml`, so that is three per commit pushed to the
release branch rather than the two it was before #500. Counted from the workflow triggers, not from
a parked run observed since the filter came off. That is the fallback, not the fix.

## The taps

`.goreleaser.yaml` publishes more than the archives. `checksums.txt` and its detached
`checksums.txt.sig`, deb, rpm and apk packages from the `nfpms` block, a Homebrew cask to
`pedrosousa13/homebrew-tap`, and a Scoop manifest to `pedrosousa13/scoop-bucket`. The cask and the
manifest are committed straight to the default branch of each tap on every release. The rest are
uploaded as release assets.

**4.1.0 is the first lnpm release that published to a tap, and it worked.** Run
[33638186070](https://github.com/pedrosousa13/lnpm/actions/runs/33638186070) on sha `5690e32`, with
`release-please`, `goreleaser` and `sync-release-notes` all green and `goreleaser` taking 2m50s.
`pedrosousa13/homebrew-tap` now holds `Casks/lnpm.rb` at `version "4.1.0"` and
`pedrosousa13/scoop-bucket` holds `lnpm.json` at `"version": "4.1.0"`. Both read back from the
GitHub contents API after the run.

What that run does not cover is a failure. Nothing below about a stale tap credential has been seen
happen, so those parts stay reasoned from GoReleaser's and GitHub's documentation, from
`goreleaser check`, and from reading the two tap repositories. Each is labelled where it appears.

Both taps are shared with `onda`, the maintainer's other project. `homebrew-tap` already holds a
`Casks/onda.rb` written by GoReleaser's `homebrew_casks`, and `scoop-bucket` already holds
`onda.json` at its root. Two consequences. A token scoped to these two repositories can also write
onda's cask and manifest, which is what sharing a tap costs in least privilege. And the install line
is `brew install pedrosousa13/tap/lnpm`, where the `tap` segment comes from the repository name
`homebrew-tap` rather than from the project name.

### Why `homebrew_casks` and not `brews`

`brews` is hard-deprecated. On goreleaser v2.18.0, the version the workflow's `~> v2` line resolves
to today, `goreleaser check` on a config using it prints
`DEPRECATED: brews should not be used anymore`, then `configuration is valid, but uses deprecated
properties`, and exits non-zero. The release job would fail on that rather than warn, so `brews` is
not an option. `homebrew_casks` is also what wrote the `onda` cask already in the tap, so the schema
is in production use in this org.

The cask has **no `test do` block**, and cannot have one. The Homebrew Cask DSL has no test stanza
at all; only formulae do. The goreleaser block that accepted a `test` key was the deprecated `brews`.
What covers that gap is thin and worth naming honestly. `brew audit` is what a tap runs against a
cask, and it checks the cask's shape rather than the program. Nothing in this pipeline executes the
published binary.

### What the taps need

A second installation token, minted by the `Mint a GitHub App installation token for the taps` step
in the `goreleaser` job and handed to GoReleaser as `TAP_GITHUB_TOKEN`. It exists because neither
credential can do the other's job. `secrets.GITHUB_TOKEN` is scoped to this repository alone and
cannot push to another one, so it cannot write a tap. The App token is scoped to `homebrew-tap` and
`scoop-bucket` only, so it cannot upload this repository's release assets. That is why there are two
tokens rather than one broader credential.

It reuses `RELEASE_APP_ID` and `RELEASE_APP_PRIVATE_KEY`, so the same App must be **installed on
both tap repositories**, with these repository permissions and no others:

| Permission | Level        | Why                                                    |
| ---------- | ------------ | ------------------------------------------------------ |
| `Contents` | read + write | Commit the cask and the manifest to the default branch |

`Metadata: read` comes with every installation and is not something you grant. No `Pull requests`
permission is needed, because GoReleaser commits to the default branch directly rather than opening
a pull request. The step narrows the token further with `permission-contents: write`, which can only
subtract from what the installation already has.

A permission added to an App after installation is not granted until the installation is reviewed
and accepted, exactly as described in
[What breaks when it goes stale](#what-breaks-when-it-goes-stale-and-what-that-looks-like) above.
Installing the App on a tap repository is subject to the same review.

### What breaks when the tap credential goes stale

The tap push happens **after** the release is published. GoReleaser's publish pipeline runs
`release.Pipe` before `cask.Pipe` and `scoop.Pipe`, and says why in a comment on the slice itself in
`internal/pipe/publish/publish.go`: "brew et al use the release URL, so, they should be last". Read
from the v2.18.0 source, not executed. So the tag exists, the GitHub release is published, and the
archives are uploaded before the tap is touched. A stale credential produces a release that is
entirely fine to download and a `brew install` that keeps serving the previous version. The
`goreleaser` job goes red, but the release itself is complete.

Recovery is **re-running the `goreleaser` job**, not re-cutting the release. Fix the App
installation or the secret first, then re-run. Nothing about the published release needs to change.

Two ways it goes stale, both of them quiet from the tap side:

- **The App is not installed on a tap repository**, or was removed from it. GitHub refuses to scope
  an installation token to a repository the installation does not cover, so the mint step fails.
  This is the loud one, and it fails before GoReleaser runs at all. Do not expect the error to say
  which of the two repositories is at fault; that was not checked against a real failure.
- **The App is installed but `Contents` has been narrowed to read.** The mint step succeeds and hands
  GoReleaser a token that cannot commit. The failure then surfaces inside the GoReleaser step as a
  push rejection, after the release is already published.

Nothing checks this automatically. The diagnosis is the `goreleaser` job's log, and the symptom a
user sees is `brew install` handing them an old version with no error anywhere.

## The extractor

`scripts/changelog-section.sh` prints one version's `CHANGELOG.md` section: the `## ` heading line
through to just before the next `## ` heading, trailing blank lines trimmed. It accepts `2.3.0` or
the tag name `v2.3.0`.

```bash
./scripts/changelog-section.sh v2.3.0
```

It exits non-zero with a message rather than printing nothing when the version has no section,
because the workflow feeds its output to `gh release edit --notes-file` and an empty notes file
blanks the body of an already published release. The workflow checks the exit status *and* that the
file is non-empty before calling `gh`, which is why it writes a file rather than piping.

Two more shapes fail the same way, because a *wrong* body overwrites a published release just as an
empty one does, and neither is caught by a non-empty check:

- **A heading with nothing under it.** One line, and the release body would be a bare heading.
- **A section whose text opens a code fence the file never closes.** CommonMark ends such a block
  only at the end of the document, so the section would carry every later version's notes.

A closed fence is fine, and headings inside one are not section boundaries — step 3's notes are
hand-written prose, and a fenced markdown example holding a `## ` line is an ordinary thing to write
there. Fences are matched as CommonMark defines them: backticks or tildes, three or more, closed by
the same character at a length at least as great and carrying nothing but whitespace after it.

**Do not indent a fenced example by four spaces or a tab.** It is the one shape known to hand `gh` a
short body without failing. CommonMark reads four columns of indent as an indented code block rather
than a fence, so an unindented `## ` line inside it *is* a section boundary: the section stops there
and the extractor exits 0, leaving nothing for the workflow's two checks to catch. Measured against
a fixture, the body came out as the heading, the prose and the opening fence line, and nothing else.
Indent by three spaces or by none. `scripts/test-changelog-section.sh` pins the behaviour — "four
spaces of indent is not a fence" and "a tab of indent is not a fence either" — so it cannot drift
unnoticed. It is not guarded against, because a guard would have to refuse indented code blocks, and
those are legitimate markdown: a fence nested inside a list item is indented past four columns as a
matter of course. Both halves were run rather than reasoned — markdown-it 3.0.0 in commonmark mode
parses the four-space `` ```markdown `` line as a code block and the column-zero `## ` after it as a
heading, and parses a `` ``` `` indented four spaces inside a `1.  ` list item as a fence.

Matching is on the version token alone. Every heading release-please writes carries a compare URL
ending in the *previous* version's tag, so a line-wide search for `2.2.1` finds 2.3.0's heading
first — and a prefix comparison finds `2.2.10`. `scripts/test-changelog-section.sh` builds a fixture
changelog where both of those wrong answers sit above the right one:

```bash
make test-changelog-section
```

CI runs it too, in `.github/workflows/ci.yaml`, since no Go test covers a shell script.

## The supported-versions check

`SECURITY.md`'s supported-versions table is hand-maintained, a decision recorded in the file itself:
no release-please substitution token can express a previous major moving from supported to
unsupported, so annotating the table would leave it wrong rather than merely stale. What that leaves
is the ordinary failure of anything manual — at some major release someone forgets, and a security
document goes on claiming support for a line that is no longer supported.

`scripts/check-security-versions.sh` compares the major named by the table's `:white_check_mark:`
row against the version in `CHANGELOG.md`'s topmost `## ` heading, which is the version
release-please maintains in-repo. Git tags are not used: a CI checkout is frequently shallow and may
carry no tags, and a check that passed vacuously in CI would be no check at all.

```bash
make check-security-versions        # the check itself
make test-check-security-versions   # its own tests
```

**On an ordinary pull request this passes and says nothing.** The top heading is the last released
version and the table already matches it, so this is not a per-PR chore. On the release pull request
the heading is the *new* version, so a patch or a minor still passes and **a new major fails** —
which is the entire feature. The failure is the reminder, and it lands on the one pull request where
the fix is a one-line edit to the table. Step 4 above is that edit.

The unsupported rows are not checked. Whether a major moves from supported to unsupported is a
policy judgement about how long you intend to support it, and it stays with the maintainer.

This runs in `.github/workflows/security-versions.yaml` rather than in `ci.yaml`. The separation was
load-bearing when `ci.yaml` carried `paths-ignore` with `'**.md'` on both its triggers. A pull
request touching only `SECURITY.md` or only `CHANGELOG.md` started no CI run then, and that is
exactly the shape of the release pull request this check exists for. #500 removed that filter from
`pull_request`, so the check would now run either way. What the separation still buys is a stale
table failing under its own check name rather than as one job inside a red `CI`. Neither workflow
carries `paths-ignore` on `pull_request` today. `ci.yaml` keeps its filter on `push`, where nothing
is gated on the result.

## Backfilling a published release

Only when a release was published before the sync job existed, or when a note is corrected after the
fact. Edit the body and nothing else — never the tag, never the assets.

```bash
./scripts/changelog-section.sh v2.3.0 > /tmp/notes.md
# read /tmp/notes.md before running the next line
gh release edit v2.3.0 --notes-file /tmp/notes.md
```
