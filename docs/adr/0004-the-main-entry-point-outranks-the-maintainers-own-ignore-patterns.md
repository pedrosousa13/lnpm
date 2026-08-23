# The `main` entry point outranks the maintainer's own ignore patterns

`defaultIncludes` omitted the manifest's `main`. `PackageJSON.Main` was parsed
and never consulted during selection, so `main: "lib/index.js"` alongside
`files: ["dist"]` published `[dist/a.js, package.json]`: the one path the
manifest advertises was not in the tarball, requiring the package failed at
runtime, and lnpm reported success (#319). npm ships the file `main` names
whatever the `files` field says. lnpm now does too — and goes one step further,
because under a `files` whitelist the path `main` names is force-included past
the maintainer's own `.npmignore` and `.gitignore` as well as past the whitelist.

That second step is the one worth stopping on. It means a publish can ship a
file the maintainer wrote into `.npmignore` by hand, which is a publish doing
more than it was asked, and `docs/adr/0001` calls that direction a bug in as many
words: *"publishing a package the maintainer deliberately excluded is not
recoverable by the person it surprises, while publishing too few is visible to
the one running the command."* This is not a swallowed error, so 0001 does not
literally govern it, but it runs against 0001's direction rule and a reader
deserves to hear that here rather than discover it in a tarball.

It is accepted because this case breaks the premise the asymmetry rests on.
0001 tolerates the fail-closed direction on the grounds that publishing too few
is *visible to the one running the command*. Here it is not.
`validation.ValidatePackage` checks `main` by `os.Stat`-ing it on disk, so a
`main` that exists but is excluded from the packed set passes validation
cleanly; `lnpm publish` then prints success and the defect surfaces only when a
consumer requires the package. Both directions therefore surprise someone, and
the choice is between surprising the maintainer with one extra file they can see
named in their own `main` field, and surprising every consumer with a package
that does not load. The costs are not symmetric either: a package missing its
README is poorer, a package missing its entry point is inert. We take the
narrower, louder failure.

What this does not override is `defaultExcludes`. `main: ".env"` does not ship
`.env`. That holds structurally rather than by a check in the force-include:
`collectFiles` evaluates `isDefaultExcluded` during the walk and returns early,
above the whitelist branch the force-include lives in. The distinction is the one
`isDefaultExcluded` already argues — the user's ignore patterns are a preference
the user expressed, the built-in list is a guard lnpm applies on the user's
behalf, and a guard that can be stepped around by naming the file in `main` is
not a guard. `TestPackMainCannotDefeatDefaultExcludes` pins it and goes red if
the force-include is hoisted above that check.

## Consequences

The exemption is narrower than the always-included set it sits beside, and the
inconsistency is deliberate. `README*`, `LICENSE*` and the rest are exempt from
the `files` whitelist only, and an ignore pattern still drops them; `main` is
exempt from both. A reader who finds the two treatments in the same `switch`
should not "make them consistent" without deciding, for each, whether losing the
file breaks the package or merely impoverishes it.

`main` was the only entry exempt from both when this was written, and is no
longer. `docs/adr/0005` makes the package root's own `package.json` unexcludable
by anything except `defaultExcludes`, in every mode rather than under a `files`
whitelist alone. There are now three treatments in that `switch`, and the
question to decide per file is unchanged.

It applies in whitelist mode only. A package with no `files` field is untouched:
there the user's ignore patterns decide the whole tree, `main` included, exactly
as before. That is #319's stated scope and it keeps the widening confined to
manifests that already opted into an explicit file list.
`TestPackMainRespectsIgnorePatternsWithoutFilesWhitelist` pins it.

Only the exact path is force-included, never a prefix of it. A `main` naming a
directory does not pull the directory's contents in, and no `..`, rooted or
drive-absolute spelling can select anything outside the package root.

Because a `main` can still go missing from the packed set — it may not be on
disk at all, or be held back by `defaultExcludes`, or be dropped by an ignore
pattern in non-whitelist mode — `Pack` now warns when the finished set does not
contain it. The warning is in `pack` rather than in the publish command because
`internal/cli/push.go` packs without validating at all and `lnpm publish
--skip-validation` turns the existing check off, so a warning wired into the
publish path alone would leave those routes as silent as #319 found them. It
warns and continues; the abort stays where it already was, in
`validation.ValidatePackage`, which every ordinary `lnpm publish` runs before
packing.

## Considered options

**Force-include `main` past both the whitelist and the ignore patterns.**
Chosen. It is the only option that closes #319 on every route, and the file it
newly ships is one the manifest itself points at, so the maintainer has a name to
search for.

**Treat `main` as another entry in the always-included set, exempt from the
`files` whitelist but still droppable by an ignore pattern.** Rejected, and it
was the documented rule until now — README stated that the always-included set is
"exempt from the `files` whitelist only". It reads as the conservative choice and
is not: an `.npmignore` naming the entry point still drops it, validation still
passes because the file is on disk, publish still reports success, and the
package still does not load. It closes the reported symptom while leaving the
failure it was reported for reachable, and invisible.

**Force-include `main` in every mode, including packages with no `files`
field.** Rejected as a much wider change bought for no extra coverage of the
reported bug. It would override ignore patterns for every package lnpm publishes
rather than only those that opted into an explicit file list, and #319's
acceptance criteria state that a package with no `files` field is unaffected.

**Abort the pack when `main` is not in the packed set.** Rejected. `pack.Pack`
is a library entry point used by `lnpm push` and by callers that have no publish
semantics, so an abort there changes far more than the publish contract, and
`validation.ValidatePackage` already refuses a manifest whose `main` is not on
disk before publish packs anything. A warning gives the missing-from-the-tarball
case the visibility it lacked without moving the abort.

This holds for `main` and is superseded for the manifest. `docs/adr/0005` adds
an abort to `Pack` for a packed set with no `package.json` in it, on the
distinction this paragraph rests on: the abort was refused here because
`validation.ValidatePackage` already catches the `main` case, and nothing
catches the manifest case at all — `readPackageJSON` reads from disk rather than
from the packed set, so it passes precisely when the manifest is missing from
the pack.
