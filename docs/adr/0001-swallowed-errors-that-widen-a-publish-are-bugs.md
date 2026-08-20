# Swallowed errors that widen a publish are bugs; ones that narrow it are not

lnpm's publish paths repeatedly discarded an error and continued, and the same
`if err != nil { continue }` means opposite things depending on which way it
fails. When workspace pattern expansion discarded a failure on a `!`-prefixed
negation, the package the maintainer excluded was published into the shared
store; when it discarded one on an include pattern, or when package listing
skipped a member whose `package.json` would not parse, the result was publishing
less than asked. We treat the first as a bug to fix loudly and the second as a
judgement call to make case by case, because publishing a package the
maintainer deliberately excluded is not recoverable by the person it surprises,
while publishing too few is visible to the one running the command.

## Consequences

A reader will find the two directions handled differently in the same file, and
that asymmetry is deliberate rather than an oversight — the resolution is not
"make them consistent". Concretely, a malformed workspace glob pattern aborts
the operation and names the pattern, and so does a workspace member that will
not read, will not parse, or names no package.

That last group is the judgement call above, called in #246. Pattern expansion
already filters on `package.json` presence, so every member it hands to package
listing had one moments earlier: a failure there is a broken member of a
workspace the caller asked for, not the non-package directory weighed below.
The nameless member — no `name` key, an empty one, or a `null` document — was
included on the maintainer's explicit confirmation, knowing pattern expansion
does not filter on `name` and that a marker manifest such as `{"private": true}`
therefore fails the whole listing; publishing under an empty name, or resolving
a `workspace:` specifier against one, is the worse outcome. The abort is not
confined to `publish --all` — `pack.indexWorkspace` lists the same packages
whenever a single package carries `workspace:` dependencies, so one broken
sibling stops that publish too.

The rule is about direction, not about severity: a fail-open path is worth
fixing even when it needs a config typo to trigger, and a fail-closed path can
be left alone even when it is more likely to happen.

## Considered options

Fixing only the negation side was rejected: it leaves two different rules in
one function, arrived at separately, which is how they drift.

Warning and continuing was rejected because it keeps the behaviour that makes
the bug bad — `publish --all` still pushes the excluded package — and buys only
a message the user may not be watching for.

Hard-failing on any swallowed error, in both directions, was rejected as the
default because the fail-closed cases are not obviously wrong. A glob can
legitimately match a directory that is not a package, and pattern expansion
already filters on `package.json` presence for exactly that reason.
