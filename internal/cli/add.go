package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/store"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// RunAdd executes the add command
func RunAdd(packageSpec string, dev bool, pure bool) error {
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

	// Find package in store
	var pkg *db.Package
	if version != "" {
		// Look for specific version by hash prefix
		pkg, err = database.GetPackageByHash(name, version)
	} else {
		// Get latest version
		pkg, err = database.GetPackageByName(name)
	}

	if err != nil {
		return fmt.Errorf("failed to look up package: %w", err)
	}
	if pkg == nil {
		return fmt.Errorf("package %s not found in store. Did you run 'lnpm publish' in the package directory?", name)
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

	// Detect package manager
	pm := config.DetectPackageManager(cwd)

	// Update package.json (unless --pure)
	var originalVersion string
	if !pure {
		originalVersion, err = updatePackageJSON(pkgJSONPath, pkg.Name, dev)
		if err != nil {
			return fmt.Errorf("failed to update package.json: %w", err)
		}
	}

	// Update lock file
	lock, err := lockfile.Load(cwd)
	if err != nil {
		return fmt.Errorf("failed to load lock file: %w", err)
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

	fmt.Printf("✓ Added %s@%s\n", pkg.Name, pkg.Version)
	fmt.Printf("  Link type: %s\n", linkType)
	fmt.Printf("  Package manager: %s\n", pm)
	if !pure {
		fmt.Printf("  Updated: package.json\n")
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

// updatePackageJSON updates package.json with the lnpm dependency
func updatePackageJSON(path string, packageName string, dev bool) (originalVersion string, err error) {
	// Read existing package.json
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return "", err
	}

	// Determine which dependencies field to use
	depsField := "dependencies"
	if dev {
		depsField = "devDependencies"
	}

	// Get or create dependencies object
	deps, ok := pkgJSON[depsField].(map[string]interface{})
	if !ok {
		deps = make(map[string]interface{})
		pkgJSON[depsField] = deps
	}

	// Save original version if it exists
	if v, ok := deps[packageName].(string); ok {
		originalVersion = v
	}

	// Also check the other deps field for original version
	otherField := "devDependencies"
	if dev {
		otherField = "dependencies"
	}
	if otherDeps, ok := pkgJSON[otherField].(map[string]interface{}); ok {
		if v, ok := otherDeps[packageName].(string); ok && originalVersion == "" {
			originalVersion = v
		}
	}

	// Set the lnpm file: reference
	deps[packageName] = fmt.Sprintf("file:.lnpm/%s", packageName)

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
