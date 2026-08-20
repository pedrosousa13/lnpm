package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/gitignore"
	"github.com/pedrosousa13/lnpm/internal/hooks"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/pkgjson"
	"github.com/pedrosousa13/lnpm/internal/store"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// RunAdd adds a single package (for backward compatibility)
func RunAdd(packageSpec string, dev bool, pure bool, runInstall bool) error {
	return runAddSingle(packageSpec, dev, pure, runInstall, false)
}

// addResult holds the result of parallel package resolution and linking
type addResult struct {
	spec        string
	pkg         *db.Package
	linkType    link.LinkType
	origVersion string
	// rolledBack records that this package's package.json handling failed and
	// the package was undone, so every later step - the lock entry, the
	// package.json write, the database link, the success line - skips it.
	rolledBack bool
	// priorLock is the lock entry this package already had when the batch
	// started, and hadPriorLock whether there was one at all. Together they
	// say whether this invocation created the package, which is what decides
	// how far a rollback may go.
	priorLock    lockfile.Package
	hadPriorLock bool
	err          error
}

// RunAddMultiple adds multiple packages with parallel linking
func RunAddMultiple(packageSpecs []string, dev bool, pure bool, runInstall bool, useLink bool) error {
	if len(packageSpecs) == 1 {
		return runAddSingle(packageSpecs[0], dev, pure, runInstall, useLink)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	pkgJSONPath := filepath.Join(cwd, "package.json")
	if _, err := os.Stat(pkgJSONPath); err != nil {
		return fmt.Errorf("no package.json found in current directory")
	}

	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to access store: %w", err)
	}

	linker := link.New(cwd)

	// Announce live linking for the batch. The single-package path names the
	// package and version here, which this path cannot: resolution happens
	// inside the parallel phase below, and printing from those goroutines would
	// interleave. The per-package link type is reported in the summary instead.
	if useLink {
		fmt.Printf("Adding %d packages (linked to source)...\n", len(packageSpecs))
	}

	// Phase 1: Resolve and link packages in parallel
	var wg sync.WaitGroup
	results := make(chan addResult, len(packageSpecs))

	for _, spec := range packageSpecs {
		wg.Add(1)
		go func(pkgSpec string) {
			defer wg.Done()
			result := addResult{spec: pkgSpec}

			name, version := parsePackageSpec(pkgSpec)

			// The store keeps the latest published version per package
			// (latest-wins), so resolve by name and then check the requested
			// version against what's stored, same as the single-package path.
			pkg, lookupErr := database.GetPackageByName(name)
			if lookupErr != nil {
				result.err = fmt.Errorf("failed to look up package: %w", lookupErr)
				results <- result
				return
			}
			if pkg == nil {
				result.err = fmt.Errorf("package %s not found in store", name)
				results <- result
				return
			}
			if version != "" && pkg.Version != version {
				result.err = versionNotInStoreError(version, name, pkg.Version)
				results <- result
				return
			}

			result.pkg = pkg

			linkRes, err := linkPackage(database, linker, s, pkg, useLink)
			if err != nil {
				result.err = err
				results <- result
				return
			}

			result.linkType = linkRes.Type
			results <- result
		}(spec)
	}

	wg.Wait()
	close(results)

	// Collect results
	var successful []addResult
	var errors []error
	for r := range results {
		if r.err != nil {
			errors = append(errors, fmt.Errorf("%s: %w", r.spec, r.err))
		} else {
			successful = append(successful, r)
		}
	}

	if len(successful) == 0 {
		fmt.Printf("\n%s All packages failed to add:\n", iconWarn())
		for _, err := range errors {
			fmt.Printf("  - %v\n", err)
		}
		return fmt.Errorf("all packages failed to add")
	}

	// Phase 2: Sequential file updates (gitignore, package.json, lockfile, database)
	cfg := config.Get()
	if cfg.ShouldManageGitignore() {
		if added, err := gitignore.EnsureInGitignore(cwd, ".lnpm/"); err != nil {
			fmt.Printf("  %s Could not update .gitignore: %v\n", iconWarn(), err)
		} else if added {
			fmt.Printf("  %s Added .lnpm/ to .gitignore\n", iconOK())
		}
	}

	pm := config.DetectPackageManager(cwd)

	lock, err := lockfile.Load(cwd)
	if err != nil {
		return fmt.Errorf("failed to load lock file: %w", err)
	}

	// Record what the lock already held for each package before anything
	// touches it, so a rollback can tell what this invocation created from
	// what it merely re-added.
	for i := range successful {
		successful[i].priorLock, successful[i].hadPriorLock = lock.Get(successful[i].pkg.Name)
	}

	// ORDERING CONSTRAINT: the user's original specifiers exist only in
	// package.json, and writing the lnpm references overwrites them. Read them
	// and save them to the lock file BEFORE package.json is rewritten, so that
	// a failure of anything after the lock is saved - which for this path
	// includes registering the project - still leaves remove/retreat able to
	// restore the specifiers instead of deleting the dependencies. Do not move
	// the package.json writes back above lock.Save. A failure of the rewrite
	// itself needs no such rescue for a package this run added: package.json was
	// never touched, so it is rolled back instead.
	if !pure {
		for i := range successful {
			deps, err := readPackageJSONDeps(pkgJSONPath, successful[i].pkg.Name, dev)
			if err != nil {
				fmt.Printf("  %s Failed to read package.json for %s: %v\n", iconWarn(), successful[i].pkg.Name, err)
				_, rolledBackErr := rollBack(lock, linker, &successful[i], fmt.Errorf("failed to read package.json: %w", err))
				errors = append(errors, rolledBackErr)
				continue
			}
			successful[i].origVersion = deps.originalVersion
		}
	}

	// Update lockfile for all successful packages
	for _, r := range successful {
		if r.rolledBack {
			continue
		}

		// Check for existing original version in lockfile
		existingOrigVersion := ""
		if r.hadPriorLock {
			existingOrigVersion = r.priorLock.OriginalVersion
		}

		origVersion := r.origVersion
		if origVersion == "" && existingOrigVersion != "" {
			origVersion = existingOrigVersion
		}

		lock.Add(r.pkg.Name, lockfile.Package{
			Version:         r.pkg.Version,
			Hash:            r.pkg.ContentHash,
			Source:          r.pkg.SourcePath,
			Linked:          time.Now(),
			OriginalVersion: origVersion,
		})
	}

	if err := lock.Save(cwd); err != nil {
		return fmt.Errorf("failed to save lock file: %w", err)
	}

	// Update package.json for all successful packages. A failure here is found
	// with the lock already on disk, so dropping a package's entry means saving
	// the lock a second time.
	if !pure {
		lockChanged := false
		for i := range successful {
			if successful[i].rolledBack {
				continue
			}
			if err := writeLnpmReference(pkgJSONPath, successful[i].pkg.Name, dev, useLink); err != nil {
				fmt.Printf("  %s Failed to update package.json for %s: %v\n", iconWarn(), successful[i].pkg.Name, err)
				removed, rolledBackErr := rollBack(lock, linker, &successful[i], fmt.Errorf("failed to update package.json: %w", err))
				errors = append(errors, rolledBackErr)
				lockChanged = lockChanged || removed
			}
		}
		if lockChanged {
			if err := lock.Save(cwd); err != nil {
				return fmt.Errorf("failed to save lock file: %w", err)
			}
		}
	}

	// Update database for all successful packages
	proj := &db.Project{
		Path:           cwd,
		Name:           getProjectName(cwd),
		PackageManager: string(pm),
	}
	if err := database.InsertProject(proj); err != nil {
		return fmt.Errorf("failed to register project: %w", err)
	}

	existingProj, err := database.GetProjectByPath(cwd)
	if err != nil {
		return fmt.Errorf("failed to get project: %w", err)
	}

	for _, r := range successful {
		if r.rolledBack {
			continue
		}

		dbLink := &db.Link{
			PackageID: r.pkg.ID,
			ProjectID: existingProj.ID,
			LinkType:  string(r.linkType),
		}
		if err := database.InsertLink(dbLink); err != nil {
			fmt.Printf("  %s Failed to record link for %s: %v\n", iconWarn(), r.pkg.Name, err)
		}

		// Reported exactly as the single-package path reports it, so a live link
		// is spelled out rather than shown as the bare type name "link".
		fmt.Printf("%s Added %s@%s\n", iconOK(), r.pkg.Name, r.pkg.Version)
		fmt.Printf("  Link type: %s\n", linkTypeLabel(r.linkType))
	}

	if len(errors) > 0 {
		fmt.Printf("\n%s Some packages failed:\n", iconWarn())
		for _, err := range errors {
			fmt.Printf("  - %v\n", err)
		}
	}

	// Rolled-back packages are still in successful - they linked cleanly and
	// only failed afterwards - so count what actually survived before advising
	// on npm install, which has nothing to resolve if nothing was added.
	added := 0
	for _, r := range successful {
		if !r.rolledBack {
			added++
		}
	}

	// Run npm install once at the end if requested
	if runInstall && !pure && added > 0 {
		fmt.Println("\nRunning npm install...")
		if err := hooks.RunPostAdd(cwd, true); err != nil {
			fmt.Printf("  %s npm install failed: %v\n", iconWarn(), err)
		}
	} else if !pure && added > 0 {
		fmt.Printf("\n  %s Run 'npm install' if you need to resolve peer dependencies\n", iconTip())
	}

	// Exit non-zero if any package failed, so scripts can detect it.
	if len(errors) > 0 {
		return fmt.Errorf("%d of %d package(s) failed to add", len(errors), len(packageSpecs))
	}

	return nil
}

// rollBack undoes a package whose package.json handling failed, marks it so
// every later step skips it, and returns the error to record for it. It reports
// whether the lock file changed, so a caller that has already saved the lock
// knows whether it must save it again.
//
// How far the undo may go depends on whether this invocation created the
// package. With no prior lock entry the package is new to the project: the link
// phase created its .lnpm copy and node_modules symlink, and package.json still
// holds the user's own specifier because the rewrite never landed, so nothing
// about it should survive. Leaving a lock entry behind would be worse than
// useless: it would carry an empty OriginalVersion, and a later remove would
// read that as "delete the dependency" and destroy the specifier package.json
// still holds.
//
// A package added by an earlier run is left alone entirely. It must not be
// unlinked - package.json points at the .lnpm copy Unlink would delete - and
// its fresh lock entry already describes what the link phase just wrote, so
// there is nothing to undo. Only the package.json write and the database link
// are skipped, and the error is still recorded so the command exits non-zero.
func rollBack(lock *lockfile.LockFile, linker *link.Linker, r *addResult, reason error) (lockChanged bool, recorded error) {
	r.rolledBack = true
	if !r.hadPriorLock {
		lock.Remove(r.pkg.Name)
		lockChanged = true
		if err := linker.Unlink(r.pkg.Name); err != nil {
			fmt.Printf("  %s Failed to unlink %s: %v\n", iconWarn(), r.pkg.Name, err)
		}
	}
	return lockChanged, fmt.Errorf("%s: %w", r.spec, reason)
}

// linkTypeLabel renders a link type for the add summary, spelling out that a
// live link resolves to the package's source directory rather than to a copy.
func linkTypeLabel(t link.LinkType) string {
	if t == link.Live {
		return "link (live source)"
	}
	return string(t)
}

// storeFilesForLink returns the files of pkg's store entry, each carrying the
// content hash the database recorded for it.
//
// Walking the store is what says which files the entry holds; the database is
// what says what is in them. Link needs both: it compares those hashes against
// the ones it last linked into the project to decide which files it can leave
// alone, and a caller that hands it files with no hashes makes every relink
// rewrite the whole package.
//
// A file the manifest does not cover keeps an empty hash and is simply always
// rewritten, which is also why a manifest that cannot be read is logged rather
// than returned: it costs the next relink its shortcut and nothing else.
//
// A manifest that describes some other generation of the package is refused
// outright rather than used for the paths it happens to share. The package row
// and its file rows are written in one transaction now, so a mismatch is no
// longer reachable, but a database written before that was true can still hold
// one - and stamping a superseded generation's hashes onto the current store
// entry would mark a genuinely changed file reusable and carry stale content
// into the project. Recomputing the package content hash from the file rows
// costs no I/O and answers exactly that question, and falling back to hashless
// files costs one full relink.
func storeFilesForLink(database *db.DB, s *store.Store, pkg *db.Package) ([]*pack.FileInfo, error) {
	files, err := s.GetFiles(pkg.Name, pkg.ContentHash)
	if err != nil {
		return nil, err
	}

	entries, err := database.GetFilesForPackage(pkg.ID)
	if err != nil {
		debug.Logf("cli: no file manifest for %s, relinking it in full: %v", pkg.Name, err)
		return files, nil
	}
	if got := fileManifestHash(entries); got != pkg.ContentHash {
		debug.Logf("cli: file manifest for %s describes %s, not the recorded %s; relinking it in full", pkg.Name, got, pkg.ContentHash)
		return files, nil
	}

	hashes := make(map[string]string, len(entries))
	for _, e := range entries {
		hashes[e.RelativePath] = e.ContentHash
	}
	for _, f := range files {
		f.ContentHash = hashes[f.RelPath]
	}
	return files, nil
}

// fileManifestHash is the package content hash the recorded file rows describe.
//
// It is pack.HashFiles over the same three fields publish and push hash to
// produce the package row's content hash - path, content hash, permissions - so
// a manifest that belongs to the package row reproduces it exactly and one that
// belongs to another generation does not.
func fileManifestHash(entries []*db.FileEntry) string {
	infos := make([]*pack.FileInfo, len(entries))
	for i, e := range entries {
		infos[i] = &pack.FileInfo{
			RelPath:     e.RelativePath,
			ContentHash: e.ContentHash,
			Mode:        e.Mode,
		}
	}
	return pack.HashFiles(infos)
}

// linkPackage points the project at pkg. With useLink the project resolves to
// pkg's live source directory, so later source edits reach it with no further
// command; otherwise the published snapshot is materialised out of the store.
func linkPackage(database *db.DB, linker *link.Linker, s *store.Store, pkg *db.Package, useLink bool) (link.Result, error) {
	if useLink {
		linkType, err := linker.LinkSource(pkg.Name, pkg.SourcePath)
		if err != nil {
			return link.Result{}, fmt.Errorf("failed to link package source: %w", err)
		}
		return link.Result{Type: linkType}, nil
	}

	files, err := storeFilesForLink(database, s, pkg)
	if err != nil {
		return link.Result{}, fmt.Errorf("failed to get package files: %w", err)
	}

	res, err := linker.Link(pkg.Name, pkg.StorePath, files)
	if err != nil {
		return link.Result{}, fmt.Errorf("failed to link package: %w", err)
	}
	return res, nil
}

// runAddSingle adds a single package (internal implementation)
func runAddSingle(packageSpec string, dev bool, pure bool, runInstall bool, useLink bool) error {
	// Parse package spec (name[@version])
	name, version := parsePackageSpec(packageSpec)

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Validate we're in a project with package.json
	pkgJSONPath := filepath.Join(cwd, "package.json")
	if _, err := os.Stat(pkgJSONPath); err != nil {
		return fmt.Errorf("no package.json found in current directory")
	}

	// Get database
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Find package in store. The store keeps the latest published version per
	// package (latest-wins), so a requested version must match what's stored.
	pkg, err := database.GetPackageByName(name)
	if err != nil {
		return fmt.Errorf("failed to look up package: %w", err)
	}
	if pkg == nil {
		return fmt.Errorf("package %s not found in store. Did you run 'lnpm publish' in the package directory?", name)
	}
	if version != "" && pkg.Version != version {
		return versionNotInStoreError(version, name, pkg.Version)
	}

	if useLink {
		fmt.Printf("Adding %s@%s (linked to source)...\n", pkg.Name, pkg.Version)
	} else {
		fmt.Printf("Adding %s@%s...\n", pkg.Name, pkg.Version)
	}

	// Get store
	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to access store: %w", err)
	}

	// Link the package
	linker := link.New(cwd)
	linkRes, err := linkPackage(database, linker, s, pkg, useLink)
	if err != nil {
		return err
	}
	linkType := linkRes.Type

	// Update .gitignore if enabled
	cfg := config.Get()
	if cfg.ShouldManageGitignore() {
		if added, err := gitignore.EnsureInGitignore(cwd, ".lnpm/"); err != nil {
			fmt.Printf("  %s Could not update .gitignore: %v\n", iconWarn(), err)
		} else if added {
			fmt.Printf("  %s Added .lnpm/ to .gitignore\n", iconOK())
		}
	}

	// Detect package manager
	pm := config.DetectPackageManager(cwd)

	// Load lock file first to check for existing original version
	lock, err := lockfile.Load(cwd)
	if err != nil {
		return fmt.Errorf("failed to load lock file: %w", err)
	}

	// Check if we already have an original version saved from a previous add
	var existingOriginalVersion string
	if existing, ok := lock.Get(pkg.Name); ok && existing.OriginalVersion != "" {
		existingOriginalVersion = existing.OriginalVersion
	}

	// ORDERING CONSTRAINT: the user's original specifier exists only in
	// package.json, and writing the lnpm reference overwrites it. Read it and
	// save it to the lock file BEFORE package.json is rewritten, so that a
	// failure of the rewrite - or of anything after it - still leaves
	// remove/retreat able to restore the specifier instead of deleting the
	// dependency. Do not move the package.json write back above lock.Save.
	var originalVersion string
	if !pure {
		deps, err := readPackageJSONDeps(pkgJSONPath, pkg.Name, dev)
		if err != nil {
			return fmt.Errorf("failed to read package.json: %w", err)
		}
		originalVersion = deps.originalVersion
	}

	// Use existing original version if we didn't find one (re-add scenario)
	if originalVersion == "" && existingOriginalVersion != "" {
		originalVersion = existingOriginalVersion
	}

	lock.Add(pkg.Name, lockfile.Package{
		Version:         pkg.Version,
		Hash:            pkg.ContentHash,
		Source:          pkg.SourcePath,
		Linked:          time.Now(),
		OriginalVersion: originalVersion,
	})

	if err := lock.Save(cwd); err != nil {
		return fmt.Errorf("failed to save lock file: %w", err)
	}

	// Update package.json (unless --pure)
	if !pure {
		if err := writeLnpmReference(pkgJSONPath, pkg.Name, dev, useLink); err != nil {
			return fmt.Errorf("failed to update package.json: %w", err)
		}
	}

	// Register project and link in database
	proj := &db.Project{
		Path:           cwd,
		Name:           getProjectName(cwd),
		PackageManager: string(pm),
	}
	if err := database.InsertProject(proj); err != nil {
		return fmt.Errorf("failed to register project: %w", err)
	}

	// Get the project ID (might be new or existing)
	existingProj, err := database.GetProjectByPath(cwd)
	if err != nil {
		return fmt.Errorf("failed to get project: %w", err)
	}

	dbLink := &db.Link{
		PackageID: pkg.ID,
		ProjectID: existingProj.ID,
		LinkType:  string(linkType),
	}
	if err := database.InsertLink(dbLink); err != nil {
		return fmt.Errorf("failed to record link: %w", err)
	}

	fmt.Printf("%s Added %s@%s\n", iconOK(), pkg.Name, pkg.Version)
	fmt.Printf("  Link type: %s\n", linkTypeLabel(linkType))
	fmt.Printf("  Package manager: %s\n", pm)
	if !pure {
		fmt.Printf("  Updated: package.json\n")
	}

	// Run npm install if --install flag was passed
	// By default, don't run (matches yalc behavior)
	if runInstall && !pure {
		if err := hooks.RunPostAdd(cwd, true); err != nil {
			fmt.Printf("  %s npm install failed: %v\n", iconWarn(), err)
		}
	} else if !pure {
		fmt.Printf("\n  %s Run 'npm install' if you need to resolve peer dependencies\n", iconTip())
	}

	return nil
}

// parsePackageSpec parses a package spec like "name" or "name@version"
func parsePackageSpec(spec string) (name, version string) {
	// Handle scoped packages (@org/name@version)
	if strings.HasPrefix(spec, "@") {
		// Find second @ for version
		idx := strings.LastIndex(spec, "@")
		if idx > 0 && idx != strings.Index(spec, "@") {
			return spec[:idx], spec[idx+1:]
		}
		return spec, ""
	}

	// Regular package (name@version)
	if idx := strings.Index(spec, "@"); idx > 0 {
		return spec[:idx], spec[idx+1:]
	}
	return spec, ""
}

// versionNotInStoreError reports that the store holds a different version of
// name than the one the user asked for, shared by the parallel and the
// single-package add paths so both tell the user the same thing.
//
// The parameters are declared in the order the message reads them. All three
// are plain strings, so a caller that swaps two still compiles and quietly
// produces a message that lies about which version is which; keeping
// declaration order and interpolation order identical is what makes such a
// swap visible at the call site.
//
// The remedy is a trailing parenthetical rather than a second sentence: the
// parallel path wraps this error as "<spec>: %w", so the string has to compose
// as a clause, and a terminal period would read badly there as well as trip
// staticcheck's ST1005.
func versionNotInStoreError(requested, name, stored string) error {
	return fmt.Errorf("version %s of %s not found in store (latest published is %s; re-publish %s to update)", requested, name, stored, name)
}

// packageJSONDeps is what reading package.json tells us about a dependency
// before the lnpm reference is written: the file's raw bytes, the field the
// reference belongs in, and the specifier the user had there. The
// field-selection rules live here alone so the read half and the write half
// cannot drift apart. The raw bytes are kept because the write splices them
// rather than re-serializing a parsed document, which is what preserves the
// author's key order and formatting.
type packageJSONDeps struct {
	src             []byte
	field           string
	originalVersion string
}

// otherField is the dependency field the entry does NOT belong in; the write
// folds any entry found there into the chosen field.
func (p *packageJSONDeps) otherField() string {
	if p.field == "devDependencies" {
		return "dependencies"
	}
	return "devDependencies"
}

// readPackageJSONDeps parses package.json and works out what writing the lnpm
// reference would do, without writing anything. Callers can therefore persist
// originalVersion to the lock file before the rewrite is attempted.
func readPackageJSONDeps(path string, packageName string, dev bool) (*packageJSONDeps, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return nil, err
	}

	// Check where package currently exists
	depsMap, _ := pkgJSON["dependencies"].(map[string]interface{})
	devDepsMap, _ := pkgJSON["devDependencies"].(map[string]interface{})

	inDeps := depsMap != nil && depsMap[packageName] != nil
	inDevDeps := devDepsMap != nil && devDepsMap[packageName] != nil

	// Determine which field to use:
	// - If --dev flag: use devDependencies
	// - Else if package exists in devDependencies: use devDependencies (preserve location)
	// - Else: use dependencies (default)
	deps := &packageJSONDeps{src: data, field: "dependencies"}
	if dev || (!inDeps && inDevDeps) {
		deps.field = "devDependencies"
	}

	// Original version from the target field (ignore lnpm references)
	if target, ok := pkgJSON[deps.field].(map[string]interface{}); ok {
		if v, ok := target[packageName].(string); ok && !isLnpmReference(v) {
			deps.originalVersion = v
		}
	}

	// Also check other field for original version
	if otherDeps, ok := pkgJSON[deps.otherField()].(map[string]interface{}); ok {
		if v, ok := otherDeps[packageName].(string); ok {
			if deps.originalVersion == "" && !isLnpmReference(v) {
				deps.originalVersion = v
			}
		}
	}

	return deps, nil
}

// write splices the lnpm dependency into the file's original bytes and saves it,
// so that every key lnpm did not touch keeps its position and formatting. When
// useLink is true the dependency uses the "link:" protocol (symlink-style
// resolution, which helps pnpm/yarn dedupe peer deps) instead of the default
// "file:".
func (p *packageJSONDeps) write(path string, packageName string, useLink bool) error {
	// Remove from other field to avoid duplicate entries
	output, err := pkgjson.RemoveDep(p.src, p.otherField(), packageName)
	if err != nil {
		return err
	}

	// Set the lnpm reference (file: by default, link: when requested)
	protocol := "file"
	if useLink {
		protocol = "link"
	}
	output, err = pkgjson.SetDep(output, p.field, packageName, fmt.Sprintf("%s:.lnpm/%s", protocol, packageName))
	if err != nil {
		return err
	}

	return os.WriteFile(path, pkgjson.EnsureTrailingNewline(output), 0644)
}

// writeLnpmReference points package.json at the linked copy of packageName.
//
// It re-reads the file rather than reusing an earlier read, so that writing
// several packages in sequence does not have each write clobber the previous
// one with a stale document.
func writeLnpmReference(path string, packageName string, dev bool, useLink bool) error {
	deps, err := readPackageJSONDeps(path, packageName, dev)
	if err != nil {
		return err
	}
	return deps.write(path, packageName, useLink)
}

// isLnpmReference checks if a version string is an lnpm reference (file:.lnpm/ or link:.lnpm/)
func isLnpmReference(version string) bool {
	return strings.HasPrefix(version, "file:.lnpm/") || strings.HasPrefix(version, "link:.lnpm/")
}

// getProjectName extracts project name from package.json or directory name
func getProjectName(projectPath string) string {
	pkgJSONPath := filepath.Join(projectPath, "package.json")
	data, err := os.ReadFile(pkgJSONPath)
	if err == nil {
		var pkgJSON struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &pkgJSON) == nil && pkgJSON.Name != "" {
			return pkgJSON.Name
		}
	}
	return filepath.Base(projectPath)
}
