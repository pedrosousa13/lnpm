package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/pkgjson"
	"github.com/pedrosousa13/lnpm/internal/shellcmd"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// RunRemove executes the remove command
func RunRemove(packageName string, all bool, yes bool) error {
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
		if !confirm(fmt.Sprintf("Remove all %d linked package(s) from this project?", len(packagesToRemove)), yes) {
			fmt.Println("Aborted.")
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

	held, err := linksOfProject(database, cwd)
	if err != nil {
		return err
	}

	linker := link.New(cwd)

	// Remove each package
	failed := 0
	for _, name := range packagesToRemove {
		fmt.Printf("Removing %s...\n", name)

		// Get lock entry for original version
		lockEntry, _ := lock.Get(name)

		// Unlink the package
		if err := linker.Unlink(name); err != nil {
			fmt.Printf("  %s Failed to unlink: %v\n", iconFail(), err)
			failed++
			continue
		}

		// Update package.json for this removal. A failure aborts the package: the
		// lock entry is the only record that it was ever linked (and, when set,
		// the only copy of the user's specifier), so dropping it would strand a
		// file:.lnpm/<pkg> reference with nothing left to drive a retry.
		if lockEntry.OriginalVersion != "" {
			if err := restorePackageJSON(cwd, name, lockEntry.OriginalVersion); err != nil {
				fmt.Printf("  %s Failed to restore package.json: %v\n", iconFail(), err)
				failed++
				continue
			}
		} else {
			// Remove the dependency entirely
			if err := removeFromPackageJSON(cwd, name); err != nil {
				fmt.Printf("  %s Failed to update package.json: %v\n", iconFail(), err)
				failed++
				continue
			}
		}

		// Remove from lock file
		lock.Remove(name)

		// Remove link from database. The row this project holds, not the one a
		// lookup by name would find: the name index mirrors the default tag, so
		// for a project on a tagged version that lookup names a different record
		// and the delete would silently match nothing.
		if proj != nil {
			if l, ok := held[name]; ok {
				_ = database.DeleteLink(l.PackageID, proj.ID)
			}
		}

		fmt.Printf("  %s Removed %s\n", iconOK(), name)
	}

	// Save updated lock file
	if err := lock.Save(cwd); err != nil {
		return fmt.Errorf("failed to save lock file: %w", err)
	}

	// Clean up empty lock file
	if len(lock.List()) == 0 {
		lockPath := filepath.Join(cwd, "lnpm.lock")
		_ = os.Remove(lockPath)
	}

	// Run package manager install to restore removed packages
	pm := config.DetectPackageManager(cwd)
	installCmd := config.GetInstallCommand(pm)
	fmt.Printf("Running %s...\n", installCmd)

	cmd := shellcmd.Command(installCmd)
	cmd.Dir = cwd
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("%s Install failed: %v\n", iconWarn(), err)
	}

	if failed > 0 {
		return fmt.Errorf("%d of %d package(s) failed to remove", failed, len(packagesToRemove))
	}

	return nil
}

// restorePackageJSON restores the original version of a dependency, editing the
// file's bytes in place so nothing but that one entry moves.
func restorePackageJSON(projectPath, packageName, originalVersion string) error {
	pkgJSONPath := filepath.Join(projectPath, "package.json")

	output, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return err
	}

	// Check both dependencies and devDependencies
	for _, field := range []string{"dependencies", "devDependencies"} {
		exists, err := pkgjson.HasDep(output, field, packageName)
		if err != nil {
			return err
		}
		if exists {
			if output, err = pkgjson.SetDep(output, field, packageName, originalVersion); err != nil {
				return err
			}
			break
		}
	}

	return os.WriteFile(pkgJSONPath, pkgjson.EnsureTrailingNewline(output), 0644)
}

// removeFromPackageJSON removes a dependency from package.json, editing the
// file's bytes in place so nothing but that one entry moves.
func removeFromPackageJSON(projectPath, packageName string) error {
	pkgJSONPath := filepath.Join(projectPath, "package.json")

	output, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return err
	}

	// Remove from both dependencies and devDependencies
	for _, field := range []string{"dependencies", "devDependencies"} {
		if output, err = pkgjson.RemoveDep(output, field, packageName); err != nil {
			return err
		}
	}

	return os.WriteFile(pkgJSONPath, pkgjson.EnsureTrailingNewline(output), 0644)
}
