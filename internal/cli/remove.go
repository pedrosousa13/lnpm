package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/user/lnpm/internal/db"
	"github.com/user/lnpm/internal/link"
	"github.com/user/lnpm/pkg/lockfile"
)

// RunRemove executes the remove command
func RunRemove(packageName string, all bool) error {
	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Get database
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Load lock file
	lock, err := lockfile.Load(cwd)
	if err != nil {
		return fmt.Errorf("failed to load lock file: %w", err)
	}

	// Determine which packages to remove
	var packagesToRemove []string
	if all {
		packagesToRemove = lock.List()
		if len(packagesToRemove) == 0 {
			fmt.Println("No linked packages to remove")
			return nil
		}
	} else {
		if !lock.Has(packageName) {
			return fmt.Errorf("package %s is not linked in this project", packageName)
		}
		packagesToRemove = []string{packageName}
	}

	// Get project from database
	proj, err := database.GetProjectByPath(cwd)
	if err != nil {
		return fmt.Errorf("failed to get project: %w", err)
	}

	linker := link.New(cwd)

	// Remove each package
	for _, name := range packagesToRemove {
		fmt.Printf("Removing %s...\n", name)

		// Get lock entry for original version
		lockEntry, _ := lock.Get(name)

		// Unlink the package
		if err := linker.Unlink(name); err != nil {
			fmt.Printf("  ✗ Failed to unlink: %v\n", err)
			continue
		}

		// Restore original package.json dependency
		if lockEntry.OriginalVersion != "" {
			if err := restorePackageJSON(cwd, name, lockEntry.OriginalVersion); err != nil {
				fmt.Printf("  ⚠ Failed to restore package.json: %v\n", err)
			}
		} else {
			// Remove the dependency entirely
			if err := removeFromPackageJSON(cwd, name); err != nil {
				fmt.Printf("  ⚠ Failed to update package.json: %v\n", err)
			}
		}

		// Remove from lock file
		lock.Remove(name)

		// Remove link from database
		if proj != nil {
			pkg, _ := database.GetPackageByName(name)
			if pkg != nil {
				_ = database.DeleteLink(pkg.ID, proj.ID)
			}
		}

		fmt.Printf("  ✓ Removed %s\n", name)
	}

	// Save updated lock file
	if err := lock.Save(cwd); err != nil {
		return fmt.Errorf("failed to save lock file: %w", err)
	}

	// Clean up empty lock file
	if len(lock.List()) == 0 {
		lockPath := filepath.Join(cwd, "lnpm.lock")
		os.Remove(lockPath)
	}

	return nil
}

// restorePackageJSON restores the original version of a dependency
func restorePackageJSON(projectPath, packageName, originalVersion string) error {
	pkgJSONPath := filepath.Join(projectPath, "package.json")

	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return err
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return err
	}

	// Check both dependencies and devDependencies
	for _, field := range []string{"dependencies", "devDependencies"} {
		if deps, ok := pkgJSON[field].(map[string]interface{}); ok {
			if _, exists := deps[packageName]; exists {
				deps[packageName] = originalVersion
				break
			}
		}
	}

	output, err := json.MarshalIndent(pkgJSON, "", "  ")
	if err != nil {
		return err
	}

	output = append(output, '\n')
	return os.WriteFile(pkgJSONPath, output, 0644)
}

// removeFromPackageJSON removes a dependency from package.json
func removeFromPackageJSON(projectPath, packageName string) error {
	pkgJSONPath := filepath.Join(projectPath, "package.json")

	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return err
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return err
	}

	// Remove from both dependencies and devDependencies
	for _, field := range []string{"dependencies", "devDependencies"} {
		if deps, ok := pkgJSON[field].(map[string]interface{}); ok {
			delete(deps, packageName)
		}
	}

	output, err := json.MarshalIndent(pkgJSON, "", "  ")
	if err != nil {
		return err
	}

	output = append(output, '\n')
	return os.WriteFile(pkgJSONPath, output, 0644)
}
