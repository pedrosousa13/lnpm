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
	"github.com/pedrosousa13/lnpm/internal/gitignore"
	"github.com/pedrosousa13/lnpm/internal/hooks"
	"github.com/pedrosousa13/lnpm/internal/link"
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
	// pkgJSONUnreadable records that reading package.json for this package
	// already failed, so the later write is skipped instead of reporting the
	// same failure twice.
	pkgJSONUnreadable bool
	err               error
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
				result.err = fmt.Errorf("version %s of %s not found in store (latest published is %s). Re-publish %s to update.", version, name, pkg.Version, name)
				results <- result
				return
			}

			result.pkg = pkg

			files, err := s.GetFiles(pkg.Name, pkg.ContentHash)
			if err != nil {
				result.err = fmt.Errorf("failed to get package files: %w", err)
				results <- result
				return
			}

			linkType, err := linker.Link(pkg.Name, pkg.StorePath, files)
			if err != nil {
				result.err = fmt.Errorf("failed to link package: %w", err)
				results <- result
				return
			}

			result.linkType = linkType
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

	// ORDERING CONSTRAINT: the user's original specifiers exist only in
	// package.json, and writing the lnpm references overwrites them. Read them
	// and save them to the lock file BEFORE package.json is rewritten, so that
	// a failure of the rewrite - or of anything after it, which for this path
	// includes registering the project - still leaves remove/retreat able to
	// restore the specifiers instead of deleting the dependencies. Do not move
	// the package.json writes back above lock.Save.
	if !pure {
		for i := range successful {
			deps, err := readPackageJSONDeps(pkgJSONPath, successful[i].pkg.Name, dev)
			if err != nil {
				fmt.Printf("  %s Failed to read package.json for %s: %v\n", iconWarn(), successful[i].pkg.Name, err)
				successful[i].pkgJSONUnreadable = true
				continue
			}
			successful[i].origVersion = deps.originalVersion
		}
	}

	// Update lockfile for all successful packages
	for _, r := range successful {
		// Check for existing original version in lockfile
		existingOrigVersion := ""
		if existing, ok := lock.Get(r.pkg.Name); ok && existing.OriginalVersion != "" {
			existingOrigVersion = existing.OriginalVersion
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

	// Update package.json for all successful packages
	if !pure {
		for _, r := range successful {
			if r.pkgJSONUnreadable {
				continue
			}
			if err := writeLnpmReference(pkgJSONPath, r.pkg.Name, dev, useLink); err != nil {
				fmt.Printf("  %s Failed to update package.json for %s: %v\n", iconWarn(), r.pkg.Name, err)
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
		dbLink := &db.Link{
			PackageID: r.pkg.ID,
			ProjectID: existingProj.ID,
			LinkType:  string(r.linkType),
		}
		if err := database.InsertLink(dbLink); err != nil {
			fmt.Printf("  %s Failed to record link for %s: %v\n", iconWarn(), r.pkg.Name, err)
		}

		fmt.Printf("%s Added %s@%s (%s)\n", iconOK(), r.pkg.Name, r.pkg.Version, r.linkType)
	}

	if len(errors) > 0 {
		fmt.Printf("\n%s Some packages failed:\n", iconWarn())
		for _, err := range errors {
			fmt.Printf("  - %v\n", err)
		}
	}

	// Run npm install once at the end if requested
	if runInstall && !pure && len(successful) > 0 {
		fmt.Println("\nRunning npm install...")
		if err := hooks.RunPostAdd(cwd, true); err != nil {
			fmt.Printf("  %s npm install failed: %v\n", iconWarn(), err)
		}
	} else if !pure && len(successful) > 0 {
		fmt.Printf("\n  %s Run 'npm install' if you need to resolve peer dependencies\n", iconTip())
	}

	// Exit non-zero if any package failed, so scripts can detect it.
	if len(errors) > 0 {
		return fmt.Errorf("%d of %d package(s) failed to add", len(errors), len(packageSpecs))
	}

	return nil
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
		return fmt.Errorf("version %s of %s not found in store (latest published is %s). Re-publish %s to update.", version, name, pkg.Version, name)
	}

	fmt.Printf("Adding %s@%s...\n", pkg.Name, pkg.Version)

	// Get store
	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to access store: %w", err)
	}

	// Get files from store
	files, err := s.GetFiles(pkg.Name, pkg.ContentHash)
	if err != nil {
		return fmt.Errorf("failed to get package files: %w", err)
	}

	// Link the package
	linker := link.New(cwd)
	linkType, err := linker.Link(pkg.Name, pkg.StorePath, files)
	if err != nil {
		return fmt.Errorf("failed to link package: %w", err)
	}

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
	fmt.Printf("  Link type: %s\n", linkType)
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

// packageJSONDeps is what reading package.json tells us about a dependency
// before the lnpm reference is written: the parsed document, the field the
// reference belongs in, and the specifier the user had there. The
// field-selection rules live here alone so the read half and the write half
// cannot drift apart.
type packageJSONDeps struct {
	doc             map[string]interface{}
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
	deps := &packageJSONDeps{doc: pkgJSON, field: "dependencies"}
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

// write records the lnpm dependency in the parsed document and saves it. When
// useLink is true the dependency uses the "link:" protocol (symlink-style
// resolution, which helps pnpm/yarn dedupe peer deps) instead of the default
// "file:".
func (p *packageJSONDeps) write(path string, packageName string, useLink bool) error {
	// Get or create dependencies object
	deps, ok := p.doc[p.field].(map[string]interface{})
	if !ok {
		deps = make(map[string]interface{})
		p.doc[p.field] = deps
	}

	// Remove from other field to avoid duplicate entries
	if otherDeps, ok := p.doc[p.otherField()].(map[string]interface{}); ok {
		if _, ok := otherDeps[packageName].(string); ok {
			delete(otherDeps, packageName)
		}
	}

	// Set the lnpm reference (file: by default, link: when requested)
	protocol := "file"
	if useLink {
		protocol = "link"
	}
	deps[packageName] = fmt.Sprintf("%s:.lnpm/%s", protocol, packageName)

	// Write back
	output, err := json.MarshalIndent(p.doc, "", "  ")
	if err != nil {
		return err
	}

	// Add trailing newline
	output = append(output, '\n')

	return os.WriteFile(path, output, 0644)
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
