package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/fsutil"
	"github.com/pedrosousa13/lnpm/internal/gitignore"
	"github.com/pedrosousa13/lnpm/internal/hooks"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/pkgjson"
	"github.com/pedrosousa13/lnpm/internal/store"
	"github.com/pedrosousa13/lnpm/internal/ui"
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
	// tag is the channel the spec asked for, empty for the default one. It is
	// recorded on the link so that moving latest later does not drag a
	// consumer that asked for another channel onto it.
	tag string
	// pinned says the spec named a build rather than a channel, so the link
	// follows nothing and only the user moves it off. See pinsTheLink.
	pinned bool
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

			// Resolved exactly as the single-package path resolves it: a tag
			// first, then an exact version, then a content-hash prefix.
			pkg, tag, kind, lookupErr := resolveAddSpec(database, name, version)
			if lookupErr != nil {
				result.err = lookupErr
				results <- result
				return
			}
			if pkg == nil {
				result.err = fmt.Errorf("package %s not found in store", name)
				results <- result
				return
			}

			result.pkg = pkg
			result.tag = tag
			result.pinned = pinsTheLink(kind)

			if useLink && kind != specDefault {
				result.err = liveLinkSpecError(name, version, kind)
				results <- result
				return
			}

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
		fmt.Printf("\n%s All packages failed to add:\n", ui.IconWarn())
		for _, err := range errors {
			fmt.Printf("  - %v\n", err)
		}
		return fmt.Errorf("all packages failed to add")
	}

	// Phase 2: Sequential file updates (gitignore, package.json, lockfile, database)
	cfg := config.Get()
	if cfg.ShouldManageGitignore() {
		if added, err := gitignore.EnsureInGitignore(cwd, ".lnpm/"); err != nil {
			fmt.Printf("  %s Could not update .gitignore: %v\n", ui.IconWarn(), err)
		} else if added {
			fmt.Printf("  %s Added .lnpm/ to .gitignore\n", ui.IconOK())
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
				fmt.Printf("  %s Failed to read package.json for %s: %v\n", ui.IconWarn(), successful[i].pkg.Name, err)
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
			Pinned:          r.pinned,
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
				fmt.Printf("  %s Failed to update package.json for %s: %v\n", ui.IconWarn(), successful[i].pkg.Name, err)
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
			Tag:       r.tag,
			Pinned:    r.pinned,
		}
		// A link that cannot be recorded is a failure, not a warning to add
		// beneath a success line. The files are in the project either way, but
		// without the row nothing knows the project consumes this package: gc
		// reads the store entry as unreferenced and deletes what the project is
		// importing. Warning and carrying on printed "Added" over exactly that
		// state - the fail-open shape #329 removed from gc, arriving instead
		// through the write that feeds it.
		if err := database.InsertLink(dbLink); err != nil {
			fmt.Printf("  %s Failed to record link for %s: %v\n", ui.IconWarn(), r.pkg.Name, err)
			errors = append(errors, fmt.Errorf("%s: failed to record the link: %w", r.pkg.Name, err))
			continue
		}

		// Reported exactly as the single-package path reports it, so a live link
		// is spelled out rather than shown as the bare type name "link".
		fmt.Printf("%s Added %s@%s%s\n", ui.IconOK(), r.pkg.Name, r.pkg.Version, linkSuffix(r.tag, r.pinned))
		fmt.Printf("  Link type: %s\n", linkTypeLabel(r.linkType))
	}

	if len(errors) > 0 {
		fmt.Printf("\n%s Some packages failed:\n", ui.IconWarn())
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
			fmt.Printf("  %s npm install failed: %v\n", ui.IconWarn(), err)
		}
	} else if !pure && added > 0 {
		fmt.Printf("\n  %s Run 'npm install' if you need to resolve peer dependencies\n", ui.IconTip())
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
			fmt.Printf("  %s Failed to unlink %s: %v\n", ui.IconWarn(), r.pkg.Name, err)
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
// content hash and permissions the database recorded for it.
//
// A store entry that is not one the store committed is refused outright, by
// GetFiles rather than here — add, pull and publish's relink all arrive through
// this function, and before #330 all three linked whatever an interrupted gc
// had left behind. Nothing here can repair such an entry, so the error is
// passed up and the command stops.
//
// Walking the store is what says which files the entry holds; the database is
// what says what is in them. Link needs both: it compares those hashes against
// the ones it last linked into the project to decide which files it can leave
// alone, and a caller that hands it files with no hashes makes every relink
// rewrite the whole package.
//
// The mode comes from the same row as the hash, and not from the store file's
// own stat, so that this and push - which links from pack.FileInfoFromStore over
// the same recorded rows - describe a file identically. The two are compared
// against each other across a command boundary, through the manifest a link
// writes and the next one reads, and a mode that disagreed would mark every file
// changed. Statting the store agrees only for as long as the store's copy holds
// exactly the permissions the file was published with, which entries written
// before copyFile chmod'd past the umask do not.
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
		return nil, storeReadError(pkg, err)
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

	recorded := make(map[string]*db.FileEntry, len(entries))
	for _, e := range entries {
		recorded[e.RelativePath] = e
	}
	for _, f := range files {
		e, ok := recorded[f.RelPath]
		if !ok {
			continue
		}
		f.ContentHash = e.ContentHash
		f.Mode = e.Mode
	}
	return files, nil
}

// storeReadError turns a failure to read pkg's store entry into something the
// user can act on.
//
// Two things get added here rather than in the store. The version, because
// entries are addressed by name and content hash and the store does not know
// it - a user told only that "left-pad" is damaged still has to work out which
// build to re-publish. And the remediation, because it depends on whether the
// entry directory survives: Store never renames over an occupied destination,
// so a damaged directory has to be removed before a re-publish of the same
// content can land, while one that is simply gone needs nothing but the
// re-publish. The store's own error stays a statement of the fault.
//
// The interrupted-gc wording is on the damaged branch only. A missing entry
// has other everyday causes - a gc that finished, a store restored from a
// backup - and naming one cause for it would be a guess.
func storeReadError(pkg *db.Package, err error) error {
	var incomplete *store.IncompleteEntryError
	if !errors.As(err, &incomplete) {
		return fmt.Errorf("%s@%s: %w", pkg.Name, pkg.Version, err)
	}
	if !incomplete.Present {
		return fmt.Errorf("%s@%s: %w; re-publish %s to rebuild it", pkg.Name, pkg.Version, err, pkg.Name)
	}
	return fmt.Errorf("%s@%s: %w; an interrupted 'lnpm gc' or publish can leave one like this, and lnpm will not use or delete it: remove %s and re-publish %s to rebuild it",
		pkg.Name, pkg.Version, err, incomplete.Path, pkg.Name)
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

	// Find the version in the store: a tag if the spec names one, then an exact
	// version against the whole retained history, then a content-hash prefix.
	pkg, tag, kind, err := resolveAddSpec(database, name, version)
	if err != nil {
		return err
	}
	if pkg == nil {
		return fmt.Errorf("package %s not found in store. Did you run 'lnpm publish' in the package directory?", name)
	}

	if useLink && kind != specDefault {
		return liveLinkSpecError(name, version, kind)
	}

	// A spec that named a build rather than a channel pins the link. --link is
	// already refused above for every such spec, so a live link never pins.
	pinned := pinsTheLink(kind)

	if useLink {
		fmt.Printf("Adding %s@%s (linked to source%s)...\n", pkg.Name, pkg.Version, tagClause(tag))
	} else {
		fmt.Printf("Adding %s@%s%s...\n", pkg.Name, pkg.Version, linkSuffix(tag, pinned))
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
			fmt.Printf("  %s Could not update .gitignore: %v\n", ui.IconWarn(), err)
		} else if added {
			fmt.Printf("  %s Added .lnpm/ to .gitignore\n", ui.IconOK())
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
		Pinned:          pinned,
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
		Tag:       tag,
		Pinned:    pinned,
	}
	if err := database.InsertLink(dbLink); err != nil {
		return fmt.Errorf("failed to record link: %w", err)
	}

	fmt.Printf("%s Added %s@%s%s\n", ui.IconOK(), pkg.Name, pkg.Version, linkSuffix(tag, pinned))
	fmt.Printf("  Link type: %s\n", linkTypeLabel(linkType))
	fmt.Printf("  Package manager: %s\n", pm)
	if !pure {
		fmt.Printf("  Updated: package.json\n")
	}

	// Run npm install if --install flag was passed
	// By default, don't run (matches yalc behavior)
	if runInstall && !pure {
		if err := hooks.RunPostAdd(cwd, true); err != nil {
			fmt.Printf("  %s npm install failed: %v\n", ui.IconWarn(), err)
		}
	} else if !pure {
		fmt.Printf("\n  %s Run 'npm install' if you need to resolve peer dependencies\n", ui.IconTip())
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

// specKind says how a spec's @suffix resolved.
//
// The returned tag cannot carry this. A tag comes back as itself, but a version
// and a hash both come back with an empty tag, because they name a build rather
// than a channel - and so does a bare name. Anything reading the tag string to
// decide what the user asked for therefore cannot tell "nothing" from "this
// exact build", which is the distinction --link turns on.
//
// The kinds are strings rather than iota constants so that the zero value is
// none of them: a path that forgot to report a kind then reads as "not a bare
// name", which is the refusing side of every guard below.
type specKind string

const (
	specDefault specKind = "default" // no @suffix: the default channel
	specTag     specKind = "tag"
	specVersion specKind = "version"
	specHash    specKind = "hash"
)

// pinsTheLink reports whether a spec that resolved this way names a build rather
// than a channel, which is what a pin records. See ADR-0006.
//
// It keys off the kind and not off "was it a hash" deliberately. A version and a
// hash both name one build, and resolveAddSpec returns an empty tag for both, so
// the returned tag cannot tell them from a bare name. A version is also the
// spelling most people type - `lnpm list <pkg> --versions` prints both, and the
// README offers both as the way to roll back - so pinning only the hash would
// leave the common rollback exactly as exposed to the next pull as it was
// before.
//
// specDefault and specTag do not pin, and the difference is who moves the link
// afterwards. A channel is meant to carry a consumer along it; a build is a
// consumer saying they want this one and no other. That is why the bare `lnpm
// add <pkg>` this returns false for is the unpin.
func pinsTheLink(kind specKind) bool {
	return kind == specVersion || kind == specHash
}

// linkSuffix names what a link follows, as a trailing parenthetical for a line
// that has none of its own: the tag it was added under, or the pin that says it
// follows no channel at all.
//
// The two cannot both be spelled out, and by construction never both apply: a
// pin comes only from a spec that named a build, which resolves with no tag. A
// pin is said out loud where the default tag is left unsaid, because a pin
// changes what every later `lnpm pull` does to this package - it is a decision
// someone made, which is the same test tagNote applies to a tag.
func linkSuffix(tag string, pinned bool) string {
	if pinned {
		return " (pinned)"
	}
	return tagSuffix(tag)
}

// minHashPrefix is the shortest @suffix the hash step will consider, borrowed
// from git's minimum for an abbreviated object name.
//
// Without a floor, `lnpm add mylib@2` - a user who means version 2 - is matched
// against content hashes, and a single retained hash beginning with "2" resolves
// it to that build. Nothing about the spec says the user meant a hash, so a
// short suffix that matches no version has to reach the dead end that names the
// retained versions rather than a verdict about hash prefixes.
const minHashPrefix = 4

// resolveAddSpec works out which version of name an add should link, which
// channel the resulting link follows, and how the spec resolved.
//
// requested is whatever followed the @ in the spec, and it is matched in three
// steps, most specific first:
//
//  1. a dist-tag, so `pkg@beta` links the build beta names. A tag is a name a
//     person chose for this package, so it wins even when it is spelled like a
//     version or a hash;
//  2. an exact version string, against every version the store has retained -
//     not only the one the default tag names, which is all this could see while
//     the store held one record per name;
//  3. a content-hash prefix of at least minHashPrefix characters, against the
//     same set.
//
// Steps 2 and 3 are what makes a rollback possible: `lnpm list <pkg> --versions`
// prints a version and an eight-character short hash per row, and both have to
// be typeable back. Resolving only the hash would mean showing a user two
// identifiers and accepting one of them.
//
// Widening step 2 past the default tag cannot change an answer that already
// existed. A version spec used to resolve only when it matched what the default
// tag names, and that record is still preferred when several carry the version -
// which two publishes can produce, since package.json can be kept out of a pack.
// Beyond that preference an ambiguous spec is refused, never guessed: this is
// the command reached for when a release broke something, so choosing between
// two builds on the user's behalf is the last thing it should do. A hash prefix
// has no such preference, because a prefix is not a claim about which version
// was meant - any prefix that names one version resolves, and one that names
// several is sent back with the full hashes, as git does.
//
// A returned tag of "" means the link follows the default one, which is how
// every link written before tags existed reads. A hash or a version resolves to
// a build rather than to a channel, so it returns "" too - which is why the
// returned specKind, and not the tag, is what a caller reads to find out what
// the user asked for.
//
// An unknown name is reported as a nil package rather than an error, because the
// two add paths word that one differently and both wordings predate tags.
func resolveAddSpec(database *db.DB, name, requested string) (*db.Package, string, specKind, error) {
	if requested == "" {
		pkg, err := database.GetPackageByName(name)
		if err != nil {
			return nil, "", specDefault, fmt.Errorf("failed to look up package: %w", err)
		}
		return pkg, "", specDefault, nil
	}

	tagged, err := database.ResolveTag(name, requested)
	if err != nil {
		return nil, "", specTag, fmt.Errorf("failed to resolve tag %s of %s: %w", requested, name, err)
	}
	if tagged != nil {
		return tagged, requested, specTag, nil
	}

	versions, err := database.GetPackageVersions(name)
	if err != nil {
		return nil, "", specVersion, fmt.Errorf("failed to read the versions of %s: %w", name, err)
	}
	if len(versions) == 0 {
		return nil, "", specVersion, nil
	}

	current, err := database.GetPackageByName(name)
	if err != nil {
		return nil, "", specVersion, fmt.Errorf("failed to look up package: %w", err)
	}

	if pkg, err := matchVersion(versions, current, name, requested); pkg != nil || err != nil {
		return pkg, "", specVersion, err
	}
	if pkg, err := matchHashPrefix(versions, name, requested); pkg != nil || err != nil {
		return pkg, "", specHash, err
	}

	return nil, "", specVersion, versionNotInStoreError(name, requested, versions)
}

// matchVersion finds the retained version whose version string is exactly
// requested, or nil when none is. current is the version the default tag names,
// which breaks a tie in favour of the answer this lookup gave before it could
// see past that tag.
func matchVersion(versions []*db.Package, current *db.Package, name, requested string) (*db.Package, error) {
	var candidates []*db.Package
	for _, v := range versions {
		if v.Version == requested {
			if current != nil && v.ID == current.ID {
				return v, nil
			}
			candidates = append(candidates, v)
		}
	}

	switch len(candidates) {
	case 0:
		return nil, nil
	case 1:
		return candidates[0], nil
	default:
		return nil, fmt.Errorf("version %s of %s is ambiguous: %d retained versions carry it (%s); ask for one of them by hash",
			requested, name, len(candidates), describeHashes(candidates))
	}
}

// matchHashPrefix finds the retained version whose content hash starts with
// requested, or nil when none does. A prefix several versions share is refused
// with their full hashes, so the user can lengthen it rather than be handed
// whichever record was walked first.
//
// Anything shorter than minHashPrefix is not treated as a hash at all, for the
// reason given there.
func matchHashPrefix(versions []*db.Package, name, requested string) (*db.Package, error) {
	if len(requested) < minHashPrefix {
		return nil, nil
	}

	var candidates []*db.Package
	for _, v := range versions {
		if strings.HasPrefix(v.ContentHash, requested) {
			candidates = append(candidates, v)
		}
	}

	switch len(candidates) {
	case 0:
		return nil, nil
	case 1:
		return candidates[0], nil
	default:
		return nil, fmt.Errorf("hash prefix %s of %s is ambiguous: it matches %d retained versions (%s); use more characters",
			requested, name, len(candidates), describeHashes(candidates))
	}
}

// describeHashes renders candidates as their full content hashes, which is what
// a user has to type to tell them apart. The short hash a listing prints is by
// definition not enough here: it is the thing that turned out to be ambiguous.
func describeHashes(candidates []*db.Package) string {
	hashes := make([]string, len(candidates))
	for i, c := range candidates {
		hashes[i] = c.ContentHash
	}
	return strings.Join(hashes, ", ")
}

// liveLinkSpecError refuses a spec that names a build alongside --link.
//
// The two say different things about what to resolve to, and only one of them
// can be honoured. A tag, a version and a hash each name a build in the store;
// --link points .lnpm/<package> at the source directory the package was
// published from, which holds whatever is in the working tree - unpublished,
// possibly uncommitted, and not what any of the three names. Doing both is
// impossible, and doing --link while reporting the spec, which is what the
// summary line did, tells the user they are on a build they are not on. A hash
// names a build more specifically than a tag does, so it is refused for the same
// reason and more sharply.
//
// Refused rather than warned about because the two are a contradiction in the
// request, not a risk in it: there is no version of this add that does what the
// spec asks for, so the user has to decide which half to drop.
func liveLinkSpecError(name, requested string, kind specKind) error {
	if kind == specTag {
		return fmt.Errorf("cannot add %s@%s with --link: --link resolves to the package's source directory, which is not the build any tag names (drop --link to get the %s build, or drop the tag to link the source)", name, requested, requested)
	}
	return fmt.Errorf("cannot add %s@%s with --link: --link resolves to the package's source directory, which holds the working tree and not the build %s names (drop --link to get that build, or drop the @%s to link the source)", name, requested, requested, requested)
}

// versionNotInStoreError reports that nothing the store retains for name
// answers to what the user asked for, shared by the parallel and the
// single-package add paths so both tell the user the same thing.
//
// The parameters are declared in the order the message reads them. name and
// requested are both plain strings, so a caller that swaps them still compiles
// and quietly produces a message that lies about which is which - "no version of
// 1.0.0 matches mylib"; keeping declaration order and interpolation order
// identical is what makes such a swap visible at the call site.
//
// The retained versions are named, and that is the whole change from the message
// this replaces. That one reported what the default tag points at as though it
// were the only version there was - true while the store held one record per
// name, and misleading the moment it stopped being, because the version the user
// asked for may be sitting in the store one row down. This is exactly the moment
// they need to be told what is there to roll back to.
//
// The remedy is a trailing parenthetical rather than a second sentence: the
// parallel path wraps this error as "<spec>: %w", so the string has to compose
// as a clause, and a terminal period would read badly there as well as trip
// staticcheck's ST1005.
func versionNotInStoreError(name, requested string, retained []*db.Package) error {
	return fmt.Errorf("no version of %s matches %s (retained: %s; run 'lnpm list %s --versions' for the full history)",
		name, requested, describeVersions(retained), name)
}

// describedVersions is how many retained versions versionNotInStoreError names
// before falling back to a count.
const describedVersions = 5

// describeVersions renders retained versions as "1.3.0 (a1b2c3d4)", in the order
// GetPackageVersions returns them, which is newest first. The short hash is the
// one `lnpm list --versions` prints, so what the error shows is what the user
// would type back.
//
// Only the newest describedVersions are spelled out, with the rest counted. The
// message already points at `lnpm list <pkg> --versions`, so it does not have to
// be the history: a package with thirty retained versions would otherwise
// produce a single ~700-character error, which the parallel add path then wraps
// again. Every other display path in lnpm caps what it prints the same way.
func describeVersions(retained []*db.Package) string {
	shown := retained
	if len(shown) > describedVersions {
		shown = shown[:describedVersions]
	}

	described := make([]string, 0, len(shown)+1)
	for _, v := range shown {
		described = append(described, fmt.Sprintf("%s (%s)", truncate(v.Version, 14), shortHash(v.ContentHash)))
	}
	if omitted := len(retained) - len(shown); omitted > 0 {
		described = append(described, fmt.Sprintf("and %d more", omitted))
	}
	return strings.Join(described, ", ")
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

	return fsutil.WriteFileAtomic(path, pkgjson.EnsureTrailingNewline(output), 0644)
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
