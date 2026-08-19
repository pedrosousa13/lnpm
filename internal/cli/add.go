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
	err         error
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

	// Update package.json for all successful packages
	if !pure {
		for i := range successful {
			origVersion, err := updatePackageJSON(pkgJSONPath, successful[i].pkg.Name, dev, useLink)
			if err != nil {
				fmt.Printf("  %s Failed to update package.json for %s: %v\n", iconWarn(), successful[i].pkg.Name, err)
				continue
			}
			successful[i].origVersion = origVersion
		}
	}

	// Update lockfile and database for all successful packages
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

	if err := lock.Save(cwd); err != nil {
		return fmt.Errorf("failed to save lock file: %w", err)
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

	// Update package.json (unless --pure)
	var originalVersion string
	if !pure {
		originalVersion, err = updatePackageJSON(pkgJSONPath, pkg.Name, dev, useLink)
		if err != nil {
			return fmt.Errorf("failed to update package.json: %w", err)
		}
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

// updatePackageJSON updates package.json with the lnpm dependency. When link is
// true the dependency uses the "link:" protocol (symlink-style resolution,
// which helps pnpm/yarn dedupe peer deps) instead of the default "file:".
func updatePackageJSON(path string, packageName string, dev bool, useLink bool) (originalVersion string, err error) {
	// Read existing package.json
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return "", err
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
	depsField := "dependencies"
	if dev || (!inDeps && inDevDeps) {
		depsField = "devDependencies"
	}

	// Get or create dependencies object
	deps, ok := pkgJSON[depsField].(map[string]interface{})
	if !ok {
		deps = make(map[string]interface{})
		pkgJSON[depsField] = deps
	}

	// Save original version from target field (ignore lnpm references)
	if v, ok := deps[packageName].(string); ok {
		if !isLnpmReference(v) {
			originalVersion = v
		}
	}

	// Also check other field for original version
	otherField := "devDependencies"
	if depsField == "devDependencies" {
		otherField = "dependencies"
	}
	if otherDeps, ok := pkgJSON[otherField].(map[string]interface{}); ok {
		if v, ok := otherDeps[packageName].(string); ok {
			if originalVersion == "" && !isLnpmReference(v) {
				originalVersion = v
			}
			// Remove from other field to avoid duplicate entries
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
	output, err := json.MarshalIndent(pkgJSON, "", "  ")
	if err != nil {
		return "", err
	}

	// Add trailing newline
	output = append(output, '\n')

	if err := os.WriteFile(path, output, 0644); err != nil {
		return "", err
	}

	return originalVersion, nil
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
