package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/user/lnpm/internal/db"
)

// RunDoctor executes the doctor command
func RunDoctor() error {
	fmt.Println("Running lnpm doctor...")
	fmt.Println()

	issues := 0
	warnings := 0

	// Check 1: Store directory exists and is writable
	storePath := getStorePath()
	fmt.Print("Checking store directory... ")
	if info, err := os.Stat(storePath); err != nil {
		fmt.Println("✗ NOT FOUND")
		fmt.Printf("  Store directory does not exist: %s\n", storePath)
		fmt.Println("  Fix: Run 'lnpm publish' to create it")
		issues++
	} else if !info.IsDir() {
		fmt.Println("✗ ERROR")
		fmt.Printf("  %s exists but is not a directory\n", storePath)
		issues++
	} else {
		// Check writable
		testFile := filepath.Join(storePath, ".write-test")
		if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
			fmt.Println("✗ NOT WRITABLE")
			fmt.Printf("  Cannot write to store directory: %v\n", err)
			issues++
		} else {
			os.Remove(testFile)
			fmt.Println("✓ OK")
		}
	}

	// Check 2: Database integrity
	fmt.Print("Checking database... ")
	database, err := db.GetDB()
	if err != nil {
		fmt.Println("✗ ERROR")
		fmt.Printf("  Failed to open database: %v\n", err)
		issues++
	} else {
		fmt.Println("✓ OK")

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
			fmt.Printf("⚠ %d orphaned package(s)\n", orphanedCount)
			fmt.Println("  Fix: Run 'lnpm gc' to remove unused packages")
			warnings++
		} else {
			fmt.Println("✓ OK")
		}

		// Check 4: Orphaned links (links to non-existent projects)
		fmt.Print("Checking for orphaned links... ")
		orphanedLinks := 0
		for _, pkg := range packages {
			links, _ := database.GetLinksForPackage(pkg.ID)
			for _, link := range links {
				proj, _ := database.GetProjectByPath(getProjectPathByID(database, link.ProjectID))
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
			fmt.Printf("⚠ %d orphaned link(s)\n", orphanedLinks)
			fmt.Println("  Fix: Run 'lnpm gc --fix-links' to clean up")
			warnings++
		} else {
			fmt.Println("✓ OK")
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
			fmt.Printf("✗ %d package(s) with missing files\n", missingFiles)
			fmt.Println("  Fix: Re-publish affected packages")
			issues++
		} else {
			fmt.Println("✓ OK")
		}
	}

	// Summary
	fmt.Println()
	if issues == 0 && warnings == 0 {
		fmt.Println("✓ All checks passed!")
	} else {
		if issues > 0 {
			fmt.Printf("✗ Found %d issue(s)\n", issues)
		}
		if warnings > 0 {
			fmt.Printf("⚠ Found %d warning(s)\n", warnings)
		}
	}

	return nil
}

// getStorePath returns the lnpm store path
func getStorePath() string {
	if storePath := os.Getenv("LNPM_STORE"); storePath != "" {
		return storePath
	}

	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".lnpm")
}

// getProjectPathByID looks up a project path by ID
func getProjectPathByID(database *db.DB, projectID int64) string {
	// This is a bit inefficient but works for doctor checks
	packages, _ := database.ListPackages()
	for _, pkg := range packages {
		links, _ := database.GetLinksForPackage(pkg.ID)
		for _, link := range links {
			if link.ProjectID == projectID {
				projects, _ := database.GetProjectsForPackage(pkg.ID)
				for _, proj := range projects {
					if proj.ID == projectID {
						return proj.Path
					}
				}
			}
		}
	}
	return ""
}
