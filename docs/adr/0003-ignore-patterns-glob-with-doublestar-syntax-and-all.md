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

None of this reaches the default exclusion list. No `defaultExcludes` entry
contains a brace or a character class, so `.env`, `.env.*`, `node_modules`,
`.git` and the rest match exactly as they did — `TestDefaultExcludesStillExclude`
pins a case per entry and fails the build when an entry is added without one, and
`TestDefaultExcludesAreLiteralNotPrefixes` pins that `.envrc` is still published.
The entries ending in `/**` never reach the glob engine at all: the
trailing-`/**` branch returns before it.

## Consequences

`**` now means two different things in one package. `isExcluded` globs with
doublestar; `isIncluded`, which matches the `package.json` `files` whitelist,
still uses `filepath.Match`. So `lib/**/*.js` excludes `lib/top.js` as an ignore
pattern but does not include it as a `files` entry. That asymmetry is deliberate
and left standing — `files` is a separate concern with its own npm-divergence
already documented in README, and #316 is about the ignore path. A reader who
finds the two functions side by side should not "make them consistent" without
deciding that question on its own.

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
