# Swallowed errors that widen a publish are bugs; ones that narrow it are not

lnpm's publish paths repeatedly discard an error and continue, and the same
`if err != nil { continue }` means opposite things depending on which way it
fails. When workspace pattern expansion discards a failure on a `!`-prefixed
negation, the package the maintainer excluded is published into the shared
store; when it discards one on an include pattern, or when package listing
skips a member whose `package.json` will not parse, the result is publishing
less than asked. We treat the first as a bug to fix loudly and the second as a
judgement call to make case by case, because publishing a package the
maintainer deliberately excluded is not recoverable by the person it surprises,
while publishing too few is visible to the one running the command.

## Consequences

A reader will find the two directions handled differently in the same file, and
that asymmetry is deliberate rather than an oversight — the resolution is not
"make them consistent". Concretely, a malformed workspace glob pattern aborts
the operation and names the pattern, while an unreadable workspace member is
still an open question tracked separately.

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
