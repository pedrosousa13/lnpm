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

// defaultIncludes are files always included regardless of config
var defaultIncludes = []string{
	"package.json",
	"README*",
	"readme*",
	"LICENSE*",
	"license*",
	"LICENCE*",
	"licence*",
	"CHANGELOG*",
	"changelog*",
	"CHANGES*",
	"changes*",
	"HISTORY*",
	"history*",
}

// defaultExcludes are patterns always excluded
var defaultExcludes = []string{
	".git",
	".git/**",
	".gitignore",
	".gitattributes",
	".hg",
	".hg/**",
	".svn",
	".svn/**",
	"CVS",
	"CVS/**",
	".DS_Store",
	"Thumbs.db",
	"node_modules",
	"node_modules/**",
	".npmrc",
	".npmignore",
	".yalc",
	".yalc/**",
	".lnpm",
	".lnpm/**",
	"lnpm.lock",
	// The snapshot `lnpm retreat` leaves in place of the lock file. The
	// documented publish flow is retreat, then publish, so it is sitting in the
	// package root at exactly this moment, and it records an absolute source
	// path per linked package. The patterns here are literal, not prefixes, so
	// "lnpm.lock" above does not cover it.
	lockfile.RetreatFileName,
	"yalc.lock",
	"*.log",
	"*.orig",
	"*.swp",
	"*.swo",
	"*~",
	".env",
	".env.*",
	"*.tgz",
}

// lowerDefaultExcludes is defaultExcludes lower-cased once, for the
// case-insensitive matching isDefaultExcluded does. It is derived rather than
// written out so the two lists cannot drift: a new entry added above is folded
// here whatever case it is spelled in.
//
// Lowering the patterns and not just the path is safe because no entry carries a
// character class, brace or escape — the invariant
// docs/adr/0003-ignore-patterns-glob-with-doublestar-syntax-and-all.md already
// records, there for keeping the move to doublestar clear of this list. It
// matters again here for a different reason: strings.ToLower would rewrite
// "[A-Z]" into "[a-z]" and "\A" into "\a", changing what the pattern matches
// rather than how it is cased. TestDefaultExcludesHoldNoMetacharactersLoweringWouldRewrite
// pins it.
var lowerDefaultExcludes = func() []string {
	lowered := make([]string, len(defaultExcludes))
	for i, pattern := range defaultExcludes {
		lowered[i] = strings.ToLower(pattern)
	}
	return lowered
}()

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
	files, err := collectFiles(packageDir, pkgJSON.Files, mainEntryPath(pkgJSON.Main))
	if err != nil {
		return nil, nil, err
	}

	// Apply explicit .git safety filter (defense in depth)
	files = filterGitFiles(files)

	debug.Logf("pack: collected %d files (after safety filters)", len(files))

	return pkgJSON, files, nil
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
// with '/' (internal/filepathlite/path.go), and Separator is '\\' only on
// Windows. So on Windows both sides fold and main "lib\\index.js" matches the
// walked lib/index.js, while on Linux neither side folds and a file whose name
// genuinely contains a backslash still matches a main spelled the same way. The
// Linux half is run — filepath.ToSlash(`lib\index.js`) came back unchanged; the
// Windows half is read off that stdlib source and can only be exercised by the
// Windows CI job.
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
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, "/") {
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
	// down to each path rather than the package root alone. They are kept apart
	// from defaultExcludes because the two rank differently against the "files"
	// whitelist: the user's patterns lose to it, defaultExcludes beat it.
	ignores := newIgnoreLoader(packageDir)

	// If files field is specified, use whitelist mode
	useWhitelist := len(filesField) > 0

	// First pass: collect all files
	err := filepath.Walk(packageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
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

		// Check if excluded. defaultExcludes are checked on their own and
		// first, because nothing overrides them — not a user "!" negation, not
		// the "files" field.
		if isDefaultExcluded(relPath) {
			if info.IsDir() {
				// Nothing inside a default-excluded directory can be wanted, so
				// the walk never descends. This is what keeps a large
				// node_modules off the walk entirely.
				return filepath.SkipDir
			}
			return nil
		}

		// The user's ignore patterns decide the whole tree only when there is no
		// whitelist. With one they drop nothing here, and are consulted again
		// further down for default includes alone — a "files" entry may be a
		// glob, so which paths under an ignored directory it selects cannot be
		// known without descending into it and asking isIncluded per file.
		// Walking into ignored directories costs something in whitelist mode;
		// publishing an empty package because .gitignore held the build output
		// costs more.
		if !useWhitelist {
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
			case mainEntry != "" && relPath == mainEntry:
				// The manifest's entry point, which ships whatever the
				// whitelist says (#319).
				//
				// Decision: main is exempt from the user's ignore patterns too,
				// not from the whitelist alone. Rejected: treating it like a
				// default include below, where an ignore pattern still drops it
				// and README documented that as the rule. That reading leaves
				// #319's own failure reachable — an .npmignore naming the entry
				// point drops it, publish reports success, and requiring the
				// package fails on the path the manifest advertises. The
				// publish-time check does not catch it either: validation.
				// ValidatePackage only stats main on disk, so a main that exists
				// but is not packed passes validation and ships broken. README's
				// divergence list is updated to match.
				//
				// The two differ in what dropping them costs, which is what
				// makes them worth treating differently: a package missing its
				// README is merely poorer, a package missing its main does not
				// load. Overriding an explicit ignore rule is a real surprise,
				// and it is the smaller one.
				//
				// The boundary in full: main beats the "files" whitelist and
				// the user's ignore patterns, and loses to defaultExcludes.
				// That split is the one isDefaultExcluded already argues — the
				// user's patterns are a preference the user expressed, the
				// built-in list is a guard lnpm applies on the user's behalf,
				// and a guard that can be stepped around by naming the file in
				// "main" is not a guard. It holds structurally rather than by a
				// check here: isDefaultExcluded is evaluated in the walk above
				// and returns early, so .env named as "main" never reaches this
				// switch. TestPackMainCannotDefeatDefaultExcludes pins it and
				// goes red if the force-include is hoisted above that check.
				//
				// This sits inside the whitelist branch on purpose. A package
				// with no "files" field is left exactly as it was — there the
				// user's patterns decide the whole tree, main included.
				// TestPackMainRespectsIgnorePatternsWithoutFilesWhitelist pins
				// that, and fails if this case is hoisted above the branch.
			case isIncluded(relPath, filesField):
				// npm documents that a file included with the "files" field
				// cannot be excluded through .npmignore or .gitignore, so the
				// user's ignore patterns are not consulted at all here.
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
	excluded := false
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
// earlier pattern excluded. collectFiles evaluates defaultExcludes in a call of
// their own, which is what keeps a user negation from re-including a
// default-excluded path such as .env or node_modules.
func isExcluded(relPath string, patterns []string) bool {
	return applyIgnorePatterns(relPath, patterns, false)
}

// isDefaultExcluded reports whether the built-in force-exclude set covers
// relPath, matching case-insensitively.
//
// defaultExcludes is a guard lnpm applies on the user's behalf rather than a
// preference the user expressed, and a guard that can be stepped around by
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
// precisely because it does match basenames and must suppress that. Teaching
// isIncluded to match basenames (npm's "files" does match a bare "*.md"
// against a nested path) would mean handling anchoring here too.
//
// The two also glob through different engines, which is the more surprising
// half of the asymmetry. isExcluded matches with doublestar, where "**" spans
// zero or more path segments; isIncluded still uses filepath.Match, where "*"
// never crosses a separator and "**" is understood only as the trailing "/**"
// handled below. So "lib/**/*.js" excludes lib/top.js as an ignore pattern but
// does not include it as a "files" entry.
//
// A trailing "/" marks a directory whose contents are included, and is dropped
// only from patterns with no glob metacharacter. npm does not read a trailing
// slash on a glob as a directory marker, so "dist/**/" matches nothing and
// must not be normalized into "dist/**", which matches everything under dist.
//
// So "dist", "/dist", "dist/" and "/dist/" are all equivalent, as they are to
// npm, and "dist/**" and "/dist/**" are equivalent to each other. "dist/**/"
// is equivalent to none of them.
//
// An entry that is empty once normalized — "", "/" or "//" — includes
// everything, which is what npm does with it: all three ship the same files as
// a package with no "files" field. isExcluded skips such a pattern for the same
// reason, so neither function filters a path out on the strength of one.
func isIncluded(relPath string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimPrefix(pattern, "/")
		if !strings.Contains(pattern, "*") {
			pattern = strings.TrimSuffix(pattern, "/")
		}

		if pattern == "" {
			return true
		}

		// Direct match
		if pattern == relPath {
			return true
		}

		// Directory match - if pattern is "dist", include "dist/anything"
		if strings.HasPrefix(relPath, pattern+"/") {
			return true
		}

		// Handle ** patterns
		if strings.HasSuffix(pattern, "/**") {
			prefix := strings.TrimSuffix(pattern, "/**")
			if relPath == prefix || strings.HasPrefix(relPath, prefix+"/") {
				return true
			}
			continue
		}

		// Glob match
		if matched, _ := filepath.Match(pattern, relPath); matched {
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
// The separator check is deliberately made before filepath.Match rather than
// left to it. defaultIncludes entries are globs ("README*"), and filepath.Match
// reads its separator from the platform. On Linux, "*" stops at "/", so
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
// Rejected: anchoring with strings.HasPrefix(relPath, pattern). It cannot
// express the "*" the entries are written with — HasPrefix compares "*" as an
// ordinary byte, so only the "package.json" entry could ever fire, and it would
// fire on "package.jsonc" too. Stripping the "*" and prefix-matching the stem
// repairs that and reintroduces the bug this function exists to fix: "changes"
// then prefix-matches "changes/secret.txt". Both spellings were run against the
// table in TestIsDefaultInclude; the package.jsonc and changes/secret.txt rows
// are what fail them.
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
// It deliberately does not consult defaultExcludes. Those are lnpm's additions
// to npm's rules, and this answers what a tool that reads only the project's own
// rules would ship - which is what `npm publish` is. `lnpm check` uses it to ask
// whether a file lnpm left in the project root is about to be published by
// something other than lnpm.
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
