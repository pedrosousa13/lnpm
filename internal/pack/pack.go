package pack

import (
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
// npm's own set is package.json, README*, LICENSE*/LICENCE* and nothing else;
// README's npm-divergence bullet records these three as deliberate.
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
// isDefaultInclude lower-cases both the pattern and the path, so one spelling
// per entry answers for every casing. That is why the generated entries are
// spelled once while the npm block above carries each name twice: the doubling
// is redundant and predates this list's matcher growing a ToLower, and #363
// removes it. Adding cased duplicates here would only give #363 more to delete.
var defaultIncludes = append([]string{
	"package.json",
	"README*",
	"readme*",
	"LICENSE*",
	"license*",
	"LICENCE*",
	"licence*",
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
//     pnpm-lock.yaml, yarn.lock, bun.lockb. Every entry here except the last
//     block is on it, the "/**" entries being lnpm's second spelling of a name
//     already there rather than extra names.
//   - npm's "cannot be included even if specified in the files globs" list is
//     the short one, and it is the true analogue of this list: .git, .npmrc,
//     node_modules, package-lock.json, pnpm-lock.yaml, yarn.lock, bun.lockb.
//
// So lnpm is stricter than npm on five entries — .hg, .svn, CVS, .DS_Store and
// *.orig, which npm ignores by default but will include for a "files" glob —
// and looser on four, the lockfiles, which lnpm does not exclude at all. Neither
// gap is #321's to close: which names belong in the lists was out of scope, and
// only where the line falls between them was in it. Adding a name here takes a
// reason of the kind npm's short list takes — that publishing it is never what
// anyone meant — rather than a reason of the kind defaultExcludes takes.
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
// Three consequences are worth saying out loud rather than leaving to be
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
//   - ".gitignore" and ".gitattributes" are listed here, but naming one in
//     "files" does not publish it. filterGitFiles runs over the finished set in
//     Pack and drops both unconditionally, by basename, at every depth. That
//     filter predates #321 and is not part of this precedence chain.
//   - ".npmignore" is the odd one out, and the asymmetry is real rather than
//     apparent: filterGitFiles has no entry for it, so "files": [".npmignore"]
//     does publish it where the two git files above cannot be published at all.
//     TestPackFilesEntryNamingAnIgnoreFile pins all three. Publishing an
//     .npmignore is
//     harmless — it describes what was left out, not a secret — so #321 left it
//     overridable rather than hard-reserving it, which would have been a
//     membership change and out of scope. #398 tracks reconciling filterGitFiles
//     with these lists; the inconsistency lives there, not in this list.
var defaultExcludes = []string{
	".gitignore",
	".gitattributes",
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
// Known gap, pre-existing and not #321's: ".gitignore" and ".gitattributes" are
// in defaultExcludes rather than here, so naming one warns nothing — yet
// filterGitFiles still strips both from the finished set, so it still does not
// ship. That filter is a separate pass with its own list and predates this
// warning. Reconciling the two is tracked in #398.
//
// It is a plain fmt.Printf with a spelled-out "warning:" prefix for the reason
// warnMainEntryNotPacked gives below: internal/cli's icon helpers are unexported
// and internal/cli imports this package, so borrowing them would be an import
// cycle.
//
// The message quotes the entry as the manifest spells it, not the normalized
// form, because that is the string the reader will search package.json for.
func warnHardReservedFilesEntry(filesField []string) {
	for _, entry := range filesField {
		if !namesHardReserved(entry) {
			continue
		}

		fmt.Printf("warning: package.json \"files\" names %q, which lnpm never "+
			"publishes; it will not be in the package\n", entry)
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

		fmt.Printf("warning: the package's ignore file negates %q, which lnpm "+
			"never publishes; it will not be in the package\n", pattern)
	}
}

// namesHardReserved reports whether one entry a maintainer wrote — a "files"
// entry or the body of an "!" negation — plainly names a hard-reserved path.
//
// It is deliberately literal, matching npm's own reading of a "files" entry: a
// leading "/" anchors rather than rooting, and a trailing "/" marks a directory,
// so "node_modules", "/node_modules" and "node_modules/" all answer true. It
// does not try to decide whether an arbitrary glob would have selected a
// hard-reserved path — "*" against the built-in list is a question with no
// useful answer, and #321 asks only for entries that name such a path plainly.
//
// An entry that is empty once normalized ("", "/", "//") means "ship
// everything" to isIncluded rather than naming any path, so it names nothing to
// report.
//
// A "./"-prefixed entry naming a hard-reserved path at the root answers true,
// and the route is worth knowing because it is not this function's
// normalization. "./node_modules" survives the trims unchanged, and
// isHardReserved reaches applyIgnorePatterns, which matches an unanchored
// separator-free pattern against filepath.Base — and Base of "./node_modules"
// is "node_modules". So "files": ["./node_modules"] does warn. Run and
// confirmed; TestPackWarnsWhenFilesNamesHardReserved carries the row, which was
// added because nothing here exercised one and the comment claimed the
// opposite.
//
// It answers by that coincidence rather than by design, and #346 is what makes
// the distinction matter. matchFilesField now resolves a leading "./", so
// "files": ["./node_modules"] selects what "node_modules" would and is refused
// by the walk's isHardReserved check like any other entry — that row is real
// evidence for the check as of #346, where before it stayed green for want of
// anything selected at all. The two halves agree today by two different routes.
//
// Aligning the normalization here is out of scope rather than unnecessary, and
// the difference is measured, not inferred. For a root entry the spellings
// already agree: "./node_modules" and "node_modules" both true, "./.git" and
// ".git" both true, "./dist" and "dist" both false. For a *nested* entry they do
// not. namesHardReserved("node_modules/dep") is true, because applyIgnorePatterns
// prefix-matches; namesHardReserved("./node_modules/dep") is false, because that
// prefix test sees a leading "./" and the basename test sees "dep". So
// "files": ["./node_modules/dep"] is dropped in silence where the unprefixed
// spelling says so out loud — and silence is the failure mode #321 is about.
// Only the warning is affected; the walk prunes node_modules either way. #402
// carries that gap, and it belongs there rather than in a drive-by trim here.
func namesHardReserved(entry string) bool {
	normalized := strings.TrimSuffix(strings.TrimPrefix(entry, "/"), "/")
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
// packages each declare main "dist/index.js" and ship only src/index.js — there
// is no dist directory in the fixture at all — and tests/workspace_test.go
// publishes them through RunPublish with skipValidation set, so nothing had ever
// reported it. The fixtures are left as they are; the warning is the point.
//
// Note this package has no warning idiom to match. iconWarn and its siblings
// live in internal/cli/output.go and are unexported, and internal/cli imports
// this package, so borrowing them would be an import cycle. The only user-facing
// writes pack makes today are collectFiles' progress counter, which is a
// carriage-return line it erases afterwards rather than a notice. So this is a
// plain fmt.Printf with a spelled-out "warning:" prefix rather than an invented
// second icon set. If pack grows more notices than this one, the icon helpers
// are what should move to a package both can import, not be duplicated here.
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

	fmt.Printf("warning: package.json \"main\" is %q, but no such file is in the "+
		"package; the published package will not load\n", declared)
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
			// it. Both nil shapes abort, which is what keeps a file-level read
			// error out of this fix — a nil info cannot be told directory from
			// file, so the path it names may be one the package asked for.
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
			// Known gap, disclosed here because this is where it would be
			// closed. A directory with its read bit but not its execute bit
			// (mode 0444) produces no error for itself — readdir succeeds — and
			// then fails the lstat of every child, so it aborts under a "files"
			// field, where the walk descends into excluded directories, and
			// packs cleanly without one, where the prune below stops it first.
			// Mode 0000 behaves alike in both. It is *not* unclosable: the
			// child's path is known even when its info is nil, and
			// unreadableDirIsExcluded answers "excluded" for a path inside an
			// excluded subtree in both modes — run and confirmed for
			// coverage/report.html under a .gitignore naming coverage/, with no
			// "files" field and with ["dist"] — so closing it would swallow
			// nothing on a packable path. It is left open because #348 is
			// scoped to the
			// unreadable directory, and because a nil info cannot be told
			// directory from file, so the predicate would be answering about a
			// path of unknown kind — a different question from the one this code
			// answers. TestPackAbortsWhenAChildCannotBeStatted pins the current
			// behaviour so a change to it is deliberate.
			//
			// Returning nil is enough to continue. This directory's own walk
			// returns the callback's verdict unchanged, and the parent's loop
			// moves to the next sibling on a nil; filepath.SkipDir would work
			// too and says no more.
			//
			// The relPath != "." guard is not decoration. The package root's own
			// read failure has to abort — Walk hands it to the callback the same
			// way — and under a "files" field the predicate below matches each
			// entry against the root's one segment, the literal ".", which an
			// ordinary entry such as "dist" does not match. It would call the
			// package root excluded and the walk would then return no error and
			// no files.
			// TestCollectFilesAbortsWhenThePackageRootCannotBeRead pins it.
			//
			// Every error is treated alike rather than only a permission error.
			// An excluded directory contributes nothing to the package whatever
			// stopped it being read, so narrowing to os.ErrPermission would only
			// abort on the rarer causes for no gain.
			if rel, relErr := filepath.Rel(packageDir, path); relErr == nil && info != nil && info.IsDir() {
				relPath := filepath.ToSlash(rel)
				if relPath != "." && unreadableDirIsExcluded(relPath, ignores, filesField, mainEntry) {
					fmt.Printf("warning: could not read %q, which this package excludes; skipping it (%v)\n", relPath, err)
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

// unreadableDirIsExcluded reports whether a directory the walk could not read
// can be skipped: whether the package's own rules already put every path at or
// under it outside the pack. relPath is slash-separated, relative to the package
// root, and never "." — the package root itself is nothing's exclusion and its
// failure to read is a real abort.
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
// An entry that is empty once normalized — "", "/", "//" or "./" — includes
// everything, which is what npm does with it: all four ship the same files as a
// package with no "files" field. isExcluded declines to filter a path out on any
// of the four as well, so neither function drops one on the strength of a
// degenerate entry — but it reaches that answer by two routes, and only one of
// them is a degenerate skip. "", "/" and "//" are skipped there without being
// consulted: the first two hit the empty-pattern guard, and "//" hits the
// empty-directory guard inside the trailing-slash branch. "./" hits neither,
// because isExcluded resolves no leading "./" — it reads the entry as the
// directory pattern "." and simply matches nothing.
//
// A slash *run* in front of a real path is a separate question and still open:
// "//dist" normalizes to "/dist" here and selects nothing, where npm 11.16.0
// ships dist. #403.
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
	// or by naming nothing at all (the degenerate "").
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
// The branches below and their order are unchanged from the single-verdict
// isIncluded this was split out of; only the returns are, and the trailing-"/"
// branch #350 added.
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
			// Almost everything that reaches here is a glob, because the trim
			// above takes one trailing slash off an entry with no "*" — "dist/"
			// arrived as "dist" and was answered by the branches above. A slash
			// *run* is the exception: "dist//" keeps a slash and lands here. It
			// selects nothing, exactly as it did before this branch existed,
			// while npm 11.16.0 ships dist for it — run and confirmed.
			//
			// That divergence belongs to #403, which covers a slash run at
			// either end of an entry: it was filed for the leading "//dist" and
			// was broadened to this trailing case during #350, measurement
			// included. Nothing here introduced it — before #350 the same entry
			// reached filepath.Match("dist/", …) and missed just as widely. A
			// fix there has to keep this branch's rule intact, since dropping a
			// trailing slash from a *glob* would turn ["dist/**/"] into
			// ["dist/**"] and ship a subtree npm ships nothing for.
			//
			// Leaving it to the glob branch would not merely select the wrong
			// paths. doublestar.Match("dist/**/", "dist") is true where
			// filepath.Match's was false, and lastSegment("dist/**/") is "",
			// which isBareWildcard rejects — so the entry would classify as
			// filesMatchDirect against a *file* named "dist" and, under #321,
			// override defaultExcludes for it. An entry npm reads as naming
			// nothing would have become an entry naming a path. #350.

		default:
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
				// alike. "", "/", "//" and "./" reach the degenerate branch
				// above and are containment there; "*" and "**" arrive here and
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
				if isBareWildcard(lastSegment(pattern)) {
					match = filesMatchContains
				} else {
					match = filesMatchDirect
				}
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
// One leading "./" comes off, and it comes off before the "/" trim. Both halves
// of that were measured against npm 11.16.0 rather than reasoned:
//
//	["./dist"]    ships dist, exactly as ["dist"] does — #346
//	["././dist"]  ships nothing, so npm resolves one "./" and not a run
//	["/./dist"]   ships nothing, so a "./" behind the anchor is not one
//	[".//dist"]   ships dist, since the "/" trim still runs afterwards
//
// Trimming "/" first flips both of the slash-mixed rows, measured: "/./dist"
// becomes "./dist" and then "dist", selecting dist from an entry npm selects
// nothing for, while ".//dist" keeps its inner "/" and selects nothing where npm
// ships dist.
//
// A trailing "/" comes off only an entry with no "*". npm does not read a
// trailing slash on a glob as a directory marker, so "dist/**/" matches nothing
// and must not be normalized into "dist/**", which matches everything under
// dist. isIncluded's doc comment carries that measurement, and matchFilesField's
// trailing-"/" branch is what answers the glob spellings this leaves alone.
//
// Note what this deliberately does not touch. A bare "." is left alone and
// therefore still selects nothing, which looks like a bug and is not: ["."]
// ships only the always-included set under npm 11.16.0, while ["./"], [""],
// ["/"] and ["//"] all ship everything. Run and confirmed on a fixture package
// for each spelling.
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
	pattern = strings.TrimPrefix(pattern, "/")
	if !strings.Contains(pattern, "*") {
		pattern = strings.TrimSuffix(pattern, "/")
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
// Both spellings are here because both say the same thing about the name:
// nothing. They no longer reach the same distance — since #350 isIncluded globs
// with doublestar, where "*" stops at a separator and "**" spans any number of
// them — but reach is not what this decides. Classifying them alike is what
// keeps "files": ["*"] and ["**"] agreeing with the degenerate spellings of
// "ship everything", none of which publishes a default-excluded path.
//
// Before #350 the two were identical here as well: filepath.Match read "**" as
// an ordinary two-star segment, so both matched exactly one segment. The
// classification did not have to change when that did.
//
// Note that "dist/**" never reaches this: the trailing-"/**" branch above claims
// it first, and calls it containment too, so the two spellings agree by two
// different routes.
func isBareWildcard(segment string) bool {
	return segment == "*" || segment == "**"
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
// The separator check is deliberately made before filepath.Match rather than
// left to it. Some defaultIncludes entries are globs ("README*"), and
// filepath.Match reads its separator from the platform. On Linux, "*" stops at "/", so
// filepath.Match("readme*", "dist/readme.md") is false — run and confirmed — and
// dropping filepath.Base alone would look sufficient here. It would only be
// sufficient here: Windows builds path_windows.go, where the separator is "\"
// and "/" is an ordinary character.
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
	for _, f := range sorted {
		_, _ = h.Write([]byte(f.RelPath))
		_, _ = h.Write([]byte(f.ContentHash))
		_, _ = fmt.Fprintf(h, "%o", f.Mode.Perm())
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
func ExcludedByProjectRules(dir string, filesField []string, relPath string) bool {
	if len(filesField) > 0 && !isIncluded(relPath, filesField) && !isDefaultInclude(relPath) {
		return true
	}
	return isExcluded(relPath, loadIgnorePatterns(dir))
}

// filterGitFiles removes any files related to .git directories
// This is a defense-in-depth safety filter applied after all other filtering
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

	// Check for git-related files
	base := filepath.Base(normalized)
	if base == ".gitignore" || base == ".gitattributes" || base == ".gitmodules" {
		return true
	}

	return false
}
