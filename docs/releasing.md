# Releasing

A release is cut by release-please, built by GoReleaser, and has its notes synced from
`CHANGELOG.md` — three jobs in `.github/workflows/release-please.yaml`, all triggered by a push to
`main`.

Two steps need a person: writing the upgrade notes, and approving the held workflow run. Everything
after the merge is automatic.

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

4. **Approve the held workflow run**, so the required check can report. See the gate below.

5. **Merge, squashed.** release-please tags `vX.Y.Z` and creates the GitHub release; the
   `goreleaser` job builds and uploads the archives; the `sync-release-notes` job then overwrites
   the release body with the version's `CHANGELOG.md` section, hand-written notes included. That
   last job is ordered after `goreleaser` but does not depend on it succeeding: the `goreleaser`
   job re-runs the test suite and the linter, and a flake there costs the archives, not the notes.

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
  `release-as` — all eighteen of them, read from `action.yml` on the `v4` tag.

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

## The bot-authored-PR gate

The release pull request is authored by `github-actions[bot]`, and GitHub holds workflow runs
triggered by it behind a manual approval. The run is created and finishes immediately without
executing anything, so the required `Conventional Commit` check does not report "pending" — it does
not report at all.

Measured on 2026-08-25:

- Open release pull request [#412](https://github.com/pedrosousa13/lnpm/pull/412) has
  `statusCheckRollup: []` and `mergeStateStatus: BLOCKED`. That empty rollup is the "no checks
  reported" the merge box shows.
- Its run `32816558361` has `conclusion: action_required`, `triggering_actor: github-actions[bot]`,
  and `created_at`, `run_started_at` and `updated_at` all equal to `2026-08-25T06:20:43Z` — created
  and finished in the same second, having run nothing.
- The equivalent run for 2.3.0, `32755038068`, was created at `17:09:59` and has
  `run_started_at: 17:11:36` with `triggering_actor: pedrosousa13` and `conclusion: success`. The
  ninety-seven second gap is the approval. Approving is what starts the run.

**What to click:** the pull request carries a banner in the merge box, and **Approve workflows to
run** starts the held run. GitHub documents that for exactly this case — a pull request created by
a workflow using `GITHUB_TOKEN`, which is what this workflow does: it passes no `token` to
`googleapis/release-please-action@v4`, whose `action.yml` defaults that input to
`${{ github.token }}`. From
[Triggering a workflow](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow):
"the resulting `pull_request` event creates workflow runs in an **approval-required** state. The
pull request displays a banner in the merge box, and a user with write access to the repository can
start the runs by selecting **Approve workflows to run**".

The route to that button is *not* documented for this case. GitHub's
[Approving workflow runs from forks](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/approve-runs-from-forks)
says to "click the button in the upper right corner labeled **Awaiting approval**, which will open
the **Merge status** panel", then "Find and click **Approve workflows to run**" — but that page
opens "Workflow runs triggered by a contributor's pull request from a fork may require manual
approval", and a release pull request is not from a fork. Treat the *Awaiting approval* label and
the panel name as unverified here; only the button they lead to is sourced.

In practice step 3 often does this for you. A push of your own to the release branch triggers a run
whose actor is you rather than the bot, and that one is not gated: run `32755370188`, for the notes
commit `25789a79`, has `triggering_actor: pedrosousa13` with `created_at` equal to `run_started_at`
and concluded `success`. The check is recorded per commit SHA, though, so it is the newest commit's
run that has to report — approve if you merge without pushing anything.

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

## Backfilling a published release

Only when a release was published before the sync job existed, or when a note is corrected after the
fact. Edit the body and nothing else — never the tag, never the assets.

```bash
./scripts/changelog-section.sh v2.3.0 > /tmp/notes.md
# read /tmp/notes.md before running the next line
gh release edit v2.3.0 --notes-file /tmp/notes.md
```
