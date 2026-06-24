package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/store"
)

// RunGC executes the garbage collection command
func RunGC(dryRun bool, olderThan string, fixLinks bool) error {
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Parse olderThan duration
	var maxAge time.Duration
	if olderThan != "" {
		var err error
		maxAge, err = parseDuration(olderThan)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", olderThan, err)
		}
	}

	if dryRun {
		fmt.Println("Dry run mode - no changes will be made")
		fmt.Println()
	}

	packages, err := database.ListPackages()
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}

	// Store root used to bound destructive RemoveAll calls.
	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to access store: %w", err)
	}
	storeRoot := s.Root()

	// Find packages to remove
	var packagesToRemove []*db.Package
	var linksToRemove []linkToRemove

	for _, pkg := range packages {
		links, _ := database.GetLinksForPackage(pkg.ID)

		// Check for orphaned links
		for _, link := range links {
			proj, _ := database.GetProjectByID(link.ProjectID)
			if proj == nil {
				linksToRemove = append(linksToRemove, linkToRemove{
					packageID: pkg.ID,
					projectID: link.ProjectID,
					reason:    "project not found in database",
				})
				continue
			}
			// Check if project directory still exists
			if _, err := os.Stat(proj.Path); os.IsNotExist(err) {
				linksToRemove = append(linksToRemove, linkToRemove{
					packageID:   pkg.ID,
					projectID:   link.ProjectID,
					projectPath: proj.Path,
					reason:      "project directory no longer exists",
				})
			}
		}

		// Re-check links after filtering orphans
		validLinks := len(links) - countLinksForPackage(linksToRemove, pkg.ID)

		// Package is orphaned if no valid links
		if validLinks == 0 {
			// Check age if specified
			if maxAge > 0 {
				age := time.Since(pkg.UpdatedAt)
				if age < maxAge {
					continue
				}
			}
			packagesToRemove = append(packagesToRemove, pkg)
		}
	}

	// Report findings
	if fixLinks && len(linksToRemove) > 0 {
		fmt.Printf("Found %d orphaned link(s):\n", len(linksToRemove))
		for _, l := range linksToRemove {
			if l.projectPath != "" {
				fmt.Printf("  - Link to %s (%s)\n", l.projectPath, l.reason)
			} else {
				fmt.Printf("  - Link to project ID %d (%s)\n", l.projectID, l.reason)
			}
		}
		fmt.Println()

		if !dryRun {
			for _, l := range linksToRemove {
				_ = database.DeleteLink(l.packageID, l.projectID)
			}
			fmt.Printf("✓ Removed %d orphaned link(s)\n", len(linksToRemove))
		}
	}

	if len(packagesToRemove) > 0 {
		fmt.Printf("Found %d orphaned package(s):\n", len(packagesToRemove))
		var totalSize int64
		for _, pkg := range packagesToRemove {
			fmt.Printf("  - %s@%s (%s)\n", pkg.Name, pkg.Version, formatSize(pkg.TotalSize))
			totalSize += pkg.TotalSize
		}
		fmt.Printf("Total size: %s\n", formatSize(totalSize))
		fmt.Println()

		if !dryRun {
			for _, pkg := range packagesToRemove {
				// Remove from store, but only if the recorded path is actually
				// inside the store root (guards against a poisoned DB entry).
				if pkg.StorePath != "" {
					if isWithinStore(storeRoot, pkg.StorePath) {
						_ = os.RemoveAll(pkg.StorePath)
					} else {
						fmt.Printf("  ⚠ Skipping %s: store path %q is outside the store root\n", pkg.Name, pkg.StorePath)
					}
				}
				// Remove from database
				_ = database.DeletePackage(pkg.ID)
			}
			fmt.Printf("✓ Removed %d package(s), freed %s\n", len(packagesToRemove), formatSize(totalSize))
		}
	}

	if len(packagesToRemove) == 0 && len(linksToRemove) == 0 {
		fmt.Println("✓ Nothing to clean up")
	}

	return nil
}

type linkToRemove struct {
	packageID   int64
	projectID   int64
	projectPath string
	reason      string
}

func countLinksForPackage(links []linkToRemove, packageID int64) int {
	count := 0
	for _, l := range links {
		if l.packageID == packageID {
			count++
		}
	}
	return count
}

// parseDuration parses a duration string like "30d", "1w", "24h"
func parseDuration(s string) (time.Duration, error) {
	if len(s) == 0 {
		return 0, fmt.Errorf("empty duration")
	}

	// Check for days/weeks suffix
	lastChar := s[len(s)-1]
	switch lastChar {
	case 'd', 'D':
		days := s[:len(s)-1]
		var d int
		if _, err := fmt.Sscanf(days, "%d", &d); err != nil {
			return 0, err
		}
		return time.Duration(d) * 24 * time.Hour, nil
	case 'w', 'W':
		weeks := s[:len(s)-1]
		var w int
		if _, err := fmt.Sscanf(weeks, "%d", &w); err != nil {
			return 0, err
		}
		return time.Duration(w) * 7 * 24 * time.Hour, nil
	default:
		// Fall back to standard Go duration parsing
		return time.ParseDuration(s)
	}
}

// isWithinStore reports whether p is the store root or nested inside it.
func isWithinStore(root, p string) bool {
	rootAbs, err1 := filepath.Abs(root)
	pAbs, err2 := filepath.Abs(p)
	if err1 != nil || err2 != nil {
		return false
	}
	rootAbs = filepath.Clean(rootAbs)
	pAbs = filepath.Clean(pAbs)
	return pAbs == rootAbs || strings.HasPrefix(pAbs, rootAbs+string(filepath.Separator))
}

// formatSize is defined in publish.go
