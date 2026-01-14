package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/user/lnpm/internal/db"
	"github.com/user/lnpm/pkg/lockfile"
)

// RunRetreat removes all lnpm changes from the current project
func RunRetreat(force bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Check for lnpm.lock
	lock, err := lockfile.Load(cwd)
	if err != nil || len(lock.List()) == 0 {
		// Check for .lnpm directory
		lnpmDir := filepath.Join(cwd, ".lnpm")
		if _, err := os.Stat(lnpmDir); os.IsNotExist(err) {
			fmt.Println("No lnpm links found in this project")
			return nil
		}
	}

	if !force {
		fmt.Println("This will remove all lnpm links and restore original dependencies.")
		fmt.Println("Run with --force to confirm.")
		fmt.Println()
		fmt.Println("Changes that will be made:")

		// Show what will be removed
		linkedPkgs := lock.List()
		if len(linkedPkgs) > 0 {
			fmt.Printf("  - Remove %d linked package(s)\n", len(linkedPkgs))
			for _, name := range linkedPkgs {
				pkg, _ := lock.Get(name)
				if pkg.OriginalVersion != "" {
					fmt.Printf("    %s: file:.lnpm/%s → %s\n", name, name, pkg.OriginalVersion)
				} else {
					fmt.Printf("    %s: will be removed from package.json\n", name)
				}
			}
		}

		fmt.Println("  - Delete .lnpm/ directory")
		fmt.Println("  - Delete lnpm.lock")
		return nil
	}

	fmt.Println("Retreating from lnpm...")

	// Get database for cleanup
	database, _ := db.GetDB()

	// Get current project
	var proj *db.Project
	if database != nil {
		proj, _ = database.GetProjectByPath(cwd)
	}

	// Remove each linked package
	linkedPkgs := lock.List()
	for _, name := range linkedPkgs {
		pkg, _ := lock.Get(name)

		fmt.Printf("  Removing %s...\n", name)

		// Remove symlink from node_modules
		nodeModulesLink := filepath.Join(cwd, "node_modules", name)
		os.Remove(nodeModulesLink)

		// Restore original package.json dependency
		if pkg.OriginalVersion != "" {
			if err := restorePackageJSON(cwd, name, pkg.OriginalVersion); err != nil {
				fmt.Printf("    ⚠ Failed to restore package.json: %v\n", err)
			} else {
				fmt.Printf("    ✓ Restored %s to %s\n", name, pkg.OriginalVersion)
			}
		} else {
			if err := removeFromPackageJSON(cwd, name); err != nil {
				fmt.Printf("    ⚠ Failed to update package.json: %v\n", err)
			}
		}

		// Remove link from database
		if database != nil && proj != nil {
			dbPkg, _ := database.GetPackageByName(name)
			if dbPkg != nil {
				database.DeleteLink(dbPkg.ID, proj.ID)
			}
		}
	}

	// Remove .lnpm directory
	lnpmDir := filepath.Join(cwd, ".lnpm")
	if err := os.RemoveAll(lnpmDir); err != nil {
		fmt.Printf("  ⚠ Failed to remove .lnpm/: %v\n", err)
	} else {
		fmt.Println("  ✓ Removed .lnpm/")
	}

	// Remove lnpm.lock
	lockPath := filepath.Join(cwd, "lnpm.lock")
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("  ⚠ Failed to remove lnpm.lock: %v\n", err)
	} else {
		fmt.Println("  ✓ Removed lnpm.lock")
	}

	fmt.Println()
	fmt.Println("✓ Retreat complete!")
	fmt.Println()
	fmt.Println("You may want to run your package manager's install command:")
	fmt.Println("  npm install / yarn install / pnpm install / bun install")

	return nil
}
