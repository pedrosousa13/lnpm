# `latest` is not a garbage collection root

`lnpm gc` collects a stored version when no valid link and no tag reaches it.
The `latest` tag is excluded from the tags that count.

Every publish moves `latest` onto the version it just wrote, so `latest` always
names something and names it without anyone deciding to. Counting it as a root
would mean no current version of any package could ever be collected — and a
store where nothing has been published twice holds only current versions, so
`lnpm gc` would run, report nothing and free nothing. The command a user reaches
for to reclaim disk would silently stop reclaiming any.

A tag someone set is a decision to keep a build; `latest` is a side effect of
the last publish. What `latest` names is still kept for as long as a project
links it, which is the rule gc has always applied.

## Consequences

`lnpm publish` followed by `lnpm gc` with nothing linked still removes the
package, exactly as before tags existed. `lnpm publish --tag beta` followed by
the same `gc` keeps the build: nothing links it, but a tag names it. Removing it
then takes two steps — drop the tag, then collect.

A version can therefore outlive the package's name: if `latest` names an
unlinked version and some other tag names an older one, gc takes the first,
which clears `packages_by_name`. The package is still in the store under its
other tag, and a tag-aware `add` still resolves it, but a lookup by name alone
no longer finds it. That is the same state a store reaches by deleting the
`latest` tag, which `db.DeleteTag` refuses for the same reason it is worth
naming here.

Superseded versions — the ones no tag names any more, because publishing moved
the tag on — are collected. Before this they were unreachable from the database
entirely, since the one record per name was overwritten, so their directories
accumulated in the store with nothing able to name or remove them.

## Considered options

**Every tag is a root, `latest` included.** This is the rule as first stated. It
was rejected on the evidence above: four existing gc tests assert that a
published-but-never-linked package is collected, and they assert it because that
is what the command is for.

**Collect the whole name when no version of it is linked, and apply the tag rule
only within a name that is still linked somewhere.** This keeps every existing
test passing too, and it needs no exception for `latest`. It was rejected
because it deletes a beta nobody has linked yet, which is the state a beta is in
between being published and being tried — the case tags were added for. The rule
also has to be stated in two clauses, and the second one silently overrides a
tag the user set.

**Never collect a tagged version, and let `lnpm tag --delete` be the only way to
release one.** This is the conservative extreme and it is what the chosen rule
does for every tag but `latest`. Extending it to `latest` is the first option
again.
