package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/user/lnpm/internal/db"
	"github.com/user/lnpm/pkg/lockfile"
)

// RunStatus executes the status command
func RunStatus() error {
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Get all packages in store
	packages, err := database.ListPackages()
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}

	// Print packages section
	fmt.Println("📦 Published Packages")
	if len(packages) == 0 {
		fmt.Println("  (none)")
	} else {
		// Print header
		fmt.Printf("  %-25s %-12s %-10s %-20s\n", "NAME", "VERSION", "HASH", "PUBLISHED")
		fmt.Printf("  %s\n", strings.Repeat("-", 70))

		for _, pkg := range packages {
			fmt.Printf("  %-25s %-12s %-10s %-20s\n",
				truncate(pkg.Name, 25),
				truncate(pkg.Version, 12),
				shortHash(pkg.ContentHash),
				formatTimeAgo(pkg.UpdatedAt),
			)
		}
	}
	fmt.Println()

	// Get all links and group by project
	type projectInfo struct {
		path     string
		pm       string
		packages []string
	}
	projectMap := make(map[string]*projectInfo)

	for _, pkg := range packages {
		projects, err := database.GetProjectsForPackage(pkg.ID)
		if err != nil {
			continue
		}
		for _, proj := range projects {
			if _, ok := projectMap[proj.Path]; !ok {
				projectMap[proj.Path] = &projectInfo{
					path: proj.Path,
					pm:   proj.PackageManager,
				}
			}
			projectMap[proj.Path].packages = append(projectMap[proj.Path].packages, pkg.Name)
		}
	}

	// Print links section
	fmt.Println("🔗 Active Links")
	if len(projectMap) == 0 {
		fmt.Println("  (none)")
	} else {
		fmt.Printf("  %-40s %-8s %-30s\n", "PROJECT", "PM", "PACKAGES")
		fmt.Printf("  %s\n", strings.Repeat("-", 80))

		for _, proj := range projectMap {
			fmt.Printf("  %-40s %-8s %-30s\n",
				truncate(proj.path, 40),
				proj.pm,
				truncate(strings.Join(proj.packages, ", "), 30),
			)
		}
	}
	fmt.Println()

	// Check current directory for local status
	cwd, err := os.Getwd()
	if err != nil {
		return nil // Non-fatal
	}

	lock, err := lockfile.Load(cwd)
	if err == nil && len(lock.List()) > 0 {
		fmt.Println("📍 Current Project")
		fmt.Printf("  %s\n", cwd)
		fmt.Printf("  Linked packages:\n")
		for _, name := range lock.List() {
			entry, _ := lock.Get(name)
			fmt.Printf("    • %s@%s (hash: %s)\n", name, entry.Version, shortHash(entry.Hash))
		}
	}

	return nil
}

// RunList executes the list command
func RunList(showStore bool, packageName string, showProjects bool) error {
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if showStore {
		// List all packages in store
		packages, err := database.ListPackages()
		if err != nil {
			return fmt.Errorf("failed to list packages: %w", err)
		}

		if len(packages) == 0 {
			fmt.Println("No packages in store")
			return nil
		}

		fmt.Println("Packages in store:")
		for _, pkg := range packages {
			fmt.Printf("  %s@%s (%s)\n", pkg.Name, pkg.Version, shortHash(pkg.ContentHash))
		}
		return nil
	}

	if packageName != "" && showProjects {
		// List projects using a specific package
		pkg, err := database.GetPackageByName(packageName)
		if err != nil {
			return fmt.Errorf("failed to look up package: %w", err)
		}
		if pkg == nil {
			return fmt.Errorf("package %s not found", packageName)
		}

		projects, err := database.GetProjectsForPackage(pkg.ID)
		if err != nil {
			return fmt.Errorf("failed to get projects: %w", err)
		}

		if len(projects) == 0 {
			fmt.Printf("No projects using %s\n", packageName)
			return nil
		}

		fmt.Printf("Projects using %s:\n", packageName)
		for _, proj := range projects {
			fmt.Printf("  %s (%s)\n", proj.Path, proj.PackageManager)
		}
		return nil
	}

	// List packages in current project
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	lock, err := lockfile.Load(cwd)
	if err != nil {
		return fmt.Errorf("failed to load lock file: %w", err)
	}

	packages := lock.List()
	if len(packages) == 0 {
		fmt.Println("No linked packages in current project")
		return nil
	}

	fmt.Println("Linked packages:")
	for _, name := range packages {
		entry, _ := lock.Get(name)
		fmt.Printf("  %s@%s\n", name, entry.Version)
		fmt.Printf("    Hash: %s\n", shortHash(entry.Hash))
		fmt.Printf("    Source: %s\n", entry.Source)
		fmt.Printf("    Linked: %s\n", formatTimeAgo(entry.Linked))
	}

	return nil
}

// truncate truncates a string to max length
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// formatTimeAgo formats a time as "X ago"
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case d < 7*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}
