// Package pack selects the files a publish ships and hashes them.
//
// It writes its warnings to stdout itself, through internal/ui, so they carry
// the same marker as every other lnpm notice. Printing from a library is not
// the layering this wants; internal/ui's package doc records the direction —
// pack returns its notices and the CLI renders them — and why that was not
// taken here. Until it is, the warnings Pack emits are pack's own.
package pack

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	// Aliased slashpath, not path, so it does not collide with the "path"
	// variables this file is full of: filepath.Walk's callback parameter,
	// readIgnoreFile and HashFile all name one. filepath.Dir is not a
	// substitute for it — on Windows filepath.Clean rewrites "/" as "\", which
	// would corrupt the slash-separated cache keys and scope prefixes that
	// ignoreLoader is built on.
	slashpath "path"
	"strings"
	"sync"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/cespare/xxhash/v2"
	"github.com/panjf2000/ants/v2"
	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/ui"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// PackageJSON represents the relevant fields from package.json
type PackageJSON struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Main    string   `json:"main"`
	Files   []string `json:"files"`
}

// FileInfo contains information about a file to be packed
type FileInfo struct {
	Path        string      // Absolute path
	RelPath     string      // Relative path from package root
	Size        int64       // File size
	Mode        os.FileMode // File permissions
	ContentHash string      // xxhash of content
	ModTime     int64       // Unix nano timestamp
}

// manifestFileName is the package root's own manifest, the one path no selection
// rule may remove from a pack.
//
// It names the three sites that enforce that rule — collectFiles' two selection
// sites and Pack's backstop — so they cannot disagree about which path they are
// protecting. It is not the package's only spelling of "package.json" and does
// not claim to be: defaultIncludes carries it as a pattern, readPackageJSON
// joins it to a directory, and several tests write it as a fixture path. Those
// are pre-existing and left alone.
const manifestFileName = "package.json"

// changelogNames are the three always-included names lnpm adds to npm's set.
// npm's own set is package.json, README*, LICENSE*/LICENCE* and the file named
// by "main" — the last of those is not an entry in any list, here or at npm, and
// lnpm reaches it through collectFiles' mainEntry arm instead (docs/adr/0004).
// README's npm-divergence bullet records these three names as deliberate.
//
// Run, not read, against npm 11.16.0 via "npm pack --dry-run --json" on a
// package with "files": ["dist"]: root CHANGELOG, CHANGELOG.md, CHANGES.md and
// HISTORY.md were all left out of the tarball, so these three really are lnpm's
// addition rather than a spelling of something npm already does. The same run
// shipped README.md and LICENSE, and a second one shipped a "main" of
// lib/entry.js while dropping its sibling lib/other.js.
//
// They share one extension list rather than carrying three of their own, and
// that is a decision rather than a convenience. The three are one concept — the
// file a package keeps its release notes in — so the spellings lnpm accepts for
// one are the spellings it accepts for all three by construction. Three
// per-name lists would let a later edit accept CHANGELOG.rst and refuse
// HISTORY.rst, and nothing in the code would say why.
var changelogNames = []string{"CHANGELOG", "CHANGES", "HISTORY"}

// changelogExtensions is the accepted extension set for changelogNames, and the
// one place it is written down. Membership is deliberately small, because every
// member widens a hole that fails open: these files ship past a "files"
// whitelist, so an extension accepted here is an extension a maintainer cannot
// hold back by writing "files" correctly.
//
// Each member, with its reason:
//
//   - "" — no extension. "CHANGELOG" with no suffix is the oldest spelling and
//     still common. It is also the form that tells the two candidate fixes
//     apart: a narrowing written as "match the old prefix glob, then reject by
//     extension" drops it, because there is no extension to accept.
//   - ".md" — Markdown, the dominant spelling today.
//   - ".markdown" — the unabbreviated spelling of the same format. Accepting
//     one and refusing the other would be arbitrary.
//   - ".txt" — plain text. Recorded by the maintainer on #360, superseding that
//     issue's own example: the hole being closed is *arbitrary* extensions, and
//     plain text is one of the two or three ways a changelog is really spelled,
//     especially in older packages. Refusing it would break real packages to
//     close a hole it is not part of.
//   - ".rst" — reStructuredText, named by the maintainer on #360.
//
// Deliberately out: ".db", ".sqlite", ".json", ".log" and every other extension
// not listed. Those name data, not documentation, and a root history.db shipping
// past a "files": ["dist"] whitelist is the bug #360 fixed. A package that
// really wants one of them published names it in "files"; that route is
// unaffected. ".adoc" and ".html" are the closest calls and are still out — a
// changelog is written in them rarely enough that naming it in "files" is the
// smaller cost.
var changelogExtensions = []string{"", ".md", ".markdown", ".txt", ".rst"}

// defaultIncludes are files always included regardless of config.
//
// The first block is npm's set, spelled as prefix globs, and it is left exactly
// as it is. README* does force-include a root readme.db, which is the same shape
// as the changelog bug #360 closed — but npm behaves that way too, and matching
// npm on npm's own entries is the choice this list makes. The asymmetry with the
// block below is deliberate, not an oversight; do not "fix" it by globbing the
// changelog names again or by narrowing these.
//
// The second block is the changelog names, one entry per name-and-extension
// pair, replacing the "CHANGELOG*", "CHANGES*" and "HISTORY*" globs #360 found.
// Those globs matched any root file whose name merely started with one of the
// three words, whatever its extension, so a "files": ["dist"] whitelist did not
// hold back a root history.db — an OWASP A01 failure that fails open, since it
// ships a file the author never selected.
//
// Every name is spelled once. isDefaultInclude lower-cases both the pattern and
// the path before matching, so one spelling answers for every casing of a name
// and a second one can never match anything the first does not. Until #363 the
// first block carried "readme*", "license*" and "licence*" beside their
// uppercase partners — dead weight that predated the matcher growing its
// ToLower, and not even uniform, since package.json was already spelled once.
// Do not add a cased duplicate back. That is not left to this comment:
// TestDefaultIncludesHoldNoCasedDuplicates fails the build on two entries that
// are equal once lowered, and TestDefaultIncludesMatchCaseInsensitivelyAtTheRoot
// asserts the fold over every entry, so a new entry is covered the moment it is
// added.
//
// LICENCE* and LICENSE* are not such a pair and both stay. They are the British
// and American spellings of the word, differing by a letter rather than by a
// case, so no fold relates them — filepath.Match("licence*", "license") is
// false, run and confirmed — and deleting either drops a real name rather than a
// duplicate. That is the headline way to get this list's deduplication wrong,
// which is why it is stated here and not left to be re-derived. The
// no-cased-duplicates test passing is the same claim from the useful side: the
// two do not collide under the fold.
var defaultIncludes = append([]string{
	"package.json",
	"README*",
	"LICENSE*",
	"LICENCE*",
}, changelogIncludes()...)

// changelogIncludes expands changelogNames against changelogExtensions. The
// entries carry no glob metacharacter, so filepath.Match compares each as a
// literal — TestIsDefaultInclude pins the resulting membership, which is where
// to read the expansion rather than inferring it from here.
func changelogIncludes() []string {
	out := make([]string, 0, len(changelogNames)*len(changelogExtensions))
	for _, name := range changelogNames {
		for _, ext := range changelogExtensions {
			out = append(out, name+ext)
		}
	}
	return out
}

// hardReservedExcludes are patterns nothing publishes. Not a "files" entry, not
// an "!" negation in .npmignore or .gitignore, not the path named by "main".
// They are the one rule in the selection path with no override at all, which is
// what makes them worth naming apart from defaultExcludes below: "lnpm's
// default" and "never publishable" are different claims, and before #321 both
// lived in one list and neither could be told from the other.
//
// The difference is what a maintainer can do about it. A defaultExcludes entry
// yields to a maintainer who names the path in "files" or negates it in an
// ignore file; an entry here yields to nothing, and naming one in "files" earns
// a warning instead (warnHardReservedFilesEntry). "main" outranks neither list —
// that is docs/adr/0004's boundary and #321 did not move it — so it is not the
// axis these two differ on.
//
// The membership rule is npm's, not lnpm's invention, but npm publishes two
// lists and this one does not line up with either exactly. Read from
// docs.npmjs.com/cli/v11/configuring-npm/package-json, "files":
//
//   - npm's "always ignored by default" list is the long one: *.orig, .*.swp,
//     .DS_Store, ._*, .git, .hg, .lock-wscript, .npmrc, .svn, .wafpickle-N, CVS,
//     config.gypi, node_modules, npm-debug.log, package-lock.json,
//     pnpm-lock.yaml, yarn.lock, bun.lockb. Every entry here except the git
//     metadata block and the last block is on it, the "/**" entries being
//     lnpm's second spelling of a name already there rather than extra names.
//   - npm's "cannot be included even if specified in the files globs" list is
//     the short one, and it is the true analogue of this list: .git, .npmrc,
//     node_modules, package-lock.json, pnpm-lock.yaml, yarn.lock, bun.lockb.
//
// So lnpm is stricter than npm on five entries — .hg, .svn, CVS, .DS_Store and
// *.orig, which npm ignores by default but will include for a "files" glob —
// stricter again on the three git metadata files, which are on neither npm list
// at all and whose block below carries the reason (#398), and matches npm
// exactly on the rest of the short list. The parity clause — matching npm on
// the rest of the short list, not the git one before it — was
// false until #399: the four lockfiles were on neither of lnpm's lists, so
// `lnpm publish` shipped a lockfile `npm publish` has not shipped for many
// major versions. Closing the gap was out of #321's scope, which decided only
// where the line between the two lists falls and not which names sit on either
// side of it. Adding a name here takes a reason of the kind npm's short list
// takes — that publishing it is never what anyone meant — rather than a reason
// of the kind defaultExcludes takes.
//
// Two near-misses are worth naming so a later reader does not read parity into
// the overlap. npm's ".*.swp" is narrower than lnpm's "*.swp", and npm's
// "npm-debug.log" is narrower than lnpm's "*.log"; #321 left both of lnpm's
// broader forms on the overridable side rather than narrow them to npm's
// spelling in order to move them here.
//
// The last block is lnpm's and yalc's own state. Those files record absolute
// paths on the machine that wrote them, so publishing one leaks the maintainer's
// home directory layout into a tarball, which is the same "never what anyone
// meant" test the npm entries pass.
//
// .DS_Store is here and Thumbs.db is in defaultExcludes below. That asymmetry
// looks like an oversight and is not: npm force-ignores the first and not the
// second, and matching npm's line is the rule this list follows. Do not "fix" it
// by moving one to join the other.
var hardReservedExcludes = []string{
	".git",
	".git/**",

	// The three git metadata files. This is a deliberate divergence from npm:
	// none of the three is on either npm list quoted above, and npm 11.16.0
	// packs all three for "files": [".gitignore", ".gitattributes",
	// ".gitmodules"] — run on 2026-08-25 with `npm pack --dry-run --json`,
	// not inferred from the two lists — where lnpm packs none of them.
	// Recorded here and in README's npm-divergence list rather than left to be
	// rediscovered as an accident.
	//
	// The same run answers the no-"files" case, and the three do not behave
	// alike there: npm packs .gitattributes and .gitmodules and drops the
	// .gitignore it read as ignore rules. So "on neither npm list" is a claim
	// about the two documented lists and not a claim that npm publishes all
	// three by default. lnpm holds back all three either way.
	//
	// The reason is that lnpm already refused to publish them, and #398 is
	// about saying so rather than about a new refusal. filterGitFiles runs over
	// the finished set in Pack and drops these three by basename at any depth,
	// unconditionally, and it predates the #321 split. #321 nonetheless put
	// .gitignore and .gitattributes in defaultExcludes, the overridable tier —
	// an assignment the filter made inert, since a "files" entry selected the
	// file and the filter took it straight back out. The user asked for a path,
	// did not get it, and warnHardReservedFilesEntry stayed quiet because the
	// warning fires for this list only. Silence is the failure mode #321 exists
	// to remove, so the tier that matches the behaviour is this one.
	// .gitmodules was in neither list and is named here for the first time.
	//
	// A "files" entry naming one now warns, an "!" negation cannot re-include
	// one, and "main" cannot reach one — the three properties every other entry
	// on this list has. filterGitFiles stays where it is, as the
	// defence-in-depth pass it has always been rather than the sole enforcement.
	//
	// The bare-name spelling is what makes the two agree, and it was measured
	// rather than read off the pattern syntax. isGitRelatedPath matches a
	// basename at any depth; a separator-free unanchored pattern reaches
	// matchesIgnorePattern's basename branch and does the same. The two obvious
	// alternatives were run rather than argued about, on 2026-08-25, against
	// docs/.gitignore: "/.gitignore" is anchored so the basename branch is
	// skipped, and ".gitignore/**" takes the trailing-"/**" branch, which wants
	// an exact match or a ".gitignore/" prefix. Both answer false there where
	// isGitRelatedPath answers true, so both would leave the tier and the filter
	// disagreeing at depth; the bare name answers true. The standing measurement
	// lives in TestGitMetadataTierAgreesWithTheGitSafetyFilter, whose table runs
	// the two predicates against each other over the three names at the root and
	// one level down, .gitignore three levels down as well, and five
	// near-misses. None of the three is a directory, so none takes the "/**"
	// second spelling the entries above carry.
	//
	// How much this list now carries was measured too, on 2026-08-25, by
	// commenting out Pack's filterGitFiles call: `go vet ./...` clean first,
	// then internal/pack and tests each printing `ok` with a duration rather
	// than a build failure. So the selection path refuses these on its own and
	// the filter is genuinely a second answer. filterGitFiles' own doc comment
	// carries the full statement of what that run established.
	".gitignore",
	".gitattributes",
	".gitmodules",

	".hg",
	".hg/**",
	".svn",
	".svn/**",
	"CVS",
	"CVS/**",
	".DS_Store",
	".npmrc",
	"node_modules",
	"node_modules/**",
	"*.orig",

	// The four lockfiles on npm's short list, quoted in full above: they
	// "cannot be included even if specified in the files globs". That wording
	// is this list's own membership test, so they belong here and not in
	// defaultExcludes — a "files" entry naming one earns the warning
	// warnHardReservedFilesEntry prints rather than a place in the tarball.
	//
	// The reason is not only that a published lockfile is dead weight. It is a
	// disclosure surface: package-lock.json records a "resolved" URL per
	// dependency, so a project installing through a private registry publishes
	// that registry's hostname, and the names and versions of its private
	// packages, to anyone who unpacks the tarball.
	//
	// bun.lockb is here although lnpm never opens one. Nothing lnpm does with
	// any of these four names goes further than os.Stat, and there are two such
	// call sites, neither of which looks at a packed set:
	//
	//   - internal/config's DetectPackageManager stats all four in a consuming
	//     project's directory and returns which package manager to shell out
	//     to. Seven call sites across five files read that answer, counted on
	//     2026-08-25 and each one read: three turn it into the install command
	//     they run, three record it in the project's database row, and one
	//     picks the binary for a "run <script>". The split is here only to say
	//     that none of the seven is ever handed a packed set — re-count it
	//     rather than trust the number.
	//   - internal/workspace's parsePackageJSONWorkspace stats yarn.lock and
	//     bun.lockb in the workspace root to set Workspace.Type. That field
	//     picks no package manager: its only non-test readers are the two
	//     fmt.Printf calls at internal/cli/publish.go:102 and :104, which name
	//     the workspace in a progress line.
	//
	// So this entry breaks neither. npm's list names bun.lockb, and a lockfile
	// lnpm cannot parse is still a lockfile it should not publish.
	//
	// Bun 1.2's text lockfile, bun.lock, is deliberately not here even though
	// DetectPackageManager stats it beside bun.lockb. Neither npm list quoted
	// above carries that name, so adding it would be lnpm's own membership
	// decision rather than the npm parity the rest of this block rests on.
	// TestPackShipsBunLockKnownGap pins what that costs — a bun.lock ships,
	// root and nested — as a known gap rather than a property worth keeping, so
	// the day a follow-up adds the name here, the test and this paragraph go
	// red together instead of only this paragraph going quietly false.
	//
	// All four are bare names with no separator, so applyIgnorePatterns matches
	// them against filepath.Base and covers a nested one as well as a root one.
	// None is a directory, so none takes the second "/**" spelling the entries
	// above carry. TestPackNeverPublishesLockfiles pins both depths.
	"package-lock.json",
	"yarn.lock",
	"pnpm-lock.yaml",
	"bun.lockb",

	// lnpm's and yalc's own state, each of which records absolute host paths.
	".lnpm",
	".lnpm/**",
	"lnpm.lock",
	// The snapshot `lnpm retreat` leaves in place of the lock file. The
	// documented publish flow is retreat, then publish, so it is sitting in the
	// package root at exactly this moment, and it records an absolute source
	// path per linked package. The patterns here are literal, not prefixes, so
	// "lnpm.lock" above does not cover it.
	lockfile.RetreatFileName,
	".yalc",
	".yalc/**",
	"yalc.lock",
}

// defaultExcludes are patterns excluded unless the package selects them anyway.
// They are what lnpm withholds from a package that has not asked for them, and
// the maintainer's two selection rules outrank them: a "files" entry naming the
// path, and an "!" negation in an ignore file.
//
// The two do not have equal reach, and the asymmetry is forced rather than
// chosen. A "files" field turns off the ignore chain for the two whitelist arms
// that select ordinary paths — isIncluded and mainEntry — because #318 makes a
// "files" entry beat .npmignore and .gitignore. An "!" negation says nothing to
// those arms, so "files": ["dist"] with "!dist/.env" still refuses dist/.env.
// Naming the path in "files" is the route for such a package.
//
// One arm still consults the chain, and the negation still reaches through it:
// isDefaultInclude. Its paths are the defaultIncludes set, which overlaps this
// list — a root file matching one of that set's glob entries (readme, license,
// licence) *and* one of the suffix patterns here (*.log, *~, *.tgz, *.swp,
// *.swo). So "!readme.log" does re-include readme.log under a "files" whitelist.
//
// The overlap used to include the changelog names, when they were globs too, and
// "!changes.log" was this paragraph's example. #360 replaced those globs with an
// explicit name-plus-extension set, and no spelling it accepts matches any
// pattern here — run and confirmed by testing isDefaultExcluded over every entry
// changelogIncludes produces, none of which matched. package.json is outside the
// overlap for the same reason it always was; nothing here matches it.
//
// TestPackNegationDoesNotOverrideInWhitelistMode pins the rule and
// TestPackNegationOverridesInWhitelistModeForDefaultIncludes pins the exception.
//
// "main" does not, and the asymmetry is deliberate rather than an oversight.
// A "files" entry and an "!" negation say "publish this"; "main" says "this is
// the entry point", and treating that as publication consent would ship a .env,
// a log or a *.tgz on the strength of one manifest field. The check that keeps
// main out is explicit, in collectFiles' mainEntry arm, because the arm does not
// consult the ignore chain this list now rides in. docs/adr/0004 records the
// boundary; TestPackMainCannotDefeatDefaultExcludes pins it.
//
// No pattern here appears on either npm list quoted above — not the short
// "cannot be included" one, which is the line hardReservedExcludes draws, and
// not the long "always ignored by default" one either. Every entry is lnpm's own
// addition. "*.swp" and "*.log" are the two close calls and are discussed above,
// since npm's ".*.swp" and "npm-debug.log" are both narrower.
//
// Two consequences are worth saying out loud rather than leaving to be
// rediscovered:
//
//   - ".env" and ".env.*" are overridable, and that is #321's whole point.
//     ".env.example" is a template rather than a secret and publishing it is
//     ordinary, and before the split there was no way to do it. It follows that
//     "files": [".env"] publishes .env — which is what npm does too, since npm
//     ignores .env on neither list. lnpm is stricter than npm by default here
//     and no longer stricter than npm when the maintainer names that exact path.
//     Naming a directory containing it does not count; see collectFiles'
//     isIncluded arm. main ".env" is still refused; see above.
//   - ".npmignore" is the only ignore file left on this list, and it really is
//     overridable: "files": [".npmignore"] publishes it. Publishing an
//     .npmignore is harmless — it describes what was left out, not a secret.
//     ".gitignore" and ".gitattributes" were here until #398 and are not
//     interchangeable with it: filterGitFiles drops those two by basename at
//     every depth whatever this chain decides, so their place here was inert
//     and, worse, silent, and #398 moved them to hardReservedExcludes where
//     naming one warns. ".gitmodules" is there too. Do not read the remaining
//     entry as "ignore files are overridable" — it is one name, not a class.
//     TestPackFilesEntryNamingAnIgnoreFile pins all four.
var defaultExcludes = []string{
	".npmignore",
	"Thumbs.db",
	"*.log",
	"*.swp",
	"*.swo",
	"*~",
	".env",
	".env.*",
	"*.tgz",
}

// lowerHardReservedExcludes and lowerDefaultExcludes are the two lists
// lower-cased once, for the case-insensitive matching isHardReserved and
// isDefaultExcluded do. Both are derived rather than written out so a list and
// its fold cannot drift: a new entry added above is folded here whatever case it
// is spelled in.
//
// Lowering the patterns and not just the path is safe because no entry in either
// list carries a character class, brace or escape — the invariant
// docs/adr/0003-ignore-patterns-glob-with-doublestar-syntax-and-all.md already
// records, there for keeping the move to doublestar clear of this list. It
// matters again here for a different reason: strings.ToLower would rewrite
// "[A-Z]" into "[a-z]" and "\A" into "\a", changing what the pattern matches
// rather than how it is cased. TestDefaultExcludesHoldNoMetacharactersLoweringWouldRewrite
// pins it, over both lists.
var (
	lowerHardReservedExcludes = lowerPatterns(hardReservedExcludes)
	lowerDefaultExcludes      = lowerPatterns(defaultExcludes)
)

func lowerPatterns(patterns []string) []string {
	lowered := make([]string, len(patterns))
	for i, pattern := range patterns {
		lowered[i] = strings.ToLower(pattern)
	}
	return lowered
}

// Pack determines which files should be included in a package publish
func Pack(packageDir string) (*PackageJSON, []*FileInfo, error) {
	debug.Logf("pack: scanning %s", packageDir)
	// Read package.json
	pkgJSON, err := readPackageJSON(packageDir)
	if err != nil {
		return nil, nil, err
	}
	debug.Logf("pack: found %s@%s", pkgJSON.Name, pkgJSON.Version)

	// Collect files using custom filtering
	mainEntry := mainEntryPath(pkgJSON.Main)
	files, err := collectFiles(packageDir, pkgJSON.Files, mainEntry)
	if err != nil {
		return nil, nil, err
	}

	// Apply explicit .git safety filter (defense in depth)
	files = filterGitFiles(files)

	debug.Logf("pack: collected %d files (after safety filters)", len(files))

	if err := requireManifestPacked(files); err != nil {
		return nil, nil, err
	}

	warnHardReservedFilesEntry(pkgJSON.Files)
	warnHardReservedIgnoreNegation(packageDir)
	warnMainEntryNotPacked(pkgJSON.Main, mainEntry, files)

	return pkgJSON, files, nil
}

// warnHardReservedFilesEntry reports "files" entries naming a path lnpm never
// publishes. It warns and returns; it never fails the pack, and it never changes
// what is packed.
//
// It reads the manifest's entries rather than the packed set, which is the
// opposite of what its sibling warnMainEntryNotPacked does, and the reason is
// that the packed set cannot answer this question. collectFiles prunes a
// hard-reserved directory with filepath.SkipDir, so no path under node_modules
// or .git is ever walked and no absence in the finished set distinguishes "the
// guard held it back" from "it was not there". Deriving from the entries is the
// only route that reports the two cases the issue names.
//
// It says nothing about defaultExcludes, on purpose. Naming ".env.example" in
// "files" now publishes it, so there is nothing to report; that is the whole
// point of the split.
//
// ".gitignore", ".gitattributes" and ".gitmodules" were that gap until #398:
// filterGitFiles stripped all three from the finished set while the first two
// sat in defaultExcludes and the third in neither list, so naming one warned
// nothing and the file did not ship either. All three are in
// hardReservedExcludes now, so naming one reaches this warning like any other
// entry on that list. TestPackFilesEntryNamingAnIgnoreFile pins it.
//
// It prints through ui.IconWarn(), the marker every lnpm warning carries, for
// the reason warnMainEntryNotPacked gives below.
//
// The message quotes the entry as the manifest spells it, not the normalized
// form, because that is the string the reader will search package.json for.
func warnHardReservedFilesEntry(filesField []string) {
	for _, entry := range filesField {
		if !namesHardReserved(entry) {
			continue
		}

		fmt.Printf("%s package.json \"files\" names %q, which lnpm never "+
			"publishes; it will not be in the package\n", ui.IconWarn(), entry)
	}
}

// warnHardReservedIgnoreNegation is warnHardReservedFilesEntry for the other
// override mechanism. #321 gives a maintainer two ways to ask for a path lnpm
// excludes by default — a "files" entry and an "!" negation — and a refused ask
// should say so whichever way it was written.
//
// It reads the package root's ignore file only, through loadIgnorePatterns, so
// it sees .npmignore or, where there is none, .gitignore. That is a narrower
// reach than collectFiles', which reads one ignore file per directory, and it is
// a real gap rather than an argument that nested files cannot matter: a
// "!node_modules" written in src/.npmignore refuses src/node_modules and is
// still silent here. Covering that would mean enumerating negations across every
// scope ignoreLoader visits and plumbing the loader out of collectFiles, which is
// more machinery than a warning is worth; the root file is where a maintainer
// writing a negation against node_modules or .git would put it.
//
// Not a gap, and not worth serving: a negation inside a hard-reserved directory,
// such as node_modules/.npmignore. collectFiles prunes those directories with
// filepath.SkipDir before their ignore files are read, so the negation was never
// consulted for any purpose.
//
// Like its sibling it reports the request rather than an observed drop, so it
// fires for a negation against a path that is not on disk at all. The message
// stays true either way — that path will not be in the package — and the
// alternative is comparing against a packed set that cannot distinguish "held
// back" from "absent".
func warnHardReservedIgnoreNegation(packageDir string) {
	for _, pattern := range loadIgnorePatterns(packageDir) {
		if !strings.HasPrefix(pattern, "!") || !namesHardReserved(pattern[1:]) {
			continue
		}

		fmt.Printf("%s the package's ignore file negates %q, which lnpm "+
			"never publishes; it will not be in the package\n", ui.IconWarn(), pattern)
	}
}

// namesHardReserved reports whether one entry a maintainer wrote — a "files"
// entry or the body of an "!" negation — plainly names a hard-reserved path.
//
// It is deliberately literal, matching npm's own reading of a "files" entry: a
// leading "/" anchors rather than rooting, a leading "./" is no part of the
// name, and a trailing "/" marks a directory, so "node_modules",
// "/node_modules", "./node_modules" and "node_modules/" all answer true. It
// does not try to decide whether an arbitrary glob would have selected a
// hard-reserved path — "*" against the built-in list is a question with no
// useful answer, and #321 asks only for entries that name such a path plainly.
//
// An entry that is empty once normalized ("", "/", "//", "./") means "ship
// everything" to isIncluded rather than naming any path, so it names nothing to
// report. A bare "." is not one of those — normalizeFilesEntry leaves it alone,
// as its own comment records, and isHardReserved(".") is false. Run and
// confirmed for all five.
//
// The normalization is normalizeFilesEntry, the call matchFilesField and
// filesFieldMayReach already share, rather than a third spelling of those trims.
// #402 made it shared, and measured the change rather than assuming it, because
// the two spellings were not equivalent: this function used to trim a trailing
// "/" unconditionally where normalizeFilesEntry trims it only from an entry
// with no "*", npm reading no directory marker into a trailing slash on a glob.
//
// That difference cannot move this predicate, which was run rather than argued.
// Measured on 2026-08-25 over 3,780 entries — nine prefixes ("", "/", "./",
// ".//", "././", "/./", "//", "..", "../") crossed with twelve suffixes and
// thirty-five cores. The cores are the twenty distinct names on the list above
// (deduped past their "/**" spellings), the five spellings that normalize to
// "" or hit the "." trap ("", "/", "//", "./", "."), and ten non-reserved,
// deliberately glob-shaped spellings ("a", "a/", "a/**", "a/**/", "*", "*/",
// "**", "**/", "b/**", "dist/**/") chosen to stress the one place a glob and a
// plain name disagree. Sharing the helper and keeping the old trims with a
// "./" trim added in front of them disagreed on zero across all 3,780. The
// glob spellings are where the difference shows and where it stops:
// "node_modules/**/" answers true either way, since the old trims cut it to
// "node_modules/**" and normalizeFilesEntry leaves the trailing slash on. Both
// strings were then run against a one-pattern list holding "node_modules" and
// again against one holding "node_modules/**", and all four combinations
// answered true.
//
// What #402 moved is the "./"-prefixed nested spelling and nothing else. Of
// those 3,780 entries, 240 changed answer against the pre-#402 code, which had
// no "./" handling at all; all 240 went false to true, all 240 carry a leading
// "./", and all 240 normalize to a path containing a "/", so not one of them is
// an entry naming a path at the root — "./node_modules/dep", "./node_modules/**"
// and "./.git/dep" are the shapes. That last property is the constraint the fix
// had to respect and it was measured, not reasoned: "node_modules",
// "/node_modules", "./node_modules" and "node_modules/" all stayed true, "a"
// and "./a" both stayed false, and so did every other control.
//
// The defect behind those 240 is worth recording, because a reader will
// otherwise reconstruct it wrongly from the fact that "./node_modules" always
// answered true. It did, but not by the old trims: the entry survived them
// unchanged and isHardReserved reached applyIgnorePatterns, which matches an
// unanchored separator-free pattern against filepath.Base — and
// Base("./node_modules") is "node_modules". Base("./node_modules/dep") is
// "dep", so the same coincidence gave nothing at depth, and
// "files": ["./node_modules/dep"] was dropped in silence where "node_modules/dep"
// said so out loud. Silence is the failure mode #321 is about. Only the warning
// was ever affected; the walk prunes node_modules either way.
//
// The other half of the pair has resolved a leading "./" since #346, and reads
// it through this same helper today, so "files": ["./node_modules"] selects what
// "node_modules" would and the walk's isHardReserved check refuses it like any
// other entry. Sharing the helper is what makes the two halves agree by one rule
// rather than by two routes that happened to meet.
// TestNamesHardReservedDotSlashAgreesWithUnprefixedForm is the standing
// measurement, TestNamesHardReservedResolvesOneDotSlashInNpmsOrder pins the
// slash-mixed spellings that make the sharing load-bearing, and
// TestPackWarnsWhenFilesNamesHardReserved carries the end-to-end rows.
//
// #399's four lockfiles are covered on every spelling, and by two branches
// rather than one. Measured on 2026-08-25 by calling this function on six
// spellings of each of the four names — the bare name, "/name", "name/",
// "./name", "sub/name" and "./sub/name" — first as the code stands and again
// with matchesIgnorePattern's basename branch disabled. All twenty-four are
// true today. Without the basename branch the four lockfiles answer alike: the
// first four spellings stay true, so normalizeFilesEntry plus the exact-match
// branch carry those by design, and the last two go false, so those ride the
// basename branch. It reaches further for these than it does for a directory
// entry, because a lockfile's basename is the pattern itself where
// filepath.Base("node_modules/dep") is "dep" — that branch is what covers a
// nested lockfile at all, with a "./" or without one.
// TestPackWarnsWhenFilesNamesHardReserved carries a row for it, on
// "./sub/package-lock.json".
func namesHardReserved(entry string) bool {
	normalized := normalizeFilesEntry(entry)
	return normalized != "" && isHardReserved(normalized)
}

// requireManifestPacked refuses a packed set with no package.json in it. It is
// #301's backstop rather than its mechanism: collectFiles force-includes the
// manifest past every selection rule, and this catches the routes that never
// reach a selection rule at all.
//
// One such route exists today and is reachable without contrivance. A symlinked
// package.json reads and parses fine — readPackageJSON goes through os.ReadFile,
// which follows the link — while collectFiles skips every symlink near the top of
// the walk, above any include check, so the force-include cannot put it back.
// Run and confirmed before the force-include existed and again after: Pack
// returned a nil error and a packed set of [index.js].
// TestPackFailsWhenManifestIsNotPacked is the fixture.
//
// An error where the sibling warnMainEntryNotPacked is a warning, which
// docs/adr/0004 rejected for its case and docs/adr/0005 accepts for this one:
// nothing else catches a missing manifest, because readPackageJSON reads from
// disk rather than from the packed set and therefore passes in exactly this
// case. It lives in Pack rather than in the publish command for the reason
// warnMainEntryNotPacked gives below — two of Pack's three callers pack with no
// validation at all — and an error is the right answer on all three.
//
// It runs after filterGitFiles so it sees the set the caller receives, not an
// earlier one. filterGitFiles cannot itself remove the manifest —
// isGitRelatedPath("package.json") is false — but the check is worth nothing if
// it is not asked of the final set.
func requireManifestPacked(files []*FileInfo) error {
	for _, f := range files {
		if f.RelPath == manifestFileName {
			return nil
		}
	}

	return fmt.Errorf("refusing to pack: no %s in the packed set; a package "+
		"without its manifest cannot be installed", manifestFileName)
}

// warnMainEntryNotPacked reports a manifest whose "main" is not in the packed
// set. It warns and returns; it never fails the pack.
//
// The check is on the finished set rather than on the selection branch, which
// costs one pass and catches every way the entry point can go missing: not on
// disk at all, dropped by an ignore pattern in non-whitelist mode, held back by
// defaultExcludes, or removed by filterGitFiles. Whatever the route, the
// published package does not load, and that is the fact worth saying.
//
// It lives in Pack rather than in the CLI because Pack is where every caller
// converges. internal/cli/publish.go:159 is only one of them:
// internal/cli/push.go:169 and push.go:194 pack with no validation at all, and
// publish's own --skip-validation turns its check off, so a warning wired into
// the publish path alone would leave `lnpm push` exactly as silent as #319
// found it. Putting it here also means the next caller gets it without knowing
// to ask.
//
// It found two on its first run. tests/fixtures/turborepo's "ui" and "utils"
// packages each declared main "dist/index.js" and shipped only src/index.js —
// there was no dist directory in the fixture at all — and
// TestPublishAllWorkspaces in tests/integration_test.go published them through
// RunPublish with skipValidation set, so nothing had ever reported it. (This
// sentence named tests/workspace_test.go until #365 checked it: that file's
// turborepo test only calls workspace.Detect and publishes nothing.) #365 built
// the two dist/index.js files rather than keeping a broken fixture as the
// warning's demonstration, and dropped the flag that had hidden them, so the
// warning is silent over that fixture now.
//
// It prints through ui.IconWarn() rather than an invented second icon set, so
// it reads as one warning idiom with internal/cli's. The icon helpers were
// unexported in internal/cli/output.go, and internal/cli imports this package,
// so calling them from here would have been an import cycle; #366 moved them to
// internal/ui, which imports nothing inside the module, and both packages call
// them there now. Every other warning pack prints goes through the same helper.
//
// The message names the manifest's spelling, not the normalized form, because
// that is the string the reader will search package.json for.
func warnMainEntryNotPacked(declared, mainEntry string, files []*FileInfo) {
	// An empty or root-escaping "main" is not reported. mainEntryPath returns ""
	// for both, so the two are indistinguishable here, and a package with no
	// "main" at all is the overwhelmingly common case — warning on it would
	// train the reader to ignore the line. A "main" that escapes the package is
	// a defect this does not catch; #319 is about the entry point going missing
	// from the tarball.
	if mainEntry == "" {
		return
	}

	for _, f := range files {
		if f.RelPath == mainEntry {
			return
		}
	}

	fmt.Printf("%s package.json \"main\" is %q, but no such file is in the "+
		"package; the published package will not load\n", ui.IconWarn(), declared)
}

// readPackageJSON reads and parses package.json
func readPackageJSON(dir string) (*PackageJSON, error) {
	path := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read package.json: %w", err)
	}

	var pkg PackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("failed to parse package.json: %w", err)
	}

	if pkg.Name == "" {
		return nil, fmt.Errorf("package.json must have a name field")
	}
	// Compose the name before validating it, and keep the composed form on the
	// struct. This is the single place #327's NFC rule is a transformation
	// rather than a refusal, and it is here because this is where a package name
	// enters lnpm's own state: the store directory, the database row, and
	// through the row the lock file and the consumer's package.json dependency
	// key all read pkg.Name off the value returned here, and the raw JSON string
	// is dropped. Composing anywhere later would leave two spellings of one name
	// in flight at once, which is the mismatch the rule exists to close.
	//
	// Before the validation, not after, so that what the rules are applied to is
	// the name lnpm will actually write - the 214-byte limit above all, which
	// composing can move in either direction.
	//
	// What this composes is the name lnpm indexes by, and deliberately not the
	// "name" field of the manifest that gets packed and stored. Those come apart
	// for a decomposed package: the store directory and the database row are
	// composed, and .lnpm/{name}/package.json still declares the decomposed
	// spelling, because the file is copied byte for byte out of the source. The
	// consumer's dependency key is composed too, so `npm ls` sees a name that
	// renders like the key it is under and is not equal to it.
	//
	// That gap is left open on purpose, and the reason is not that it is small.
	// The store is content-addressed: publish hashes the packed files, and the
	// entry's directory is named after that hash, so any rewrite of the manifest
	// has to happen before the hash is taken or the stored bytes stop describing
	// it. lnpm used to get that wrong - the lifecycle-script strip ran inside
	// store.Store, after the hash, and doctor had to carve the root manifest out
	// of verification because of it. That was #447, and it is fixed: every
	// rewrite now runs in PrepareManifest, in front of the hash.
	//
	// So composing the name here is not blocked on correctness any more, only on
	// worth. If it is ever wanted, PrepareManifest is where it goes, beside the
	// strip and the workspace-specifier resolution, and it would have to run for
	// every package rather than only the ones carrying a decomposed name. Doing
	// that to correct a field nothing resolves through is still a bad trade.
	pkg.Name = NormalizePackageName(pkg.Name)
	if err := ValidatePackageName(pkg.Name); err != nil {
		return nil, err
	}
	if pkg.Version == "" {
		return nil, fmt.Errorf("package.json must have a version field")
	}

	return &pkg, nil
}

// mainEntryPath normalizes the manifest's "main" into the form collectFiles
// compares against: slash-separated and relative to the package root. It returns
// "" when main names nothing this package can ship, which is the signal to
// force-include nothing.
//
// npm ships the file "main" names whatever the "files" whitelist says, and lnpm
// did not: Main was parsed and never consulted during selection, so main
// "lib/index.js" with files ["dist"] published [dist/a.js, package.json] and
// requiring the package failed on the one path the manifest advertises (#319).
//
// Normalization is filepath.ToSlash here and filepath.ToSlash on the relPath
// side, so the two are compared like with like on whichever platform is running.
// ToSlash is identity when Separator is '/' and otherwise replaces Separator
// with '/' (Go's stdlib internal/filepathlite/path.go — not this repo's
// internal/), and Separator is '\\' only on Windows. So on Windows both sides
// fold and main "lib\\index.js" matches the walked lib/index.js, while on Linux
// neither side folds and a file whose name genuinely contains a backslash still
// matches a main spelled the same way. TestMainEntryPath carries a backslash row
// whose expectation is chosen by runtime.GOOS, so the Windows CI job asserts the
// folding half and the Linux and macOS jobs assert the literal half; before that
// row existed, no fixture spelled main with a backslash and no job checked
// either.
//
// slashpath.Clean, not filepath.Clean: filepath.Clean rewrites "/" as "\" on
// Windows — the reason for this file's slashpath import in the first place —
// which would leave a native-separator string compared against a ToSlash'd
// relPath. Clean is also what collapses "./lib/index.js" and
// "lib/../lib/index.js" onto the "lib/index.js" the walk produces; both were run
// through slashpath.Clean and returned it.
//
// The "..", absolute and "." rejections defend no reachable caller today and are
// here on purpose. relPath is filepath.Rel(packageDir, path) for a path
// filepath.Walk yielded from inside packageDir, so it never begins with ".." and
// is never absolute, and "." is skipped as the root before any of this runs —
// meaning an escaping main already selects nothing purely because equality
// cannot match. TestPackMainEscapingPackageRootSelectsNothing pins the outcome
// with the escaping file actually present on disk. The guard is what keeps that
// true if the comparison in collectFiles is ever loosened from equality; a
// prefix comparison with "../evil.js" left in would reach outside the package.
//
// Absoluteness is asked twice because one question does not cover it. The
// HasPrefix "/" test catches the rooted spelling, which filepath.IsAbs rejects on
// Windows — there "\x" and "/x" are rooted but driveless, so IsAbs is false —
// and filepath.IsAbs catches the drive-absolute "C:/x", which HasPrefix cannot
// see. IsAbs is asked of the manifest's own spelling and is deliberately
// platform-dependent: "C:/x" really is an absolute path on Windows and really is
// a relative path on Linux, where "C:" is an ordinary directory name, so
// answering the same on both would be wrong on one of them. TestMainEntryPath
// pins the row per GOOS.
//
// A main naming a directory returns that directory's path and matches nothing:
// collectFiles skips directories before the whitelist branch, and the comparison
// is equality, so main "lib" does not widen the whitelist to lib's contents.
// Node resolves a directory main through its own index lookup; #319 puts that,
// and every other entry-point field, out of scope.
//
// A main naming a path that is not on disk is not this function's business:
// nothing matches it and pack proceeds. That is deliberate, and it does not
// weaken publish. Confirmed by reading the call order: internal/cli/publish.go
// calls validation.ValidatePackage (publish.go:140) before pack.Pack
// (publish.go:159), and internal/validation/validation.go:28-36 already refuses
// a manifest whose main is not on disk. So on the publish path pack never sees a
// missing main unless --skip-validation was passed, and #319's "warning, not
// abort" criterion is read here as "introduce no new abort inside pack" rather
// than as a reason to downgrade that existing check.
func mainEntryPath(main string) string {
	if main == "" {
		return ""
	}

	rel := slashpath.Clean(filepath.ToSlash(main))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") ||
		strings.HasPrefix(rel, "/") || filepath.IsAbs(main) {
		return ""
	}
	return rel
}

// collectFiles walks directory and returns files to include in a package.
//
// mainEntry is the manifest's "main" through mainEntryPath, or "" for none.
func collectFiles(packageDir string, filesField []string, mainEntry string) ([]*FileInfo, error) {
	var filesToHash []*FileInfo
	fileCount := 0

	// Load .npmignore or .gitignore patterns, from every directory on the way
	// down to each path rather than the package root alone.
	//
	// defaultExcludes rides along inside this chain as its shallowest layer, so
	// the two rank together against the "files" whitelist: both lose to it.
	// hardReservedExcludes is the list kept apart, checked in the walk below and
	// beaten by nothing.
	ignores := newIgnoreLoader(packageDir)

	// If files field is specified, use whitelist mode
	useWhitelist := len(filesField) > 0

	// Answers the defaultExcludes question for the two whitelist-mode arms
	// below, which cannot ask the ignore chain: consulting it there would let
	// the user's patterns drop a file the "files" field selected, which is the
	// thing #318 landed. Unused in non-whitelist mode, where the seeded chain
	// decides the whole tree.
	softExcluded := newDefaultExcludedTree()

	// First pass: collect all files
	err := filepath.Walk(packageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// A directory that could not be read, which the package's own rules
			// already exclude, is skipped rather than aborting the pack (#348).
			// Nothing under it would have been packed, so skipping publishes
			// nothing the maintainer did not ask for and docs/adr/0001's rule —
			// that a swallowed error which *widens* a publish is a bug — is not
			// engaged.
			//
			// Read docs/adr/0001 before changing the direction here, because the
			// obvious paraphrase of it is backwards. It is about direction and
			// says so outright: "The rule is about direction, not about
			// severity." Aborting on a directory that would have been packed is
			// the *narrowing* side, which the ADR explicitly leaves as "a
			// judgement call to make case by case" rather than a bug, and on
			// severity it ranks narrowing as the milder outcome — publishing too
			// few "is visible to the one running the command", where a wrongly
			// published path "is not recoverable by the person it surprises". So
			// the abort below is not the ADR enforcing itself. It is #348's
			// triage making the judgement call the ADR hands over, and the
			// reason is downstream rather than severity-ranked: a tarball
			// missing a file it should contain installs and then fails to load.
			//
			// The prune further down cannot do this, and that is why the check
			// is here. filepath.Walk reads a directory before it calls the
			// callback for it:
			//
			//	names, err := readDirNames(path)
			//	err1 := walkFn(path, info, err)
			//
			// so the read error and the callback arrive together and returning
			// filepath.SkipDir is already too late. Read from
			// $(go env GOROOT)/src/path/filepath/path.go on Go 1.26.7, which is
			// also where the facts below come from.
			//
			// The walk has three error shapes, not two, and info is valid on
			// exactly one of them:
			//
			//   - this one, a directory whose read failed. info came from the
			//     parent's lstat rather than from the failed read, which is what
			//     makes the directory question answerable at all.
			//   - a child whose lstat failed, `fileInfo, err := lstat(filename)`,
			//     where os.Lstat returns a nil FileInfo alongside its error.
			//   - the walk root's own lstat failing, which Walk reports as
			//     `fn(root, nil, err)` before walk is ever entered. Also nil.
			//
			// So the nil test below is load-bearing rather than defensive:
			// without it the second shape panics, run and confirmed by deleting
			// it. #409 then split the two nil shapes rather than aborting on
			// both: the walk root's own failure still aborts, and a child whose
			// lstat failed is put to the same predicate the directory shape
			// uses. What makes that sound for a path of unknown kind is below.
			//
			// The IsDir test beside it is the opposite case and is here on
			// purpose, in the sense mainEntryPath's comment above uses: it
			// defends no reachable caller today. Every error site listed above
			// passes either a directory's info or nil, so info != nil already
			// implies a directory — deleting IsDir turns no test red, run and
			// confirmed, which is the evidence for the claim rather than a gap
			// in the tests. It is what keeps the guard true if a later Go
			// release grows a fourth shape.
			//
			// The nil shape, decided by #409. A nil info names a path whose
			// *kind* is unknown — the second shape above is precisely the one
			// os.Lstat could not answer — so the question asked of it cannot be
			// "is this an excluded directory". It is asked as "is every path at
			// or under this one excluded", which is what
			// unreadableDirIsExcluded answers and which degenerates correctly
			// for a file, a file's subtree being itself. The decision is
			// therefore: the kind is not guessed and not needed, and the
			// predicate is restricted to the paths where it does not matter.
			//
			// That restriction is the strings.Contains(relPath, "/") test below,
			// and it is load-bearing rather than tidy. The whitelist switch has
			// four selecting arms and two are anchored to the package root — the
			// manifest arm tests relPath == manifestFileName exactly, and
			// isDefaultInclude returns false for any path carrying a "/" — while
			// in the other mode the prune below exempts the manifest by the same
			// exact test. unreadableDirIsExcluded asks neither, correctly, since
			// neither can select anything *inside* a directory. For a root-level
			// path they can, and there the predicate is wrong: measured on
			// 2026-08-25 against a package with "files": ["dist"],
			// unreadableDirIsExcluded is true for both "package.json" and
			// "README.md", and both of those pack. A root child's lstat failing
			// is not confined to an unenterable package root — a concurrent
			// unlink between readdir and lstat gives ENOENT, and a failing disk
			// gives EIO, just as well — but aborting is the right answer
			// regardless of the cause.
			// TestCollectFilesAbortsWhenARootChildCannotBeStatted pins it.
			//
			// This is what closes #409. A directory with its read bit but not
			// its execute bit (mode 0444) produces no error for itself — readdir
			// succeeds — and then fails the lstat of every child. So before #409
			// it aborted under a "files" field, where the walk descends into
			// excluded directories, and packed cleanly without one, where the
			// prune below stops it first; mode 0000 behaved alike in both, and
			// now so does 0444. The two modes still reach that agreement by
			// different routes and only the whitelist one prints a warning,
			// which TestPackSkipsAnUnenterableExcludedDirectory asserts per row.
			//
			// A file-level read error is still untouched by any of this. The
			// walk lstats an ordinary file successfully, so no error reaches
			// this branch at all; it is selected, and HashFile fails in the
			// second pass. TestPackAbortsOnAnUnreadableFile pins that.
			//
			// Returning nil is enough to continue, on both shapes, and it means
			// the same as filepath.SkipDir on each — but for two different
			// reasons, so the equivalence is stated rather than assumed. For a
			// directory whose read failed, this directory's own walk returns the
			// callback's verdict unchanged and the parent's loop moves to the
			// next sibling on a nil. For a child whose lstat failed, the loop is
			//
			//	fileInfo, err := lstat(filename)
			//	if err != nil {
			//		if err := walkFn(filename, fileInfo, err); err != nil && err != SkipDir {
			//			return err
			//		}
			//	} else {
			//		err = walk(filename, fileInfo, walkFn)
			//		...
			//	}
			//
			// so on the error side nil and filepath.SkipDir are exactly the two
			// values that do not stop the walk, and every other value aborts it.
			// SkipDir's other meaning — the one that would silently drop the
			// siblings after it — lives in the else branch, where
			// `if !fileInfo.IsDir() || err != SkipDir { return err }` returns
			// that SkipDir out of the enclosing directory's own walk() call,
			// truncating whatever siblings after the file it had not yet
			// visited. One level up, the enclosing directory's own fileInfo
			// is itself a directory, so the same check is false there and the
			// parent's loop continues with its own remaining entries — the
			// whole walk aborts only when the file is a direct child of the
			// walk root, and even then Walk swallows the returned SkipDir and
			// reports nil. That branch is unreachable from here. nil is
			// returned because it says exactly what is meant. Note also that
			// the walk recurses only in that else branch, so it never
			// descends into a child it could not lstat: the subtree is
			// skipped either way.
			//
			// The relPath != "." guard is not decoration. The package root's own
			// read failure has to abort — Walk hands it to the callback the same
			// way — and under a "files" field the predicate below matches each
			// entry against the root's one segment, the literal ".", which an
			// ordinary entry such as "dist" does not match. It would call the
			// package root excluded and the walk would then return no error and
			// no files.
			// TestCollectFilesAbortsWhenThePackageRootCannotBeRead pins it. It
			// guards the directory shape only; the nil shape's "/" test is
			// stricter and refuses "." already.
			//
			// Every error is treated alike rather than only a permission error.
			// An excluded directory contributes nothing to the package whatever
			// stopped it being read, so narrowing to os.ErrPermission would only
			// abort on the rarer causes for no gain.
			if rel, relErr := filepath.Rel(packageDir, path); relErr == nil {
				relPath := filepath.ToSlash(rel)

				// Which paths the predicate may be asked about, per error
				// shape. Neither guard is decoration; both are argued above.
				answerable := false
				if info != nil && info.IsDir() {
					answerable = relPath != "."
				} else if info == nil {
					answerable = strings.Contains(relPath, "/")
				}

				// This fires once per child, not once per directory: mode 0000
				// fails the directory's own readdir, so one line names it, but
				// mode 0444 fails the lstat of every name readdir returned, so a
				// node_modules full of entries prints one line per entry. That is
				// verbose rather than wrong — every line still names a real path
				// this predicate excluded — so no deduplication is added here.
				if answerable && unreadableDirIsExcluded(relPath, ignores, filesField, mainEntry) {
					fmt.Printf("%s could not read %q, which this package excludes; skipping it (%v)\n", ui.IconWarn(), relPath, err)
					return nil
				}
			}
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(packageDir, path)
		if err != nil {
			return err
		}

		// Skip root directory
		if relPath == "." {
			return nil
		}

		// Skip symlinks. Following them would dereference into the store and
		// could pull in files outside the package (e.g. a link to ~/.ssh).
		// npm likewise does not follow symlinks out of the package.
		if info.Mode()&os.ModeSymlink != 0 {
			debug.Logf("pack: skipping symlink %s", relPath)
			return nil
		}

		// Normalize path separators for pattern matching
		relPath = filepath.ToSlash(relPath)

		// The hard-reserved set is checked on its own and first, above every
		// user-driven selection rule, because nothing overrides it — not a user
		// "!" negation, not the "files" field, not "main".
		//
		// defaultExcludes is deliberately not asked here. It is seeded into the
		// ignore chain instead (ignoreLoader.excludes), which is what lets a
		// "files" entry or an "!" negation outrank it. #321.
		if isHardReserved(relPath) {
			if info.IsDir() {
				// Nothing inside a hard-reserved directory can be wanted, so
				// the walk never descends. This is what keeps a large
				// node_modules off the walk entirely.
				return filepath.SkipDir
			}
			return nil
		}

		// The package root's own manifest is not the package's to exclude
		// (#301). An .npmignore line reading "package.json" dropped it, and so
		// did a "files" field that did not list it, in both cases producing a
		// tarball nothing downstream can read a name or version out of.
		// docs/adr/0005 records the decision: why this ships a file the
		// maintainer explicitly excluded, which is the direction docs/adr/0001
		// calls a bug, and why it is narrowed to the manifest rather than
		// widened to all of defaultIncludes.
		//
		// The flag is consulted at both of the two sites that dropped it. The
		// prune just below decides the whole tree when there is no whitelist;
		// the isDefaultInclude arm of the switch further down re-consults the
		// same patterns when there is one. Fixing one leaves the other
		// reachable — TestPackManifestSurvivesIgnorePatterns carries a row for
		// each, and its first two rows and its third fail separately.
		//
		// Exact equality, not a basename test, so a sub/package.json is another
		// package's manifest or a fixture and an ignore pattern still drops it.
		// TestPackManifestForceIncludeIsRootAnchored pins it.
		//
		// This sits below the isHardReserved check above, never above it: that
		// list is a guard lnpm applies on the user's behalf, and a guard
		// anything can step around is not a guard (docs/adr/0004 for "main",
		// docs/adr/0005 for the manifest). No hardReservedExcludes entry
		// matches package.json today, so no ordinary fixture can see that
		// placement — TestPackManifestCannotDefeatHardReserved puts one there
		// for its own duration and goes red if this is hoisted.
		//
		// It sits *above* defaultExcludes, which is evaluated inside the ignore
		// chain further down. That is a real precedence move and an inert one:
		// no entry in either built-in list matches package.json, so no package
		// can observe it. It is not an exemption carved out for the manifest —
		// the manifest simply does not carry the explicit isDefaultExcluded
		// check the mainEntry arm below does, because unlike a "main" it names
		// nothing either list can plausibly grow. docs/adr/0005 records it.
		isManifest := relPath == manifestFileName

		// The user's ignore patterns decide the whole tree only when there is no
		// whitelist. With one they drop nothing here, and are consulted again
		// further down for default includes alone — a "files" entry may be a
		// glob, so which paths under an ignored directory it selects cannot be
		// known without descending into it and asking isIncluded per file.
		// Walking into ignored directories costs something in whitelist mode;
		// publishing an empty package because .gitignore held the build output
		// costs more.
		if !useWhitelist && !isManifest {
			if ignores.excludes(relPath) {
				if info.IsDir() {
					// Pruning is the one place last-match-wins does not hold
					// end to end: the walk never descends, so a later "!"
					// pattern inside this directory is never consulted.
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Skip directories (we only care about files)
		if info.IsDir() {
			return nil
		}

		// Check if included
		if useWhitelist {
			switch {
			case isManifest:
				// Ahead of the isDefaultInclude arm below on purpose. That arm
				// also matches "package.json" — it is defaultIncludes' first
				// entry — and then re-consults the user's ignore patterns and
				// drops it, which is the whitelist-mode half of #301.
			case mainEntry != "" && relPath == mainEntry:
				// The manifest's entry point, which ships whatever the
				// whitelist says (#319).
				//
				// Decision: main is exempt from the user's ignore patterns too,
				// not from the whitelist alone. So this ships a file the
				// maintainer explicitly excluded, which is the direction
				// docs/adr/0001 calls a bug. The reasoning and the rejected
				// alternatives are in
				// docs/adr/0004-the-main-entry-point-outranks-the-maintainers-own-ignore-patterns.md;
				// the short form is that the other reading leaves #319's failure
				// reachable *and* invisible, because validation.ValidatePackage
				// only stats main on disk, so a main that exists but is not
				// packed passes validation and publish reports success on a
				// package that does not load.
				//
				// The boundary in full: main beats the "files" whitelist and
				// the user's ignore patterns, and loses to both built-in
				// exclusion lists. docs/adr/0004 states that boundary and #321
				// did not move it. The two halves are enforced differently, and
				// the difference is the thing to read carefully:
				//
				//   - hardReservedExcludes holds structurally. isHardReserved is
				//     evaluated in the walk above and returns early, so .npmrc
				//     named as "main" never reaches this switch.
				//     TestPackMainCannotDefeatHardReserved pins it and goes red
				//     if the force-include is hoisted above that check.
				//   - defaultExcludes needs the explicit check below, and used
				//     not to. Before #321 it was evaluated in the same early
				//     return and this arm was unreachable for a .env; now it
				//     lives inside ignoreLoader.excludes, which this arm
				//     deliberately does not call — that is exactly how a "files"
				//     entry overrides it. So the one line below is the whole
				//     reason main does not inherit that override.
				//     TestPackMainCannotDefeatDefaultExcludes goes red when it is
				//     deleted; run and confirmed by deleting it, not by reading.
				//
				// The two lists are not treated alike here because they fail in
				// opposite directions. Letting main override defaultExcludes
				// would publish .env, a log or a *.tgz on the strength of one
				// manifest field, which fails toward leaking; refusing it packs
				// nothing and warnMainEntryNotPacked says why, which fails toward
				// a message. #321 asked to make defaultExcludes overridable by
				// the maintainer's *selection* rules, and left main where
				// docs/adr/0004 put it.
				//
				// The isIncludedDirectly half is not "main overrides after
				// all". It is the "files" field speaking for this path, and it
				// has to be asked here because this arm runs first: without it,
				// a manifest with "files": [".env.example"] and
				// main ".env.example" returned right here and failed #321's own
				// first acceptance criterion. main alone is still not consent —
				// main ".env" with "files": ["dist"] is refused, and
				// TestPackMainCannotDefeatDefaultExcludes pins that half.
				//
				// Reordering the arms would also fix that case and must not be
				// done: the arm order carries #319's rule that main outranks the
				// whitelist.
				if softExcluded.covers(relPath) && !isIncludedDirectly(relPath, filesField) {
					return nil
				}

				// This sits inside the whitelist branch on purpose. A package
				// with no "files" field is left exactly as it was — there the
				// user's patterns decide the whole tree, main included.
				// TestPackMainRespectsIgnorePatternsWithoutFilesWhitelist pins
				// that, and fails if this case is hoisted above the branch.
			case isIncluded(relPath, filesField):
				// npm documents that a file included with the "files" field
				// cannot be excluded through .npmignore or .gitignore, so the
				// user's ignore patterns are not consulted at all here. Not
				// consulting them is also what carries #321's override of
				// defaultExcludes, since that list rides inside the same chain.
				//
				// The override is narrower than the inclusion, and this is the
				// line that narrows it. A "files" entry overrides the list only
				// for a path it names — "files": [".env.example"] publishes the
				// template — and never for a path it merely contains, so
				// "files": ["dist"] does not publish dist/.env, dist/app.log or
				// dist/pkg.tgz. Naming a build directory is not a statement
				// about whatever secrets landed inside it, and without this the
				// commonest "files" field in existence would start shipping
				// them.
				//
				// covers rather than isDefaultExcluded, so the same holds one
				// level up: "files": ["src"] does not publish src/.env/config
				// either. See defaultExcludedTree for why the ancestor question
				// exists at all and what it costs the walk.
				//
				// The test runs only for paths defaultExcludes covers. Every
				// other path keeps isIncluded's containment reach exactly as
				// #318 landed it: dist/a.js still ships for "files": ["dist"]
				// past a .gitignore naming dist.
				//
				// This is deliberately not npm's behaviour and the divergence is
				// wider than it looks. npm does not ignore .env at all, so
				// `npm publish` with "files": ["dist"] publishes dist/.env
				// outright. lnpm withholds it by default and, since #321,
				// publishes it when the maintainer names that path. Neither
				// half of that matches npm; the README divergence list says so.
				if softExcluded.covers(relPath) && !isIncludedDirectly(relPath, filesField) {
					return nil
				}
			case isDefaultInclude(relPath):
				// A default include arrives on its own steam rather than
				// through the "files" field, so an ignore pattern still drops
				// it. Nothing skipped the check above for this path.
				if ignores.excludes(relPath) {
					return nil
				}
			default:
				return nil
			}
		}

		fileCount++
		if fileCount%1000 == 0 {
			fmt.Printf("\r  Scanning... %d files", fileCount)
		}

		// Need to hash this file
		filesToHash = append(filesToHash, &FileInfo{
			Path:    path,
			RelPath: relPath,
			Size:    info.Size(),
			Mode:    info.Mode(),
			ModTime: info.ModTime().UnixNano(),
		})

		return nil
	})

	if fileCount >= 1000 {
		fmt.Printf("\r                                        \r") // Clear progress line
	}

	if err != nil {
		return nil, err
	}

	// Second pass: hash files in parallel using ants pool
	if len(filesToHash) > 0 {
		var wg sync.WaitGroup
		wg.Add(len(filesToHash))

		// Capture the first hashing error instead of swallowing it — an
		// empty ContentHash would otherwise corrupt the package content hash.
		var hashErrMu sync.Mutex
		var hashErr error

		pool, err := ants.NewPoolWithFunc(runtime.NumCPU()*2, func(i interface{}) {
			defer wg.Done()
			file := i.(*FileInfo)
			hash, err := HashFile(file.Path)
			if err != nil {
				hashErrMu.Lock()
				if hashErr == nil {
					hashErr = fmt.Errorf("failed to hash %s: %w", file.RelPath, err)
				}
				hashErrMu.Unlock()
				return
			}
			file.ContentHash = hash
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create worker pool: %w", err)
		}
		defer pool.Release()

		// Submit all files to pool
		for _, file := range filesToHash {
			if err := pool.Invoke(file); err != nil {
				return nil, fmt.Errorf("failed to hash files: %w", err)
			}
		}

		// Wait for all workers to complete before proceeding
		wg.Wait()

		if hashErr != nil {
			return nil, hashErr
		}
	}

	return filesToHash, nil
}

// unreadableDirIsExcluded reports whether a path the walk could not read can be
// skipped: whether the package's own rules already put every path at or under it
// outside the pack. relPath is slash-separated, relative to the package root,
// and never "." — the package root itself is nothing's exclusion and its failure
// to read is a real abort.
//
// The name says "Dir" because that is the shape #348 wrote it for, and the
// question it asks is about a subtree. #409 gave it a second caller, a child
// whose lstat failed, whose kind is unknown because os.Lstat is exactly what
// could not answer. The subtree question survives that: a file's subtree is the
// file. What does not survive is the root-anchored half of the switch below, so
// that caller passes only a relPath carrying a "/" — collectFiles' walk callback
// argues why in full.
//
// It asks a different question per mode because the modes exclude by different
// rules, and asking one rule in both would be wrong twice over.
//
// Without a "files" field the ignore chain decides the whole tree, so this asks
// exactly what the walk's own prune asks. A directory the chain excludes is one
// the prune would have refused to descend into.
//
// With one, the chain decides nothing (#318) and the whitelist switch answers
// instead. It has four selecting arms and two of them are anchored to the
// package root, so neither can select anything inside a directory: the manifest
// arm tests relPath == manifestFileName exactly, and isDefaultInclude rejects any
// path carrying a "/". That leaves the mainEntry arm, which ships "main"
// whatever the whitelist says (#319) and whatever the ignore files say
// (docs/adr/0004), and the isIncluded arm, which is the "files" entries
// themselves.
//
// hardReservedExcludes is asked first and in both modes, because it is the one
// rule no mode overrides. It is not covered by either branch below: node_modules
// is on that list and not in defaultExcludes, so a package with no .gitignore
// naming it gets no answer from the ignore chain.
func unreadableDirIsExcluded(relPath string, ignores *ignoreLoader, filesField []string, mainEntry string) bool {
	if isHardReserved(relPath) {
		return true
	}

	if len(filesField) == 0 {
		return ignores.excludes(relPath)
	}

	if mainEntry == relPath || strings.HasPrefix(mainEntry, relPath+"/") {
		return false
	}
	return !filesFieldMayReach(relPath, filesField)
}

// filesFieldMayReach reports whether any "files" entry could select a path at or
// under dir, both given slash-separated and relative to the package root.
//
// It is sound in the abort direction and only in that direction: it may answer
// true for an entry that would in fact have selected nothing there, and it must
// never answer false for one that could have selected something. Its one caller
// skips a directory on a false, so a wrong false publishes a package missing a
// file the maintainer asked for, while a wrong true fails the pack with the
// error naming the directory. When it cannot decide, it answers true.
//
// matchFilesField is not this predicate and cannot stand in for it, which is the
// trap this function exists to avoid: matchFilesField("coverage",
// ["coverage/report.html"]) is filesMatchNone — run and confirmed — because the
// entry names neither coverage nor an ancestor of it, and the entry plainly
// selects into it all the same. It answers "does this entry reach this path";
// the question here is "could this entry reach past this path".
//
// The rule is a segment-by-segment walk of the entry against the directory:
//
//   - An entry that runs out of segments while every one matched named an
//     ancestor of dir, so it contains the whole subtree. Reaches.
//   - An entry whose segments all matched, with dir also exhausted, named dir
//     itself or describes paths below it. Reaches.
//   - A "**" segment stands for any number of segments, including dir's
//     remaining ones, so whatever follows it describes a path under dir.
//     Reaches, without looking further.
//   - Otherwise the segments have to match one for one, and the first that does
//     not ends it: no path under dir can be selected through this entry.
//
// The soundness argument is that only the last bullet ever returns false, and it
// returns false only after a segment of the entry failed to match the
// corresponding segment of dir. Any path at or under dir carries dir's segments
// as its own prefix, so that same segment would fail against any such path too.
// The three reaching bullets are the conservative side and are allowed to be
// generous: "coverage/**/x.js" is called reaching for dir "coverage" whether or
// not an x.js is there to find, which costs an abort naming a directory that
// could not be read.
//
// Segments are compared with doublestar, the engine matchFilesField itself globs
// with since #350, and with a literal string compare first — that is the escape
// hatch docs/adr/0003 leans on and matchFilesField's own literal branches carry:
// doublestar.Match("weird{a,b}", "weird{a,b}") is false because braces expand —
// run and confirmed — and the entry still names that directory. A segment
// doublestar will not parse is undecidable here and reaches.
//
// That undecidable branch is what keeps the split honest against a brace, which
// is the one construct that can span a separator: doublestar.Match with
// "a{b,c/d}/x.js" matches "ac/d/x.js", run and confirmed, so splitting the entry
// on "/" cuts the brace across segments — three of them for that entry, not two
// halves. The segment holding the opening "{" is unbalanced and doublestar
// rejects it — Match("a{b,c", "ac") is a syntax error, also run — so the entry
// reaches rather than being decided from a segment that no longer means what it
// did. That segment is the first one the walk reaches at or after the brace, and
// every segment ahead of the brace is intact, so a genuine mismatch there still
// ends the walk.
//
// The entry is normalized through normalizeFilesEntry, the same call
// matchFilesField makes, so the two read the same string by construction rather
// than by two copies staying in step. The trailing-slash and degenerate
// spellings then fall
// out of the walk rather than being special-cased: "" reaches everything,
// "dist/*/" fails at its first segment against a directory named coverage, and
// "coverage/*/" is called reaching for "coverage" though matchFilesField selects
// nothing for it — run and confirmed, and generous in the allowed direction.
func filesFieldMayReach(dir string, patterns []string) bool {
	dirSegments := strings.Split(dir, "/")

	for _, pattern := range patterns {
		pattern = normalizeFilesEntry(pattern)
		if pattern == "" {
			// The degenerate entry ships everything, matchFilesField's first
			// branch. Splitting it would give one empty segment, which matches
			// no directory name.
			return true
		}

		if segmentsReach(strings.Split(pattern, "/"), dirSegments) {
			return true
		}
	}
	return false
}

// segmentsReach is filesFieldMayReach for one entry, already normalized and
// split. Its rule and the argument for it are in that function's comment.
func segmentsReach(patternSegments, dirSegments []string) bool {
	for i, dirSegment := range dirSegments {
		if i == len(patternSegments) {
			// The entry named an ancestor of the directory.
			return true
		}

		patternSegment := patternSegments[i]
		if patternSegment == "**" {
			return true
		}
		if patternSegment == dirSegment {
			continue
		}

		matched, err := doublestar.Match(patternSegment, dirSegment)
		if err != nil {
			// An entry the glob engine will not parse cannot be decided.
			return true
		}
		if !matched {
			return false
		}
	}

	// Every segment of the directory was matched, so the entry named it or
	// describes paths below it.
	return true
}

// ignoreScope is one ignore file's patterns together with the directory they
// resolve against, as a slash-separated path relative to the package root ("."
// for the package root itself). The directory is what a leading "/" in a pattern
// anchors to, so "/lib/gen.js" in src/.npmignore names src/lib/gen.js and says
// nothing about lib/gen.js at the root.
type ignoreScope struct {
	dir      string
	patterns []string
}

// ignoreLoader answers, for one path, what the ignore files above it say — the
// package root's, then every directory down to the one the path sits in.
//
// filepath.Walk has no "leaving a directory" callback, so patterns are resolved
// per path from the root down rather than pushed and popped on a stack, and the
// per-directory answer is cached. The cache is not an optimization to taste: it
// is what keeps a package of N files D directories deep at one read per
// directory instead of N*D — see TestIgnoreLoaderReadsEachDirectoryOnce.
type ignoreLoader struct {
	// read returns the patterns governing one directory, given as a
	// slash-separated path relative to the package root. It is a field so that
	// a test can count the reads; production always gets loadIgnorePatterns.
	read  func(dir string) []string
	cache map[string][]ignoreScope
	// dirVerdicts memoizes excludes for directories. Without it the "is this
	// scope's own directory excluded" check below is exponential in depth: the
	// check at depth D re-asks every shallower depth, each of which re-asks all
	// of its own. Memoized, each directory is decided once per pack.
	dirVerdicts map[string]bool
}

func newIgnoreLoader(root string) *ignoreLoader {
	return &ignoreLoader{
		read: func(dir string) []string {
			return loadIgnorePatterns(filepath.Join(root, filepath.FromSlash(dir)))
		},
		cache:       make(map[string][]ignoreScope),
		dirVerdicts: make(map[string]bool),
	}
}

// scopes returns the ignore files governing the contents of dir, shallowest
// first. An ignore file never governs the directory holding it — git cannot
// exclude src through src/.gitignore — so a scope for dir applies to the paths
// inside it, which is exactly what excludes asks for.
func (l *ignoreLoader) scopes(dir string) []ignoreScope {
	if scopes, ok := l.cache[dir]; ok {
		return scopes
	}

	var scopes []ignoreScope
	if dir != "." {
		scopes = l.scopes(slashpath.Dir(dir))
	}
	if patterns := l.read(dir); patterns != nil {
		// Copy rather than append in place: the parent's slice is cached and
		// shared with every sibling directory, so appending to it could
		// overwrite one sibling's scope with another's in the backing array.
		scopes = append(append([]ignoreScope{}, scopes...), ignoreScope{dir: dir, patterns: patterns})
	}

	l.cache[dir] = scopes
	return scopes
}

// excludes reports whether the ignore files governing relPath exclude it.
// relPath is slash-separated and relative to the package root.
//
// Every applicable file is evaluated, shallowest first, carrying the running
// last-match-wins verdict from one to the next. So a deeper file has the final
// say, as it does in git: a root "*.txt" plus "!keep.txt" in src/.npmignore
// publishes src/keep.txt. Each file's patterns are matched against the path
// relative to that file's own directory, not the package root.
//
// A scope whose own directory is excluded stops the whole thing. git never
// descends into an ignored directory, so an ignore file sitting in one is never
// read and cannot re-include anything out of it. Enforcing that here rather than
// leaving it to the walk's directory pruning is what keeps the two walk modes
// agreeing: with a "files" whitelist the walk descends into ignored directories
// on purpose (#349), so pruning is not there to do it.
func (l *ignoreLoader) excludes(relPath string) bool {
	// defaultExcludes is the chain's shallowest layer: a verdict already in
	// hand before the package root's own ignore file speaks. Seeding it here
	// rather than deciding it in the walk is what gives an "!" negation the last
	// word over it, by the same last-match-wins rule that already lets a deeper
	// ignore file overrule a shallower one. #321.
	//
	// A "files" entry outranks it by a different route and not through this
	// function at all: the isIncluded arm of collectFiles' whitelist switch never
	// calls excludes. Note that the mainEntry arm does not call it either, which
	// is why that arm has to ask defaultExcludedTree.covers itself — not calling
	// excludes is what an override *is* here, and "main" is not meant to be one.
	//
	// Note this reaches dirExcluded too, so a directory named by an overridable
	// default is still pruned in non-whitelist mode exactly as it was before the
	// split.
	excluded := isDefaultExcluded(relPath)
	for _, scope := range l.scopes(slashpath.Dir(relPath)) {
		if scope.dir != "." && l.dirExcluded(scope.dir) {
			return true
		}

		subPath := relPath
		if scope.dir != "." {
			subPath = strings.TrimPrefix(relPath, scope.dir+"/")
		}
		excluded = applyIgnorePatterns(subPath, scope.patterns, excluded)
	}
	return excluded
}

// defaultExcludedTree answers whether defaultExcludes covers a path or any
// directory above it, memoizing the directory half.
//
// The ancestor question exists because #321 moved defaultExcludes out of the
// walk. Before it, the walk asked the combined list per path and returned
// filepath.SkipDir for a matching directory in *both* modes, so a directory
// named .env.d or app.log was pruned and nothing under it could be selected.
// Afterwards the list reaches a path only through ignoreLoader.excludes — which
// whitelist mode skips on purpose — or through collectFiles' per-file checks,
// which ask about the file's own path. That left "files": ["src"] publishing
// src/.env/config: the file's own name is not default-excluded, and isIncluded
// reached it by containment. The direct-match rule was defeated one level up.
//
// So the rule the two whitelist-mode call sites apply is: refuse a path whose
// own name or any ancestor directory is default-excluded, unless a "files" entry
// names that path directly.
//
// Two consequences follow and both are deliberate.
//
// Whitelist mode cannot prune a default-excluded directory. Whether a "files"
// entry selects something inside one cannot be known without descending and
// asking per file — a "files" entry may be a glob — so the walk descends and
// pays for it. That is the same trade collectFiles already makes one block above
// for ignored directories, and for the same reason: walking costs something,
// publishing the wrong set costs more. Non-whitelist mode still prunes, through
// the seeded chain in excludes, and is untouched.
//
// A directly named path under a default-excluded directory now packs where the
// pre-#321 walk refused it — it pruned the directory, so nothing inside could be
// selected: "files": ["src/.env/config"] ships that file, and
// "files": [".env.d/keep.js"] ships that one. This is a narrow widening and it
// follows from the rule that a direct name is consent — the same rule that makes
// "files": ["dist/.env"] pack. TestPackFilesEntryOverridesDefaultExcludesOnlyByDirectMatch
// carries a row for each so neither reads as an accident.
//
// It does not reuse ignoreLoader.dirVerdicts, which memoizes the same shape —
// one verdict per directory, resolved up towards the root. That cache answers
// ignoreLoader.excludes, which folds in the
// user's .npmignore and .gitignore patterns — and in whitelist mode a "files"
// entry outranks those (#318), so consulting it here would drop files the
// whitelist asked for. The two questions differ by exactly the thing #318
// landed, so they need separate answers.
type defaultExcludedTree struct {
	dirVerdicts map[string]bool
}

func newDefaultExcludedTree() *defaultExcludedTree {
	return &defaultExcludedTree{dirVerdicts: make(map[string]bool)}
}

// covers reports whether relPath or any directory above it is default-excluded.
// relPath is slash-separated and relative to the package root.
func (t *defaultExcludedTree) covers(relPath string) bool {
	return isDefaultExcluded(relPath) || t.dirCovers(slashpath.Dir(relPath))
}

// dirCovers is covers for a directory, memoized. It terminates because
// slashpath.Dir walks strictly up and returns "." for a single-segment path,
// which is the package root and is never excluded.
func (t *defaultExcludedTree) dirCovers(dir string) bool {
	if dir == "." || dir == "/" || dir == "" {
		return false
	}

	if verdict, ok := t.dirVerdicts[dir]; ok {
		return verdict
	}

	verdict := isDefaultExcluded(dir) || t.dirCovers(slashpath.Dir(dir))
	t.dirVerdicts[dir] = verdict
	return verdict
}

// dirExcluded is excludes memoized for a directory. It terminates because the
// scopes governing dir are drawn from strictly shallower directories, so the
// recursion walks up towards the package root and stops there.
func (l *ignoreLoader) dirExcluded(dir string) bool {
	if verdict, ok := l.dirVerdicts[dir]; ok {
		return verdict
	}

	verdict := l.excludes(dir)
	l.dirVerdicts[dir] = verdict
	return verdict
}

// loadIgnorePatterns reads .npmignore or .gitignore
func loadIgnorePatterns(dir string) []string {
	var patterns []string

	// Try .npmignore first
	npmignorePath := filepath.Join(dir, ".npmignore")
	if patterns = readIgnoreFile(npmignorePath); patterns != nil {
		return patterns
	}

	// Fall back to .gitignore
	gitignorePath := filepath.Join(dir, ".gitignore")
	if patterns = readIgnoreFile(gitignorePath); patterns != nil {
		return patterns
	}

	return nil
}

// readIgnoreFile reads an ignore file and returns patterns
func readIgnoreFile(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var patterns []string
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}

	return patterns
}

// isExcluded checks if a path matches any exclude pattern.
//
// It follows gitignore semantics: every pattern is evaluated and the last one
// that matches decides the outcome, so a later "!pattern" re-includes a path an
// earlier pattern excluded. collectFiles evaluates hardReservedExcludes in a call
// of their own and above every selection rule, which is what keeps a user
// negation from re-including node_modules or .npmrc. defaultExcludes gets no
// such treatment: ignoreLoader.excludes seeds it into the chain, precisely so a
// negation can re-include .env.example (#321).
func isExcluded(relPath string, patterns []string) bool {
	return applyIgnorePatterns(relPath, patterns, false)
}

// isHardReserved reports whether the hard-reserved set covers relPath, matching
// case-insensitively for the reasons isDefaultExcluded gives below — the fold
// arrived (#317) while the two sets were still one list, and splitting them
// keeps it on both halves rather than choosing one.
//
// This is the set with no override. collectFiles asks it first in the walk,
// above every user-driven selection rule, and Pack warns when a "files" entry
// names a path it covers (warnHardReservedFilesEntry).
func isHardReserved(relPath string) bool {
	return isExcluded(strings.ToLower(relPath), lowerHardReservedExcludes)
}

// isDefaultExcluded reports whether the overridable half of the built-in
// exclusion set covers relPath, matching case-insensitively.
//
// It is not the whole guard and has not been since #321 split the list: the half
// nothing can override is isHardReserved above. This half is seeded into the
// ignore chain by ignoreLoader.excludes, so a user "!" negation outranks it, and
// the isIncluded arm of collectFiles' whitelist switch never consults the chain
// at all, so a "files" entry outranks it too.
//
// Neither arm of that switch consults the chain, in fact, so both call back into
// this list explicitly — the isIncluded arm through defaultExcludedTree.covers,
// which also asks about ancestor directories, and the mainEntry arm through the
// same helper. Two guard sites, not one. The mainEntry call is what keeps "main"
// from inheriting the override (docs/adr/0004 has the why); the isIncluded call
// is what keeps the override to directly named paths. Deleting either leaves the
// other green, so a revert check has to name which one it is testing —
// docs/agents/verification-discipline.md spells the two experiments out.
//
// The reasoning below is the fold's, so it covers both halves and isHardReserved
// points here rather than restating it.
//
// A built-in exclusion is a guard lnpm applies on the user's behalf rather than
// a preference the user expressed, and a guard that can be stepped around by
// holding shift is not a guard: matched case-sensitively, ".ENV", ".Env.local"
// and ".NPMRC" all shipped while ".env" and ".npmrc" did not. On macOS and
// Windows those name the same file, so whether a secret was protected came down
// to how the developer typed it (#317). Folding here can only widen the set, and
// every name it newly catches is one the guard already meant to keep out.
//
// The user's own .npmignore and .gitignore patterns keep matching
// case-sensitively, which is why this folds the path itself instead of teaching
// applyIgnorePatterns or matchesIgnorePattern a case-insensitive mode — both are
// shared with the user-pattern path. Note this is a divergence from git rather
// than a match to it: git folds its own ignore matching whenever core.ignorecase
// is set, which git sets at init and clone on a case-insensitive filesystem, so
// on macOS and Windows a default git repository already ignores SECRET.TXT for a
// "secret.txt" pattern. Three reasons to diverge anyway. The scope is the issue's
// (#317 changes the built-in guard, not the user's rules, and moving those is a
// decision of its own). The two fail in opposite directions: folding the guard
// only ever withholds more, and every name it newly catches is one the guard
// already meant to keep out, where folding the user's patterns would drop files
// the author never asked to drop. And lnpm has no core.ignorecase of its own —
// no per-project signal saying whether this filesystem folds — so the choice
// would have to be made unconditionally, which would then diverge from git on
// Linux instead.
//
// The fold is ASCII-adequate rather than exhaustive: strings.ToLower applies
// Unicode's simple lowercase mapping, not full case folding, so a name equal to
// an entry only under a full fold is not caught — "ß" does not become "ss", and
// the Turkish dotless "ı" stays itself rather than becoming "i". Every entry is
// ASCII, so this costs nothing today, and it matches how isDefaultInclude and
// isGitRelatedPath already compare.
func isDefaultExcluded(relPath string) bool {
	return isExcluded(strings.ToLower(relPath), lowerDefaultExcludes)
}

// applyIgnorePatterns is isExcluded with the running verdict passed in, so one
// ignore file's patterns can pick up where a shallower file's left off.
// ignoreLoader.excludes chains the calls; anything asking about a single flat
// pattern list wants isExcluded.
func applyIgnorePatterns(relPath string, patterns []string, excluded bool) bool {
	baseName := filepath.Base(relPath)

	for _, pattern := range patterns {
		// A leading "!" negates: a match un-excludes the path.
		negated := strings.HasPrefix(pattern, "!")
		if negated {
			pattern = pattern[1:]
		}

		// A leading "/" anchors the pattern to the directory of the ignore file
		// it came from, so it is matched against the path relative to that
		// directory only, never the basename. For a root ignore file that is
		// the package root; for src/.npmignore it is src, and ignoreLoader.
		// excludes has already trimmed relPath to suit.
		anchored := strings.HasPrefix(pattern, "/")
		if anchored {
			pattern = pattern[1:]
		}

		// A trailing "/" marks a directory pattern: it matches the directory
		// itself and everything under it. A negated one matches the directory
		// only, never its contents, because git cannot re-include a file whose
		// parent directory is excluded.
		//
		// Limitation: git's trailing slash matches directories only, but
		// isExcluded receives no directory signal, so a plain *file* named
		// "dist" is also matched by "dist/". Threading an isDir flag through
		// the signature is out of scope here.
		if strings.HasSuffix(pattern, "/") {
			dir := strings.TrimSuffix(pattern, "/")
			if dir == "" {
				continue
			}
			if relPath == dir || (!negated && strings.HasPrefix(relPath, dir+"/")) {
				excluded = !negated
			}
			continue
		}

		if pattern == "" {
			continue
		}

		if matchesIgnorePattern(relPath, baseName, pattern, anchored, negated) {
			excluded = !negated
		}
	}

	return excluded
}

// matchesIgnorePattern reports whether relPath matches a single ignore pattern
// that has already had its "!" and leading "/" stripped. Directory patterns
// (a trailing "/") never reach here: isExcluded handles them inline.
//
// negated says the "!" was there. It only ever narrows the match: a negated
// pattern matches the paths it names directly, but not the paths merely
// underneath a directory it names, because git cannot re-include a file whose
// parent directory is excluded. A positive pattern keeps that reach, since git
// ignores everything inside an ignored directory.
//
// Globbing goes through doublestar, not filepath.Match, so that "**" means zero
// or more path segments the way git and npm read it. filepath.Match's "*" never
// crosses a separator, which left "**/*.pem" — the standard "exclude keys
// everywhere" idiom — matching paths of exactly two segments: a key at the
// package root and a key nested two directories down were both published, while
// the pattern looked like it worked.
//
// That also brings the rest of doublestar's syntax, and one direction of it
// fails open: "weird{a,b}.txt" is now brace alternation, so it no longer
// excludes a file of that literal name by basename, and "[!a]" negates a
// character class where filepath.Match read it as the class containing "!" and
// "a". The branches below that compare strings rather than glob are the escape
// hatch — a pattern naming the full path still matches. Accepted deliberately,
// with the reasoning
// and the rejected alternatives in
// docs/adr/0003-ignore-patterns-glob-with-doublestar-syntax-and-all.md.
//
// Case is never folded here, on any platform: a user's pattern matches the path
// as spelled. That is a decided divergence from git (#353), not an unexamined
// default. isDefaultExcluded's comment holds the git behaviour and the reasons
// the built-in lists fold where these do not; it stays the single copy of that
// reasoning, and what follows is only what #353 added to it.
//
// The divergence was measured, on git 2.43.0, against a .gitignore holding
// "secret.txt" with SECRET.TXT on disk: core.ignorecase=false leaves it
// untracked and `git check-ignore` exits 1, while core.ignorecase=true drops it
// from `git status` and check-ignore prints the matching rule. lnpm behaves like
// the first of those everywhere. That much was run. What was *not* run is git's
// probe: that git-init(1) and git-clone(1) "probe and set core.ignoreCase true
// if appropriate when the repository is created" is quoted from git-config(1),
// because the probe only turns the setting on for a case-insensitive
// filesystem, and this was measured on a case-sensitive one — where git init
// leaves core.ignorecase unset altogether.
//
// #353 weighed two alternatives and rejected both, named here so the next reader
// does not reopen the question. Probing the filesystem at pack time and folding
// to match git makes a tarball's contents depend on the machine that packed it,
// and puts a filesystem probe in the pack path. Folding unconditionally diverges
// from git on Linux instead, and silently stops publishing files that ship today
// — the direction that loses content from a package. What was kept instead is
// fail-open for a file the author asked to withhold, and README's npm-divergence
// list says so in those terms. It reaches author-chosen exclusions only, since
// the built-in lists fold on every platform regardless.
//
// TestUserIgnorePatternsStayCaseSensitive pins this side of the line, and
// TestDefaultExcludesMatchCaseInsensitively the other.
func matchesIgnorePattern(relPath, baseName, pattern string, anchored, negated bool) bool {
	// "dir/**" matches the directory and everything under it.
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return relPath == prefix || (!negated && strings.HasPrefix(relPath, prefix+"/"))
	}

	// Exact match
	if pattern == relPath {
		return true
	}

	if !anchored && !strings.Contains(pattern, "/") {
		// For unanchored patterns without path separators, also match against
		// the basename. This follows gitignore behavior: "*.log" matches
		// "foo/bar.log".
		if matched, _ := doublestar.Match(pattern, baseName); matched {
			return true
		}
	} else {
		// Full path glob match
		if matched, _ := doublestar.Match(pattern, relPath); matched {
			return true
		}
	}

	// Directory prefix match: "node_modules" excludes "node_modules/foo".
	return !negated && strings.HasPrefix(relPath, pattern+"/")
}

// isIncluded checks if a path matches any include pattern (files field).
//
// Each pattern is normalized before matching, the way npm reads a "files"
// entry. A leading "/" anchors the pattern to the package root rather than
// naming an absolute path, and it is dropped because every match below is
// against the full relative path: isIncluded never matches a basename, so
// anchoring is already how it behaves and the "/" carries no information.
// Contrast isExcluded, which keeps the leading "/" as its anchored flag
// precisely because it does match basenames and must suppress that.
//
// Not matching basenames is npm parity, not a divergence, and an earlier draft
// of this comment had it backwards — it claimed npm's "files" matches a bare
// "*.md" against a nested path. It does not: on npm 11.16.0, "files": ["*.md"]
// ships a root top.md and leaves docs/guide.md out, and ["guide.md"] ships
// nothing at all against docs/guide.md. Run on a fixture package for each.
// What did miss a root file was "**/*.md", and that was the "**" defect #350
// fixed rather than anything about basenames.
//
// The two glob through the same engine, and did not always. isIncluded used
// filepath.Match, where "*" never crosses a separator and "**" is an ordinary
// two-star segment, so "lib/**/*.js" excluded lib/top.js as an ignore pattern
// and did not include it as a "files" entry — one package, two meanings for
// "**", and the "files" side silently shipped less than the manifest declared.
// #350 moved this side onto doublestar as well.
//
// That inherits doublestar's whole dialect here too, as it did on the ignore
// side. For the two constructs ADR-0003 named — brace alternation and "[!a]" as
// a class negation — the widening moves toward npm rather than away from it,
// since npm globs a "files" entry with minimatch and both are measured parity.
// It does not follow that the dialects match: doublestar has no extglob, so an
// entry like "+(a|b).txt" selects nothing here and a file under npm. ADR-0003
// records both directions and that gap.
//
// A leading "./" is dropped too, and npm agrees: "./dist" ships the tarball
// "dist" ships (#346). One is dropped, and it is dropped before the "/" —
// matchFilesField's own comment carries the npm runs behind both details.
//
// That is where the two matchers stop agreeing, and the divergence is decided
// rather than overlooked. isExcluded leaves a leading "./" in place, so a
// ".gitignore" line reading "./dist" ignores nothing. Both halves were run
// rather than reasoned: on git 2.43.0 a .gitignore holding "./dist" leaves
// dist/index.js untracked while one holding "dist" ignores it, and here
// isExcluded("dist/index.js", ["./dist"]) is false against a true for ["dist"].
// An ignore file is git's format even when it is spelled .npmignore, and a
// "files" entry is npm's, so each side follows its own tool. #346 names this
// asymmetry as a thing to record rather than remove;
// TestIsExcludedDoesNotResolveLeadingDotSlash pins it from both sides, and
// applying matchFilesField's trim to applyIgnorePatterns turns that test red.
//
// A trailing "/" marks a directory whose contents are included, and is dropped
// only from patterns with no glob metacharacter. npm does not read a trailing
// slash on a glob as a directory marker, so "dist/**/" matches nothing and
// must not be normalized into "dist/**", which matches everything under dist.
// A glob keeping its slash is then answered by matchFilesField's own
// trailing-"/" branch, which matches nothing on purpose. It used to fall through
// to the glob engine and match nothing by accident, which stopped being true
// when #350 changed the engine.
//
// So "dist", "/dist", "dist/", "/dist/" and "./dist" are all equivalent, as
// they are to npm, and "dist/**", "/dist/**" and "./dist/**" are equivalent to
// each other. "dist/**/" is equivalent to none of them, and neither is
// "./dist/**/".
//
// An entry that is empty once normalized — "", "./" or any run of "/" —
// includes everything, which is what npm does with it: all of them ship the same
// files as a package with no "files" field. isExcluded declines to filter a path
// out on any of them as well, so neither function drops one on the strength of a
// degenerate entry — but it reaches that answer by two routes, and only one of
// them is a degenerate skip. "", "/" and "//" are skipped there without being
// consulted: the first two hit the empty-pattern guard, and "//" hits the
// empty-directory guard inside the trailing-slash branch. "./" hits neither,
// because isExcluded resolves no leading "./" — it reads the entry as the
// directory pattern "." and simply matches nothing.
//
// Longer runs join the set only on this side, and only since #403: "///"
// normalized to "/" before it and selected nothing here, where npm 11.16.0 ships
// everything. It takes the third route rather than either of those two —
// isExcluded resolves no run any more than it resolves a leading "./" — but the
// property that matters survives, and was run rather than argued: measured on
// 2026-08-25, isExcluded("dist/cli/index.js", ["///"]) is false, so no path is
// filtered out on the strength of it.
//
// A bare "." is not one of them, however much it looks like one. npm 11.16.0
// ships only the always-included set for "files": ["."] — run and confirmed on
// a fixture package — so it selects nothing here either, and #346 left it alone
// deliberately rather than by omission.
func isIncluded(relPath string, patterns []string) bool {
	return matchFilesField(relPath, patterns) != filesMatchNone
}

// isIncludedDirectly reports whether some "files" entry names relPath itself,
// rather than reaching it only as the contents of a directory the entry names.
//
// It exists for one caller and one question. #321 lets a "files" entry override
// defaultExcludes, and the override is per named path: "files": [".env.example"]
// publishes that template, and "files": ["dist"] must not thereby publish
// dist/.env, dist/app.log or dist/pkg.tgz — an entry naming a build directory is
// not a statement about the secrets that may have landed in it. collectFiles
// asks this only for paths defaultExcludes covers; every other path keeps
// isIncluded's containment reach exactly as #318 landed it.
func isIncludedDirectly(relPath string, patterns []string) bool {
	return matchFilesField(relPath, patterns) == filesMatchDirect
}

// filesMatch is how a "files" entry reached a path: not at all, by containing
// the directory it sits in, or by naming the path itself.
//
// The distinction is drawn by which branch of matchFilesField fired and by
// nothing else. It is deliberately not inferred from the text of the pattern:
// #318's first attempt at a related question compared pattern text lexically and
// broke on glob entries, whose reach cannot be read off the string.
type filesMatch int

const (
	filesMatchNone filesMatch = iota
	// filesMatchContains: the entry swept the path in rather than naming it —
	// by naming an ancestor ("dist" reaching dist/a.js), by naming a subtree
	// ("dist/**"), by ending in a bare wildcard segment ("dist/*", "*", "**"),
	// by matching an ancestor with such a segment ("*" reaching dist/a.js,
	// #406), or by naming nothing at all (the degenerate "").
	filesMatchContains
	// filesMatchDirect: the entry named this path. Either literally
	// ("dist/.env"), or as a glob whose last segment constrains the name
	// ("*.env", ".env.*", "dist/*.env").
	filesMatchDirect
)

// matchFilesField reports the strongest way any entry in patterns reaches
// relPath. Direct beats contains, so one entry naming the path outranks another
// merely containing it: "files": ["dist", "dist/.env"] publishes dist/.env.
//
// The boundary is a glob's final segment, and it is the one to know. A bare "*"
// or "**" there sweeps a directory and names nothing, so "dist", "dist/*" and
// "dist/**" all agree: none publishes a default-excluded dist/.env. A segment
// that constrains the name does name, so "dist/*.env", ".env.*" and "*.example"
// all publish what they match.
//
// An earlier draft split "dist/*" from "dist/**", on the reasoning that a single
// "*" cannot cross a separator so the first picks out one level rather than a
// tree. Still true of doublestar — doublestar.Match("dist/*", "dist/cli/index.js")
// is false, run and confirmed — and still beside the point: it made
// "files": ["*"] publish every overridable-list file at the package root, and it
// split the spellings of "ship everything", since the degenerate entries
// classify as containment and a "*" that named paths disagreed with them. The
// question is whether the entry said anything about the name, not how deep it
// reaches.
//
// Since #406 the two spellings do not differ in reach either, and that came from
// the *other* side of the question: npm expands whatever such an entry matches
// into a subtree, so "dist/*" selects dist/cli/index.js as "dist/**" does. The
// classification did not move to meet it — the default branch's ancestor arm is
// containment and only containment — so the paragraph above still holds, and
// the two questions stayed separate answers to one predicate.
//
// The branches below and their order are unchanged from the single-verdict
// isIncluded this was split out of; the returns are, the trailing-"/" branch is
// #350's, and the default branch's ancestor arm is #406's.
//
// The trailing "/**" branch is kept, and it is no longer redundant in the way it
// looks. For an entry carrying no metacharacter ahead of the "/**" the glob
// engine now reaches the same verdict, so deleting the branch changed nothing
// measurable until #350 added the two rows that pin it; with those rows present,
// deleting it turns exactly them red and nothing else in the suite. Both runs
// measured.
//
// What it buys is the escape hatch ADR-0003 leans on. It compares strings, so an
// entry like "weird{a,b}/**" still names that literal directory and its
// contents, where doublestar expands the braces and matches nothing —
// doublestar.Match("weird{a,b}/**", "weird{a,b}/x.txt") is false, run and
// confirmed. matchesIgnorePattern has its own copy of the branch, so the two
// sides keep that hatch together.
func matchFilesField(relPath string, patterns []string) filesMatch {
	best := filesMatchNone

	for _, pattern := range patterns {
		// Every branch below sees a string byte-identical to the unprefixed
		// spelling, which is what makes "./dist" and "dist" classify the same
		// rather than merely select the same paths. A "./dist" reaching the glob
		// engine whole would have been a direct match, and #321's rule would
		// have published dist/.env from an entry naming the directory only.
		// normalizeFilesEntry carries the npm runs behind each trim.
		pattern = normalizeFilesEntry(pattern)

		var match filesMatch
		switch {
		case pattern == "":
			// The degenerate entry ships everything, so it contains every path
			// and names none. That keeps "files": [""] shipping what a package
			// with no "files" field ships, which is what it has always meant —
			// and a package with no "files" field does not ship .env.
			match = filesMatchContains

		case pattern == relPath:
			match = filesMatchDirect

		case strings.HasPrefix(relPath, pattern+"/"):
			// "dist" reaching dist/anything.
			match = filesMatchContains

		case strings.HasSuffix(pattern, "/**"):
			prefix := strings.TrimSuffix(pattern, "/**")
			if relPath == prefix || strings.HasPrefix(relPath, prefix+"/") {
				// Containment for both halves. The relPath == prefix half can
				// only fire for a *file* literally named "dist" against the
				// entry "dist/**", since collectFiles skips directories before
				// asking: an odd fixture, and calling it containment costs
				// nothing unless that file is also default-excluded.
				match = filesMatchContains
			}

		case strings.HasSuffix(pattern, "/"):
			// A glob entry with a trailing "/" names nothing, and this branch is
			// what says so. npm reads the slash as part of the pattern rather
			// than as a directory marker, and ships nothing for either spelling:
			// "files": ["dist/**/"] and ["dist/*/"] both produce a tarball
			// holding only the always-included set, run on a fixture package
			// with npm 11.16.0.
			//
			// Only a glob reaches here now, because the trim above takes the
			// whole trailing slash run off an entry with no "*" — "dist/" and
			// "dist//" both arrive as "dist" and are answered by the branches
			// above. Until #403 the trim took one slash, so "dist//" kept one,
			// landed here and selected nothing where npm 11.16.0 ships dist.
			// That was the trailing half of #403, broadened onto it during #350
			// with the measurement; nothing in #350 introduced it, since before
			// it the same entry reached filepath.Match("dist/", …) and missed
			// just as widely.
			//
			// #403's fix had to leave this branch's rule intact, and did: the
			// trim is still guarded on "*", so ["dist/**/"] and ["dist/**//"]
			// both still land here and name nothing. npm ships nothing for
			// either — run and confirmed — where dropping a trailing slash from
			// a *glob* would turn them into ["dist/**"] and ship a subtree.
			//
			// Leaving it to the glob branch would not merely select the wrong
			// paths. doublestar.Match("dist/**/", "dist") is true where
			// filepath.Match's was false, and lastSegment("dist/**/") is "",
			// which isBareWildcard rejects — so the entry would classify as
			// filesMatchDirect against a *file* named "dist" and, under #321,
			// override defaultExcludes for it. An entry npm reads as naming
			// nothing would have become an entry naming a path. #350.

		default:
			// A bare wildcard in the last segment does two things at once, and
			// they are separate questions. It decides the classification —
			// containment rather than naming, the paragraph below — and it
			// decides the reach, this line and the ancestor branch under the
			// glob test. #406.
			//
			// The reach half is npm's. Read from npm-packlist's processPackage
			// in npm 11.16.0: an entry ending in "/*" has a second "*" appended
			// before anything globs, so "dist/*" is globbed as "dist/**", and an
			// entry the walker cannot lstat becomes an inverted rule in a
			// synthetic ignore *file*, where a bare "*" carries gitignore's
			// match-at-any-depth meaning rather than a glob's. Either way the
			// entry sweeps a whole subtree.
			//
			// Run one entry at a time with npm pack --dry-run --json, all four
			// against the fixture TestMatchFilesFieldGlobsWithDoublestar's
			// header describes — that tree and not the five-file one README and
			// ADR-0003 cite, since a measurement here is not stitched together
			// from two fixtures. Re-run on 2026-08-25:
			//
			//	["dist/*"]  ships dist/cli/deep/z.js, so the reach is a subtree
			//	            and not one level
			//	["*"]       ships the whole tree
			//	["*/*"]     ships lib/sub/a.js and lib/keep.js and neither
			//	            top.js nor index.js, so it expands every
			//	            first-level directory and reaches no root file
			//	["di*/*"]   ships the dist subtree, so the prefix may glob
			//
			// It is only the *last* segment that does this, which is why the
			// reach is not "any entry that matches a directory expands it". Two
			// entries settle that, run against the same fixture: ["d*"] and
			// ["dist/c*"] each ship nothing but the always-included set, though
			// d* matches the directory dist and dist/c* matches dist/cli. So an
			// ancestor-directory rule written without this guard would ship two
			// subtrees npm does not.
			//
			// The ancestor walk is how the subtree is reached rather than
			// rewriting the entry to prefix + "/**" and matching that. The two
			// differ on ["*/*"]: doublestar.Match("*/**", "index.js") is true —
			// run and confirmed — where npm ships no root file for that entry,
			// since "**" here may stand for zero segments. Asking whether the
			// entry matches a *directory above* relPath cannot reach a root file
			// at all, because a root file has no ancestor but the package root.
			sweepsSubtree := isBareWildcard(lastSegment(pattern))

			if matched, _ := doublestar.Match(pattern, relPath); matched {
				// A glob names a path only if its last segment says something
				// about the name. A trailing bare wildcard does not: "*" and
				// "dist/*" are "everything here", which is a statement about a
				// directory's contents and not about any file in it. Treating
				// them as naming made "files": ["*"] publish .env, .env.local,
				// app.log, pkg.tgz, Thumbs.db and the rest of the overridable
				// list from a package that had said nothing about any of them.
				//
				// It also keeps the spellings of "ship everything" classifying
				// alike. "", "./" and any run of "/" reach the degenerate
				// branch above — since #403 the run is trimmed whole, so "///"
				// arrives there like "/" and "//" always did — and are
				// containment there; "*" and "**" arrive here and
				// have to agree, or the same manifest would mean two different
				// things depending on which spelling was chosen. How far each
				// reaches is a separate question, and since #350 they reach
				// different distances while classifying alike: doublestar stops
				// "*" at a separator and lets "**" span any number of them, so
				// "**" sweeps the whole tree in and "*" only the package root.
				// Both still name nothing.
				//
				// A bare "." is not on that list. It looks like it belongs and
				// does not: npm 11.16.0 ships only the always-included set for
				// ["."] — run and confirmed on a fixture package — so it is not
				// a spelling of this intent, it classifies as filesMatchNone,
				// and nothing here has to agree with it.
				//
				// Anything that constrains the segment still names: "*.example",
				// ".env.*" and "dist/*.env" all pick out files by name rather
				// than sweeping a directory.
				if sweepsSubtree {
					match = filesMatchContains
				} else {
					match = filesMatchDirect
				}
			} else if sweepsSubtree && matchesAncestorDir(pattern, relPath) {
				// The entry matched a directory relPath sits under, so it swept
				// the path in. Containment, never naming — and that is the
				// whole safety property of #406 rather than a detail of it.
				// Every path here arrived through a bare wildcard, so the entry
				// said nothing about any name, and #321's override must not
				// fire: "files": ["*"] sweeps dist/.env in and still does not
				// publish it.
				//
				// Writing filesMatchDirect here is what publishes it, and one
				// test in the suite catches that:
				// TestPackGlobSweepDoesNotConsentToDefaultExcludes, which #406
				// added for this line alone. Measured on 2026-08-25 by making
				// exactly that change, with go vet ./... clean first and
				// internal/pack's result line read for a duration — it packs
				// [dist/.env dist/a.js dist/app.log dist/cli/.env dist/cli/b.js
				// dist/cli/deep.log dist/pkg-1.0.tgz index.js package.json].
				//
				// The two tests #406's issue named as the guards here both stay
				// green under it, and that is worth knowing rather than
				// assuming: TestPackDoubleStarSweepsWithoutConsentingToDefaultExcludes
				// runs "**", which doublestar matches against every path
				// directly, and every tree in
				// TestPackFilesEntryOverridesDefaultExcludesOnlyByDirectMatch
				// puts its default-excluded file where the entry already
				// reaches it. Neither ever runs this branch. A third case stays
				// green for a reason of its own: the *root* .env is refused by
				// the bare-wildcard rule in the branch above, not by this line,
				// since "*" matches a root path outright.
				match = filesMatchContains
			}
		}

		if match > best {
			best = match
		}
		if best == filesMatchDirect {
			return best
		}
	}

	return best
}

// normalizeFilesEntry puts one "files" entry into the spelling every matcher
// reads it in. It is shared rather than copied: matchFilesField and
// filesFieldMayReach have to agree on the string byte for byte, and the second
// was first written with its own copy of these lines and a comment asserting the
// two stayed in step. lowerPatterns above is the same call — derive, do not
// duplicate — and this repo has already had a corrected sentence's twin go stale
// nine lines away.
//
// namesHardReserved is the third caller, since #402. It is not a matcher, so it
// does not need the byte-for-byte agreement the first two do; it shares this for
// the plainer reason that a maintainer's entry should mean one thing whether
// lnpm is selecting on it or warning about it. Its own comment carries the
// measurement that let it drop its own trims, which were not equivalent to
// these.
//
// One leading "./" comes off, and it comes off before the "/" trims. Both halves
// of that were measured against npm 11.16.0 rather than reasoned:
//
//	["./dist"]    ships dist, exactly as ["dist"] does — #346
//	["././dist"]  ships nothing, so npm resolves one "./" and not a run
//	["/./dist"]   ships nothing, so a "./" behind the anchor is not one
//	[".//dist"]   ships dist, since the "/" trims still run afterwards
//
// Trimming "/" first flips both of the slash-mixed rows, measured: "/./dist"
// becomes "./dist" and then "dist", selecting dist from an entry npm selects
// nothing for, while ".//dist" keeps its inner "/" and selects nothing where npm
// ships dist. Both flips were re-run for #403 against the greedy trims below and
// both still flip: swapping the two now turns seven rows red over three tests,
// TestIsIncludedPatternForms' ".//dist", "/./dist", ".///dist" and "//./dist",
// TestNamesHardReservedResolvesOneDotSlashInNpmsOrder' "/./node_modules/dep" and
// ".//node_modules/dep", and one row of
// TestNamesHardReservedDotSlashAgreesWithUnprefixedForm. Measured 2026-08-25
// with go vet ./... clean first; the run spellings made the guard wider rather
// than weaker.
//
// "Swapping the two" has two spellings now that there are two "/" trims, and
// they do not give the same answer, so say which one the seven belongs to: it
// is the "./" trim moved to sit *between* the leading and the trailing slash
// trim. Moving it below both instead turns ten rows red over five tests — the
// seven above, plus TestIsIncludedDegeneratePattern's "dot slash" and "agrees
// with isExcluded ./" and TestPackDegenerateFilesEntryShipsEverything's
// "files ./". Those three are the degenerate entries and they move for a reason
// the seven never reach: "./" gets past the leading trim untouched, loses its
// slash to the *trailing* one, and arrives at the "./" trim as ".", which this
// function leaves alone by design — so the entry stops meaning "ship
// everything". Both spellings measured 2026-08-25, go vet ./... clean first.
//
// The "/" trims are greedy where the "./" trim is not, and that asymmetry is
// npm's rather than this repo's. #403 measured the run against npm 11.16.0 on a
// fixture package instead of assuming it followed the "./" rule, because it does
// not:
//
//	["//dist"] ["///dist"] ["////dist"]  all ship dist, as ["/dist"] does
//	["dist//"] ["dist///"]               both ship dist, as ["dist/"] does
//	["//dist//"]                         ships dist, so the two ends compose
//	[".///dist"]                         ships dist, the "./" trim first again
//	["//./dist"]                         ships nothing, so trimming the leading
//	                                     run does not expose a second "./"
//	["///"]                              ships everything, joining [""], ["/"]
//	                                     and ["//"] — #227
//
// Before #403 a run survived partially and matched nothing: ["//dist"] became
// "/dist" and selected nothing where npm ships dist, and ["///"] became "/" and
// selected nothing where npm ships everything. #227 had settled the degenerate
// spellings only as far as its two-slash one.
//
// A trailing "/" run comes off only an entry with no "*". npm does not read a
// trailing slash on a glob as a directory marker, so "dist/**/" matches nothing
// and must not be normalized into "dist/**", which matches everything under
// dist. ["dist/**//"] ships nothing either, run and confirmed, so guarding on
// "*" answers the run as well as the single slash. isIncluded's doc comment
// carries that measurement, and matchFilesField's trailing-"/" branch is what
// answers the glob spellings this leaves alone.
//
// The trailing half has a revert direction of its own, and nothing recorded one
// until it was asked for: the leading trim and the trim order were both covered
// above and swapping this strings.TrimRight back to strings.TrimSuffix was not,
// though #403's second comment is what broadened scope onto the trailing end.
// Measured on 2026-08-25 with go vet ./... clean first and internal/pack's
// result line read for a duration rather than [build failed] — five rows red
// over two tests:
//
//	TestIsIncludedPatternForms/dist//
//	TestIsIncludedPatternForms/dist///
//	TestIsIncludedPatternForms///dist//
//	TestPackFilesEntryTrailingSlashRunOnAFileOverridesDefaultExcludes/.env_with_a_slash_run
//	TestPackFilesEntryTrailingSlashRunOnAFileOverridesDefaultExcludes/index.js_with_a_slash_run
//
// Rows expected to stay green and confirmed green, since a revert that moves a
// row proves nothing until the rest is checked. TestIsIncludedPatternForms'
// "dist/**//" stays green because the trim is guarded on "*" either way, and its
// leading-run rows — "//dist", "///dist", "////dist", ".///dist", "//./dist" and
// "//lib" — stay green because TrimLeft is untouched; only "//dist//", which
// carries a run at both ends, is in the red list. TestIsIncludedDegeneratePattern's
// "///" stays green for the same reason, the leading trim having emptied it
// before this one is reached. Every namesHardReserved row stays green,
// "node_modules//" included: TrimSuffix leaves "node_modules/", which
// isHardReserved answers true for already. And every package outside
// internal/pack prints `ok`.
//
// The trailing trim is an approximation of npm and not parity, which #403
// measured and deliberately did not change. Its cost reaches #321's secret
// override, so state it rather than leave it to be derived.
//
// npm reads a trailing slash as a directory marker in the strict sense: it
// refuses a *file* outright. Run on a fixture package with npm 11.16.0 on
// 2026-08-25:
//
//	[".env"]      ships .env
//	[".env/"]     ships nothing but the always-included set
//	[".env//"]    ships nothing but the always-included set
//	["index.js/"] ships nothing but the always-included set
//
// This trims the run off instead and lets the bare name match whatever it names.
// A bare name is a *direct* match, and a direct match is exactly what #321 lets
// override defaultExcludes — collectFiles' two softExcluded.covers guards each
// stand down for it. So a trailing slash run on a *file* entry now overrides
// that list where it previously did not: [".env//"] publishes a secret lnpm
// withholds by default, having selected nothing at all before #403, since the
// old TrimSuffix left ".env/" for matchFilesField's trailing-"/" branch to
// answer filesMatchNone.
//
// [".env/"] is what keeps that honest, and it is why the behaviour stayed. It
// already overrode defaultExcludes before #403 — one slash came off then too —
// so the override through a slashed spelling is not new, and what #403 changed
// is that the two spellings now agree rather than one being a silent hole.
// Agreeing is #403's first acceptance criterion. Agreeing in npm's direction
// instead means refusing a file with a trailing slash outright, which is a
// behaviour change to argue on its own and not something a trim should decide.
// TestPackFilesEntryTrailingSlashRunOnAFileOverridesDefaultExcludes pins all
// five spellings so a later change has to flip them on purpose.
//
// So the divergence is narrower than "lnpm trims a trailing slash and npm does
// not", and it is worth naming which spelling falls where rather than leaving it
// at the one example. All eight below were run against npm 11.16.0 on
// 2026-08-25:
//
//   - On a *file*, lnpm ships more than npm, in both slash spellings, and this
//     is the whole of the divergence. ["index.js/"], ["index.js//"], [".env/"]
//     and [".env//"] each select the file here and each select nothing there.
//   - On a *directory*, the two agree, and since #403 for the run as well:
//     ["dist/"] and ["dist//"] both ship dist on both sides.
//   - On a *glob*, the two agree and did before #403, because the trim is
//     guarded on "*": ["dist/**/"] and ["dist/**//"] name nothing on either
//     side.
//
// Note what this deliberately does not touch. A bare "." is left alone and
// therefore still selects nothing, which looks like a bug and is not: ["."]
// ships only the always-included set under npm 11.16.0, while ["./"], [""],
// ["/"], ["//"] and ["///"] all ship everything. Run and confirmed on a fixture
// package for each spelling. ["././"] is the neighbouring trap and is not
// degenerate: npm ships nothing for it, and the trims leave "." here.
//
// Only the "files" side resolves this. isExcluded deliberately does not — see
// isIncluded's doc comment for why the two differ.
//
// No platform split hides in this. Each trim is a literal byte compare and its
// output is the unprefixed spelling itself, so both spellings reach the glob
// branch identically, and TestMatchFilesFieldDotSlashAgreesWithUnprefixedForm
// asserts that equality on every CI platform as well as a fixed answer. Since
// #350 there is nothing platform-dependent left to worry about here anyway:
// doublestar's separator is always "/", unlike filepath.Match, whose is the
// platform's — the split isDefaultInclude still has to guard against. A
// Windows-style ".\\dist" is a different entry and is not handled — nothing here
// has ever read "\" as a separator, since collectFiles hands over paths already
// through filepath.ToSlash.
func normalizeFilesEntry(pattern string) string {
	pattern = strings.TrimPrefix(pattern, "./")
	pattern = strings.TrimLeft(pattern, "/")
	if !strings.Contains(pattern, "*") {
		pattern = strings.TrimRight(pattern, "/")
	}
	return pattern
}

// lastSegment returns the part of a slash-separated "files" entry after its
// final "/", which is the segment that decides whether the entry names a file
// or sweeps a directory. An entry with no "/" is its own last segment.
func lastSegment(pattern string) string {
	if i := strings.LastIndex(pattern, "/"); i >= 0 {
		return pattern[i+1:]
	}
	return pattern
}

// isBareWildcard reports whether a path segment is nothing but a wildcard.
//
// Since #406 this decides two things about a "files" entry's last segment, and
// they are worth naming apart because only one of them is a safety property:
//
//   - Classification. Both spellings say the same thing about the name, which
//     is nothing, so an entry ending in either is containment and never
//     consent. Classifying them alike is what keeps "files": ["*"] and ["**"]
//     agreeing with the degenerate spellings of "ship everything", none of
//     which publishes a default-excluded path.
//   - Reach. matchFilesField's default branch gates its ancestor-directory walk
//     on this predicate, so an entry ending in a bare wildcard expands every
//     directory it matches into that directory's whole subtree and one that
//     constrains its last segment does not. That is why ["d*"] and ["dist/c*"]
//     select nothing but the always-included set, which is what npm does with
//     them.
//
// So this comment used to say "reach is not what this decides", and #406 made
// that false in the same change that wrote the line above. The two spellings
// reach the same distance again, by two different routes: "**" gets there
// through the glob engine, which spans separators, and "*" gets there through
// the ancestor walk, which is the only way it can, since doublestar stops a
// single "*" at a separator. Run and confirmed on 2026-08-25 — matchFilesField
// returns the same verdict for ["*"] and ["**"] on index.js, dist/a.js,
// dist/cli/deep/z.js, a/b/c/d/e/f.txt, .env and dist/.env alike.
//
// It is only between #350 and #406 that the two differed: doublestar swept the
// whole tree for "**" and only the package root for "*". Before #350 they
// agreed for a third reason — filepath.Match read "**" as an ordinary two-star
// segment, so both matched exactly one segment. The classification is the half
// that has never had to change through any of it.
//
// Note that "dist/**" never reaches this: the trailing-"/**" branch above claims
// it first, and calls it containment too, so those two spellings agree by yet
// another route.
func isBareWildcard(segment string) bool {
	return segment == "*" || segment == "**"
}

// matchesAncestorDir reports whether pattern matches some directory strictly
// above relPath, both slash-separated and relative to the package root.
//
// This is how an entry ending in a bare wildcard reaches a whole subtree, and
// its one caller guards it on exactly that — see matchFilesField's default
// branch for the npm runs behind both halves. A hit there is containment and
// nothing else, so this predicate can never widen #321's override.
//
// It walks up rather than down because matchFilesField is asked per file:
// collectFiles skips directories before asking, so the directory dist/cli is
// never a relPath and "dist/*" would otherwise select nothing under it.
//
// It terminates because slashpath.Dir walks strictly up and returns "." for a
// single-segment path. The package root is not a candidate — it is not a path
// any entry can name, and stopping there is what keeps ["*/*"] off a root file.
// A doublestar syntax error is a non-match here, as it is at the caller's own
// Match: an entry the engine will not parse selects nothing rather than
// everything.
//
// What it costs, because #406's issue asked for that measured rather than
// argued. This runs one doublestar.Match per ancestor per path per guarded
// entry, on every path in the tree and not only on the ones an entry ends up
// selecting, so the shape is depth × entries. It is paid exactly where
// whitelist mode is already at its most expensive: collectFiles' ignore-chain
// prune is guarded on !useWhitelist, so a package with a "files" field has
// always descended into every directory hardReservedExcludes does not stop.
// #406 did not change that, so there was no pruning to lose — and nothing here
// caches or reorders to make the number below smaller.
//
// Measured on 2026-08-25 on Linux, go1.26.7, four cores, against a fixture of
// 4098 files: package.json and index.js at the root, and a deep/ tree of
// breadth 4 and five levels of subdirectories — 1364 of them — holding four
// files in each of the 1024 leaves. Every leaf file carries six directories
// above it, so a walk to the root is six Match calls and the fixture forces
// 24,576 of them. Pack was benchmarked at -benchtime 10x against worktrees of
// 2ff5885 and 837fbbb, with the walk's progress Printfs stubbed out identically
// on both sides so terminal writes stayed out of the timing — the per-1000 one
// in all three runs below, the line-clearing one in the second and third.
//
// The isolating entry is "files": ["nomatch/*"]. Its last segment is a bare
// wildcard, so every path walks all the way to the root, and nothing matches,
// so both sides pack the same single file and the whole difference between them
// is #406's branch and this function. Three runs, medians of six, six and ten
// readings:
//
//	before   38.88 ms   40.08 ms   40.57 ms
//	after    41.72 ms   42.61 ms   42.53 ms
//	delta    +2.84      +2.53      +1.96
//
// About +2.5 ms on a 40 ms pack. Each delta against its own before reading is
// +7.3%, +6.3% and +4.8%, so the spread across those three runs is +4.8% to
// +7.3% and the band has to contain both ends rather than sit between them.
// Per path that is about 600 ns and per Match about 100 ns. In the first two
// runs the before and after readings did not overlap at all; in the third they
// did, the machine having been busier — 39.0-44.7 before against 41.6-50.1
// after — which is the spread any of these numbers should be read against.
//
// The control is "files": ["nomatch"], the same entry without the wildcard. It
// packs the same one file and never reaches this function at all, because
// isBareWildcard rejects a literal last segment. Run beside the sweep in
// the same process, ten readings each, it settles the delta without asking two
// checkouts to have been equally loaded: on 837fbbb the sweeping entry costs
// 42.53 ms against the literal one's 40.00, the same +2.5 ms, and on 2ff5885
// there is no difference to find — 40.57 against 41.25, the sweep nominally the
// faster of the two.
//
// Small, then, but not free, and the term to watch is depth rather than file
// count: a tree twice as deep pays twice per path. The entry that pays most is
// one that matches nothing, since a hit returns at the first ancestor it finds
// and a miss walks to the root.
func matchesAncestorDir(pattern, relPath string) bool {
	for dir := slashpath.Dir(relPath); dir != "." && dir != "/" && dir != ""; dir = slashpath.Dir(dir) {
		if matched, _ := doublestar.Match(pattern, dir); matched {
			return true
		}
	}
	return false
}

// isDefaultInclude reports whether relPath is in the always-included set: the
// files that ship whatever the package.json "files" whitelist says.
//
// The set is anchored to the package root, as npm's is. It used to match
// filepath.Base(relPath), so any depth qualified, and a whitelist the developer
// wrote as ["dist"] still shipped internal-docs/README.private,
// notes/changes.txt and vendor/foo/LICENSE — each basename matched an entry
// somewhere below the root. #320.
//
// Entries are matched case-folded: both the pattern and relPath go through
// strings.ToLower before filepath.Match, unconditionally and on every platform,
// so a single spelling of a name in defaultIncludes answers for every casing of
// it and a second, differently cased spelling of the same name would be dead
// weight. #363 deleted the three that had accumulated. Two tests assert the
// fold, one per side: TestDefaultIncludesMatchCaseInsensitivelyAtTheRoot over
// every entry in the list, on this function, and
// TestPackDefaultIncludesShipPastAFilesWhitelistInEveryCasing end to end
// against real files, over every entry but package.json — the fixture writes
// that one as the manifest, whose name readPackageJSON fixes, so it is the one
// entry no end-to-end row can re-case.
//
// The separator check is deliberately made before filepath.Match rather than
// left to it. Some defaultIncludes entries are globs ("README*"), and
// filepath.Match reads its separator from the platform. On Linux, "*" stops at
// "/", so filepath.Match("readme*", "dist/readme.md") is false — run and
// confirmed — and dropping filepath.Base alone would look sufficient here. It
// would only be sufficient here: Windows builds path_windows.go, where the
// separator is "\" and "/" is an ordinary character.
// TestIsExcludedSingleStarNeverCrossesSeparatorOnAnyPlatform pins that same
// split biting the ignore matcher, where a single "*" did cross a "/" on Windows
// and not elsewhere from identical inputs. Rather than depend on which way
// filepath.Match resolves it, this rejects a separator before filepath.Match
// sees the path, so the answer cannot differ by platform. TestIsDefaultInclude
// carries the nested rows, and the Windows CI job is the only one that can show
// the check is load-bearing: Linux and macOS both build path_unix.go, so
// filepath.Separator is "/" on each and the two spellings agree there anyway.
//
// Rejected: anchoring with strings.HasPrefix(relPath, pattern) in place of the
// separator check. It cannot express the "*" the glob entries are written with —
// HasPrefix compares "*" as an ordinary byte, so only the entries carrying no
// "*" ever fire, and every one of those over-fires on a longer name.
// Stripping the "*" and prefix-matching the stem repairs the under-match and
// leaves the over-match, since a stem is a prefix.
//
// Both spellings were run against the table in TestIsDefaultInclude, with this
// separator check removed so each was doing the anchoring itself. Measured:
//
//   - HasPrefix on the pattern as written: the six glob rows go red for
//     under-matching — a root README.md stops being included at all — and eight
//     rows go red for over-matching. package.jsonc is one; the other seven are
//     the changelog entries, which have carried no "*" since #360 and so fire
//     as bare prefixes: history.db, changes.sqlite, changelog.json,
//     CHANGELOG.db, changesets.md, historybook.txt and the nested
//     changes/secret.txt.
//   - HasPrefix on the stem: the glob rows pass, and nine go red for
//     over-matching — the eight above plus readme/secret.txt, which the first
//     spelling could not reach because "readme*" is not a prefix of it.
//
// Note what changed with #360 here. Before it, "changes*" was a glob, so the
// first spelling never reached changes/secret.txt and only the second one did.
// Now "CHANGES" is a literal and is its own stem, so that row catches both. The
// stem-only guarantee moved to readme/secret.txt, which is why that row exists.
func isDefaultInclude(relPath string) bool {
	// collectFiles hands over a relPath already through filepath.ToSlash, so
	// "/" is the separator it will carry. The filepath.Separator half defends
	// no caller that exists today and is here on purpose: both current callers
	// are covered by the "/" test alone — collectFiles has already run ToSlash,
	// and ExcludedByProjectRules' only caller passes a bare root filename. This
	// is an exported package's helper, so a future caller handing over a native
	// Windows path would otherwise get every nested path force-included. Do not
	// read the two tests as redundant because they are identical on Linux:
	// filepath.Separator is "\" only on Windows, which is where this half acts.
	if strings.ContainsRune(relPath, '/') || strings.ContainsRune(relPath, filepath.Separator) {
		return false
	}
	for _, pattern := range defaultIncludes {
		matched, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(relPath))
		if matched {
			return true
		}
	}
	return false
}

// HashFile calculates the xxhash of a file's contents
func HashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	h := xxhash.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%016x", h.Sum64()), nil
}

// HashFiles calculates a combined hash of all files
//
// Each field goes in length-prefixed rather than raw, so the boundary between
// one file's record and the next is recoverable from the stream. Written back
// to back they were not: ContentHash is a fixed sixteen characters but RelPath
// is arbitrary and the permission field is one to three octal digits, so a
// filename could be chosen to contain a record boundary and two genuinely
// different packages hashed identically - with no cryptanalysis and no search.
// TestHashFilesFramesFields carries the construction; ADR-0007 carries why
// framing is the fix here rather than a wider hash.
//
// The prefix changes every package hash ever computed, which is why this
// landed in 4.0.0 with #447 rather than on its own. See #453.
func HashFiles(files []*FileInfo) string {
	// Sort by path so the hash is independent of collection/cache order, and
	// include the file mode so permission changes (e.g. chmod +x on a bin)
	// produce a new hash.
	sorted := make([]*FileInfo, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].RelPath < sorted[j].RelPath
	})

	h := xxhash.New()
	var length [8]byte
	field := func(s string) {
		binary.LittleEndian.PutUint64(length[:], uint64(len(s)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(s))
	}
	for _, f := range sorted {
		field(f.RelPath)
		field(f.ContentHash)
		field(fmt.Sprintf("%o", f.Mode.Perm()))
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

// FileInfoFromStore creates FileInfo slice from store path and relative paths
// Used to avoid re-walking store directory when file list is known from DB
func FileInfoFromStore(storePath string, entries []FileEntryData) []*FileInfo {
	files := make([]*FileInfo, len(entries))
	for i, e := range entries {
		files[i] = &FileInfo{
			Path:        filepath.Join(storePath, e.RelPath),
			RelPath:     e.RelPath,
			Size:        e.Size,
			Mode:        e.Mode,
			ContentHash: e.Hash,
		}
	}
	return files
}

// FileEntryData is a minimal struct for file data needed for linking
type FileEntryData struct {
	RelPath string
	Size    int64
	Mode    os.FileMode
	Hash    string
}

// ReadPackageJSON reads and returns just the package.json without scanning files
func ReadPackageJSON(dir string) (*PackageJSON, error) {
	return readPackageJSON(dir)
}

// ExcludedByProjectRules reports whether the package at dir keeps relPath out of
// a published tarball through rules of its own: the package.json "files" field
// passed as filesField, and the root .npmignore - falling back to a root
// .gitignore when there is none, as npm does.
//
// It reads the root ignore file only, where collectFiles now reads one per
// directory. The gap is deliberate and currently invisible: the only caller asks
// about lockfile.RetreatFileName, which sits in the package root, and no ignore
// file in a subdirectory can govern a root-level path. A caller passing a nested
// path would get an answer that ignores the ignore file next to it, so widen
// this before adding one.
//
// It deliberately consults neither built-in exclusion list — not
// hardReservedExcludes and not defaultExcludes. Those are lnpm's own rules, and
// this answers what a tool that reads only the project's own rules would ship -
// which is what `npm publish` is. `lnpm check` uses it to ask whether a file
// lnpm left in the project root is about to be published by something other than
// lnpm.
//
// #321 split one list into those two and did not change this: it consulted
// neither half before and consults neither now.
//
// Its precedence is npm's, and matches collectFiles': a file the "files" field
// includes cannot be excluded through .npmignore or .gitignore, so the
// isIncluded arm below answers on its own and the ignore file is not read for
// that path at all. #318 put that rule into collectFiles' `case
// isIncluded(relPath, filesField):` arm and left this function inverted, asking
// the ignore file anyway, so the two disagreed and this one reported a
// "files"-named path excluded. The two functions do answer different questions
// by design - what npm ships against what lnpm ships - but this is not one of
// the differences; it was a bug, and a fail-open one, since check.go negates the
// answer and so said nothing about a file npm would publish (#347).
//
// The always-included set does not get the same treatment. README* and the rest
// are exempt from the "files" whitelist only, never from an ignore pattern - the
// same rule collectFiles' isDefaultInclude arm states - so a default include
// that "files" did not name falls through to the ignore check exactly as it did
// before. TestExcludedByProjectRulesFilesFieldBeatsIgnoreFile pins both halves.
//
// That fall-through does not depend on the shape below, and it would be easy to
// read it as if it did. One line - `if len(filesField) > 0 && isIncluded(...) {
// return false }` above the original single condition - preserves the
// always-included behaviour identically. Three branches are here because they
// mirror collectFiles' switch, which gives each of these decisions an arm of its
// own, and because that one-line alternative would leave a `!isIncluded` term in
// the condition below it that can no longer be false. A term that cannot fail is
// worse to read than the brace it saves.
func ExcludedByProjectRules(dir string, filesField []string, relPath string) bool {
	if len(filesField) > 0 {
		if isIncluded(relPath, filesField) {
			return false
		}
		if !isDefaultInclude(relPath) {
			return true
		}
	}
	return isExcluded(relPath, loadIgnorePatterns(dir))
}

// filterGitFiles removes any files related to .git directories
// This is a defense-in-depth safety filter applied after all other filtering
//
// It runs over the finished set, after every selection decision, so nothing it
// removes can be reported by the selection path. That made it the wrong place
// for the *only* copy of a rule: until #398 it was the only thing keeping
// .gitignore, .gitattributes and .gitmodules out, and a "files" entry naming one
// was refused in silence. hardReservedExcludes now carries all three, so the
// selection path refuses them itself and warns, and this pass is a second
// answer to a question already settled rather than the first.
//
// Measured on 2026-08-25 by commenting this call out of Pack. What was
// established, rather than what the absence of output suggested: `go vet ./...`
// exited 0 first, so the package and its test files still type-check with the
// call gone; then `go test -count=1` printed `ok` with a duration for
// internal/pack and for tests, not `FAIL <package> [build failed]`, so the two
// test binaries were built and run. Nothing in the suite depends on this pass
// for those three names. Do not repeat the experiment without the vet step —
// docs/agents/verification-discipline.md records what a revert that never built
// looks like when it is read as a green result.
//
// Being a second answer is the point rather than a redundancy to tidy away. The
// .git directory half below still answers for spellings the built-in patterns
// do not: measured on 2026-08-25, isHardReserved(`src\.git\config`) is false —
// ".git" is separator-free so it is matched against filepath.Base, which on a
// backslash-separated string is not ".git" — while isGitRelatedPath is true.
// The walk never produces such a path, since collectFiles calls filepath.ToSlash
// first, which is exactly why this pass is worth keeping for a set that reached
// Pack by some other route. TestFilterGitFiles and
// TestGitMetadataTierAgreesWithTheGitSafetyFilter pin the two halves.
func filterGitFiles(files []*FileInfo) []*FileInfo {
	filtered := make([]*FileInfo, 0, len(files))
	removedCount := 0

	for _, f := range files {
		if isGitRelatedPath(f.RelPath) {
			debug.Logf("pack: safety filter removed: %s", f.RelPath)
			removedCount++
			continue
		}
		filtered = append(filtered, f)
	}

	if removedCount > 0 {
		debug.Logf("pack: safety filter removed %d git-related files", removedCount)
	}

	return filtered
}

// isGitRelatedPath checks if a path is related to .git directories
func isGitRelatedPath(relPath string) bool {
	// Normalize path separators
	normalized := filepath.ToSlash(strings.ToLower(relPath))

	// Check for exact .git match or .git prefix
	if normalized == ".git" {
		return true
	}

	// Check if path contains .git directory
	if strings.HasPrefix(normalized, ".git/") {
		return true
	}

	// Check if .git appears anywhere in the path
	if strings.Contains(normalized, "/.git/") || strings.Contains(normalized, "\\.git\\") {
		return true
	}

	// Check for git-related files. These three are in hardReservedExcludes
	// since #398, spelled as bare names there so that list matches a basename at
	// any depth exactly as this branch does; see that list's git metadata block.
	// Adding a name here without adding it there puts the silent-drop bug back.
	base := filepath.Base(normalized)
	if base == ".gitignore" || base == ".gitattributes" || base == ".gitmodules" {
		return true
	}

	return false
}
