# Ignore patterns glob with doublestar, and take its whole syntax with them

`matchesIgnorePattern` globbed with `filepath.Match`, whose `*` never crosses a
path separator, and `**` was special-cased only as a trailing `/**`. So
`**/*.pem` — the standard idiom for keeping keys out of a tarball — matched
paths of exactly two segments: `keys/deploy.pem` was excluded while `root.pem`
and `src/keys/deploy.pem` were both published, by a pattern that looked like it
worked (#316). Making `**` mean zero or more path segments means globbing with
something else, and the repo already depends on doublestar for workspace
globbing.

That swap does not import one construct. It imports doublestar's dialect, which
is minimatch's, which is what npm's own ignore handling uses — and lnpm's
filtering is modeled on npm's conventions, so most of the difference is a move
toward the thing we are imitating. We accept the dialect rather than trying to
hold the syntax still, because the parts that widen cannot be separated from the
part that fixes the bug.

The honest half is that one direction fails open. An `.npmignore` line
`weird{a,b}.txt` used to exclude a file of that literal name wherever it sat in
the tree; it is now brace alternation matching `weirda.txt` and `weirdb.txt`, so
`src/weird{a,b}.txt` is no longer excluded and ships. A publish doing more than
the maintainer asked is the direction `docs/adr/0001` calls a bug. This is not a
swallowed error, so that ADR does not literally govern it, but the decision runs
against its spirit and a reader deserves to hear that here rather than find it
in a tarball.

What keeps it narrow is that globbing is not the only route through
`matchesIgnorePattern`. Every branch that compares strings still reads the
braces literally: a pattern spelling out the full path — `/src/weird{a,b}.txt`,
or `weird{a,b}.txt` for a file at the package root — matches on string equality
before anything globs, and a literal-brace directory is still excluded by naming
it (`weird{a,b}`), by the trailing-slash form (`weird{a,b}/`) or by
`weird{a,b}/**`, none of which reach the glob engine either. What is lost is the
globbing routes specifically: matching by basename, and matching a full path
that carries metacharacters.

Braces are the visible half of the widening. Character classes are the other:
`filepath.Match` negates a class with `^`, so `[!a]` was the class containing
`!` and `a`, while doublestar accepts `!` and `^` alike and reads it as "not
`a`". `a.txt` therefore stops being excluded and ships, and `b.txt` starts being
excluded. Unlike braces this one cannot be suppressed while using doublestar at
all — there is no spelling of the negation that means one thing to
`filepath.Match` and nothing to doublestar — which is what makes "keep the
syntax identical apart from `**`" unachievable with this library rather than
merely inconvenient.

doublestar is also stricter about malformed patterns. An unbalanced `{` is a
hard error where `filepath.Match` read the brace literally, and both call sites
discard the error, so the glob branch returns false: `src/{tmpl.txt` used to
match a file of that name and now only the exact-full-path branch catches it.

None of this reaches the built-in exclusion lists. No entry in either list
(`hardReservedExcludes` or `defaultExcludes`, one list at the time this was
written and split by #321) contains a brace or a character class, so `.env`,
`.env.*`, `node_modules`, `.git` and the rest match exactly as they did —
`TestDefaultExcludesStillExclude` pins a case per entry, over both lists, and
fails the build when an entry is added without one, and
`TestDefaultExcludesAreLiteralNotPrefixes` pins that `.envrc` is still published.
The entries ending in `/**` never reach the glob engine at all: the
trailing-`/**` branch returns before it.

## Consequences

`**` meant two different things in one package for the length of one issue.
`isExcluded` globbed with doublestar; `isIncluded`, which matches the
`package.json` `files` whitelist, still used `filepath.Match`. So `lib/**/*.js`
excluded `lib/top.js` as an ignore pattern and did not include it as a `files`
entry. #316 left that standing deliberately, because `files` decides what a
publish selects rather than what an ignore rule drops, and that is a different
blast radius. #350 then answered the question on its own terms and moved the
`files` side onto doublestar too. The amendment at the end of this ADR records
what that changed and what it did not.

The same swap silently fixed a platform inconsistency nobody filed.
`filepath.Match` reads its separator from the platform, and `collectFiles` hands
`isExcluded` a path already through `filepath.ToSlash`, so on Windows `/` was an
ordinary character and `*` crossed it freely: `src/*.pem` excluded `src/a/b.pem`
there and not on Linux or macOS, from identical inputs. doublestar's separator is
always `/`.

Users writing brace or class patterns get npm's behaviour, which is what most
will expect from a `.npmignore`. Users who had a literal `{`, `}` or `[` in a
filename and matched it by basename have to anchor the pattern to the full path
instead. README's File Filtering section records the trade, because a syntax
change nobody documented is how a mystery bug report gets filed six months later.

## Considered options

**Keep the widening and document it.** Chosen. It costs fail-open edge cases
against filenames almost nobody has, all of them still reachable by a pattern
that names the full path, and it buys the syntax npm users already expect
without a bespoke matcher in the code path that enforces `.env` exclusion.

**Pre-escape `{` and `}` before handing the pattern to doublestar.** Rejected as
only a partial fix bought at the price of a second dialect. It suppresses the
brace widening but not the `[!a]` one, which has no escape that survives both
matchers, so the result is neither `filepath.Match`'s syntax nor doublestar's but
a third thing that exists only here and that nobody can look up.

**Hand-roll `**` on top of `filepath.Match` and avoid the library.** Rejected.
It is the only option that changes nothing else, but writing a segment matcher by
hand puts new untested logic in the function that decides whether `.env` and
`node_modules` ship — the one place in this codebase where a subtle bug is least
recoverable, since the person it surprises finds out after publishing. doublestar
is already a direct dependency, exercised by workspace globbing, and its `**` is
the semantics being copied in the first place.

## Amendment: #350 puts the `files` side on the same engine

`matchFilesField`, which serves the `package.json` `files` whitelist, globs with
doublestar too now. `**` therefore means the same thing everywhere in the
package: `files: ["lib/**/*.js"]` selects `lib/top.js` as well as
`lib/sub/a.js`, and a bare `["**"]` selects the whole tree instead of the
package root alone.

**The syntax it inherits is not the same trade.** Everything above about braces
and character classes applies to these patterns verbatim, and the direction of
harm inverts. An `.npmignore` is git's format, so widening it moves away from the
tool being imitated and a pattern that used to exclude a file stops doing so —
that is the fail-open cost this ADR accepted. A `files` entry is npm's format,
and npm globs it with minimatch, which is the dialect doublestar is closest to.
Both widenings this ADR named above therefore move *toward* npm on this side,
measured on npm 11.16.0 with `npm pack --dry-run` on a fixture package:

- `files: ["weird{a,b}.txt"]` ships `weirda.txt` and `weirdb.txt` and not the
  file literally named `weird{a,b}.txt`. doublestar now agrees on the first two.
- `files: ["[!a].txt"]` ships `!.txt` and `b.txt` and not `a.txt` — the class is
  a negation, as doublestar reads it and as `filepath.Match` did not.

That is a claim about those two constructs, not about the dialects as wholes.
doublestar is not minimatch: it has no extglob. `files: ["+(a|b).txt"]` and
`["@(a|b).txt"]` each ship `a.txt` and `b.txt` under npm 11.16.0, while
`doublestar.Match("+(a|b).txt", "a.txt")` is false — both run and confirmed — so
an entry written in extglob selects nothing here and a file there. That is the
same fail-closed shape as the `dist/*` gap below, it is not fixed by #350, and
it was not made worse by it either: `filepath.Match` had no extglob to lose.
`TestMatchFilesFieldGlobsWithDoublestar` carries the row.
(`!(a).txt` is not evidence in this direction. npm ships nothing for it, because
the leading `!` is read as a negated entry rather than as extglob.)

The escape hatch survives on this side and is wider than it is on the ignore
side. `matchFilesField` compares `pattern == relPath` before it globs, and a
`files` entry is only ever matched against the full path, so a literal-brace
entry still selects its file wherever it sits: `["weird{a,b}.txt"]` selects the
literal file *as well as* the two npm ships. That is a divergence from npm in the
maintainer's favour and is recorded in README's divergence list. The same holds
for doublestar's strictness about malformed patterns — an unbalanced `{` is a
hard error and the glob branch discards it — because `["{tmpl.txt"]` is answered
by the string compare first. npm ships that file too, so the two agree here.
Both runs confirmed on a fixture package; `TestMatchFilesFieldGlobsWithDoublestar`
carries the rows.

Two things did not change, deliberately:

- **The subtree branch stays**, the one that answers an entry ending in a slash
  and two stars. It compares strings, so `["weird{a,b}/**"]` still names that
  literal directory and its contents, where doublestar would expand the braces.
  That inverts npm exactly — npm ships `weirda/x.txt` and not
  `weird{a,b}/x.txt` for that entry — and it is kept because
  `matchesIgnorePattern` has its own copy of the branch, which the paragraphs
  above lean on. Deleting it turns exactly the two rows that pin it red and
  nothing else in the suite; both runs measured.
- **A glob-matched directory is still not expanded into its subtree.** npm ships
  `dist/cli/index.js` for `files: ["dist/*"]` and the whole tree for `["*"]`,
  because it treats a pattern matching a *directory* as selecting everything
  under it. Neither glob engine does that —
  `doublestar.Match("dist/*", "dist/cli/index.js")` is false, exactly as
  `filepath.Match`'s was — so the gap is npm's tree expansion rather than
  anything `**` decides, and #350 measured it and left it standing. lnpm expands
  a directory only when the entry names it literally.

A trailing `/` on a glob needed a branch of its own once the engine changed.
`filepath.Match("dist/**/", "dist")` was false, so `["dist/**/"]` selected
nothing by accident; `doublestar.Match("dist/**/", "dist")` is true.
`lastSegment("dist/**/")` is `""`, which is not a bare wildcard, so a naive swap
would have classified that entry as *naming* a file called `dist` — and under
#321 a named path overrides `defaultExcludes`. npm ships nothing at all for
`["dist/**/"]` or `["dist/*/"]`, so `matchFilesField` now answers a glob with a
trailing `/` with no match, on purpose rather than by accident.

None of this reaches the built-in exclusion lists. `matchFilesField` never
matches their entries at all — `hardReservedExcludes` is enforced by
`isHardReserved` in the walk and `defaultExcludes` is seeded into the ignore
chain — so the engine under the `files` field cannot move either list, and
`TestDefaultExcludesStillExclude` stays green.

#321's `filesMatch` classification is unchanged as well — `**` and `dist/*` still
end in a bare wildcard segment, so they sweep files in without consenting to
publish a default-excluded one. A wider-reaching `**` therefore selects strictly
more files and still does not publish `.env`;
`TestPackDoubleStarSweepsWithoutConsentingToDefaultExcludes` pins that.

One platform split closes with the engine, by the same mechanism the ignore
side's paragraph above records: `filepath.Match` reads its separator from the
platform, so on Windows a `files` entry's `*` crossed a `/` freely. doublestar's
does not — `Match` calls `matchWithSeparator(pattern, name, '/', …)` with the
separator written in, read from `doublestar/v4@v4.10.0/match.go`. The Windows
half is inherited from that earlier paragraph and was not re-run here; no local
run can settle it, and CI is what covers the platform.
