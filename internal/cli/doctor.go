package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/store"
)

// RunDoctor executes the doctor command
func RunDoctor() error {
	fmt.Println("Running lnpm doctor...")
	fmt.Println()

	issues := 0
	warnings := 0

	// Check 1: Store directory exists and is writable
	fmt.Print("Checking store directory... ")
	storeUsable := false
	storePath, err := config.GetStorePath()
	if err != nil {
		fmt.Printf("%s ERROR\n", iconFail())
		fmt.Printf("  Failed to resolve store path: %v\n", err)
		issues++
	} else if info, err := os.Stat(storePath); err != nil {
		fmt.Printf("%s NOT FOUND\n", iconFail())
		fmt.Printf("  Store directory does not exist: %s\n", storePath)
		fmt.Println("  Fix: Run 'lnpm publish' to create it")
		issues++
	} else if !info.IsDir() {
		fmt.Printf("%s ERROR\n", iconFail())
		fmt.Printf("  %s exists but is not a directory\n", storePath)
		issues++
	} else {
		// Check writable
		testFile := filepath.Join(storePath, ".write-test")
		if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
			fmt.Printf("%s NOT WRITABLE\n", iconFail())
			fmt.Printf("  Cannot write to store directory: %v\n", err)
			issues++
		} else {
			_ = os.Remove(testFile)
			fmt.Printf("%s OK\n", iconOK())
			storeUsable = true
		}
	}

	// Check 2: Database integrity
	fmt.Print("Checking database... ")
	database, err := db.GetDB()
	if err != nil {
		fmt.Printf("%s ERROR\n", iconFail())
		fmt.Printf("  Failed to open database: %v\n", err)
		issues++
	} else {
		fmt.Printf("%s OK\n", iconOK())

		// Check 3: Orphaned packages (packages with no links)
		fmt.Print("Checking for orphaned packages... ")
		packages, _ := database.ListPackages()
		orphanedCount := 0
		for _, pkg := range packages {
			links, _ := database.GetLinksForPackage(pkg.ID)
			if len(links) == 0 {
				orphanedCount++
			}
		}
		if orphanedCount > 0 {
			fmt.Printf("%s %d orphaned package(s)\n", iconWarn(), orphanedCount)
			fmt.Println("  Fix: Run 'lnpm gc' to remove unused packages")
			warnings++
		} else {
			fmt.Printf("%s OK\n", iconOK())
		}

		// Check 4: Orphaned links (links to non-existent projects)
		fmt.Print("Checking for orphaned links... ")
		orphanedLinks := 0
		for _, pkg := range packages {
			links, _ := database.GetLinksForPackage(pkg.ID)
			for _, link := range links {
				proj, _ := database.GetProjectByID(link.ProjectID)
				if proj == nil {
					orphanedLinks++
					continue
				}
				// Check if project directory still exists
				if _, err := os.Stat(proj.Path); os.IsNotExist(err) {
					orphanedLinks++
				}
			}
		}
		if orphanedLinks > 0 {
			fmt.Printf("%s %d orphaned link(s)\n", iconWarn(), orphanedLinks)
			fmt.Println("  Fix: Run 'lnpm gc --fix-links' to clean up")
			warnings++
		} else {
			fmt.Printf("%s OK\n", iconOK())
		}

		// Check 5: Verify store files exist
		fmt.Print("Checking store file integrity... ")
		missingFiles := 0
		for _, pkg := range packages {
			if pkg.StorePath == "" {
				continue
			}
			if _, err := os.Stat(pkg.StorePath); os.IsNotExist(err) {
				missingFiles++
			}
		}
		if missingFiles > 0 {
			fmt.Printf("%s %d package(s) with missing files\n", iconFail(), missingFiles)
			fmt.Println("  Fix: Re-publish affected packages")
			issues++
		} else {
			fmt.Printf("%s OK\n", iconOK())
		}
	}

	// Check 6: One-time backfill of completeness markers into entries written
	// before markers existed. Reported only: the backfill runs when a command
	// opens the store, and doctor never repairs what it finds. Skipped when
	// Check 1 found no usable store — with no store there is nothing to
	// backfill, and with an unwritable one the command named as the fix could
	// not run either, so Check 1's own finding is the one worth acting on.
	if storeUsable {
		fmt.Print("Checking store completeness markers... ")
		done, err := store.BackfillDone()
		if err != nil {
			fmt.Printf("%s ERROR\n", iconFail())
			fmt.Printf("  Failed to read the backfill status: %v\n", err)
			issues++
		} else if !done {
			fmt.Printf("%s PENDING\n", iconWarn())
			fmt.Println("  Store entries have not been backfilled with completeness markers yet")
			fmt.Println("  Fix: Run 'lnpm gc --dry-run' (or any command that opens the store)")
			warnings++
		} else {
			fmt.Printf("%s OK\n", iconOK())
		}
	}

	// Summary
	fmt.Println()
	if issues == 0 && warnings == 0 {
		fmt.Printf("%s All checks passed!\n", iconOK())
	} else {
		if issues > 0 {
			fmt.Printf("%s Found %d issue(s)\n", iconFail(), issues)
		}
		if warnings > 0 {
			fmt.Printf("%s Found %d warning(s)\n", iconWarn(), warnings)
		}
	}

	// The findings are already printed above; the error exists so that a script
	// running `lnpm doctor && ...` sees a non-zero exit. Warnings do not fail
	// the command: they describe cleanup worth doing, not a broken install.
	if issues > 0 {
		return fmt.Errorf("doctor found %d issue(s)", issues)
	}

	return nil
}
