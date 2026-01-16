package tui

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// PackageItem represents a package in the TUI
type PackageItem struct {
	Name         string
	Version      string
	Hash         string
	LinkedCount  int
	LinkedProjects []string // List of project names/paths linked to this package
	SourcePath   string
	PublishedAt  time.Time
	ContentHash  string
}

func (p PackageItem) Title() string       { return p.Name }
func (p PackageItem) Description() string {
	return fmt.Sprintf("v%s - %d linked projects", p.Version, p.LinkedCount)
}
func (p PackageItem) FilterValue() string { return p.Name }

// ProjectItem represents a project in the TUI
type ProjectItem struct {
	Path         string
	Name         string
	PackageCount int
	PackageManager string
	UpdatedAt    time.Time
}

func (p ProjectItem) Title() string       { return p.Name }
func (p ProjectItem) Description() string {
	return fmt.Sprintf("%d packages - %s", p.PackageCount, p.PackageManager)
}
func (p ProjectItem) FilterValue() string { return p.Name }

// LinkItem represents a link in the current project
type LinkItem struct {
	Name           string
	Version        string
	OriginalVersion string
	LinkedAt       time.Time
	SourcePath     string
}

func (l LinkItem) Title() string       { return l.Name }
func (l LinkItem) Description() string {
	if l.OriginalVersion != "" && l.OriginalVersion != l.Version {
		return fmt.Sprintf("v%s (was %s)", l.Version, l.OriginalVersion)
	}
	return fmt.Sprintf("v%s", l.Version)
}
func (l LinkItem) FilterValue() string { return l.Name }

// GetPackagesList fetches all packages from the database
func GetPackagesList() ([]list.Item, error) {
	// log.Println("[TUI] GetPackagesList: starting")
	debug.Logf("tui: GetPackagesList started")

	database, err := db.GetDB()
	if err != nil {
		// log.Printf("[TUI] GetPackagesList: failed to open database: %v\n", err)
		debug.Logf("tui: GetPackagesList failed to open database: %v", err)
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	// log.Println("[TUI] GetPackagesList: database opened successfully")

	packages, err := database.ListPackages()
	if err != nil {
		// log.Printf("[TUI] GetPackagesList: failed to list packages: %v\n", err)
		debug.Logf("tui: GetPackagesList failed to list packages: %v", err)
		return nil, fmt.Errorf("failed to list packages: %w", err)
	}

	// log.Printf("[TUI] GetPackagesList: found %d packages\n", len(packages))
	items := make([]list.Item, len(packages))
	for i, pkg := range packages {
		// Count linked projects for this package
		projects, err := database.GetProjectsForPackage(pkg.ID)
		linkedCount := 0
		var linkedProjectNames []string

		if err == nil {
			linkedCount = len(projects)
			for _, p := range projects {
				linkedProjectNames = append(linkedProjectNames, p.Name)
			}
		}

		items[i] = PackageItem{
			Name:           pkg.Name,
			Version:        pkg.Version,
			Hash:           shortHash(pkg.ContentHash),
			LinkedCount:    linkedCount,
			LinkedProjects: linkedProjectNames,
			SourcePath:     pkg.SourcePath,
			PublishedAt:    pkg.UpdatedAt,
			ContentHash:    pkg.ContentHash,
		}
		log.Printf("[TUI] GetPackagesList: item %d: %s@%s (%d projects)\n", i, pkg.Name, pkg.Version, linkedCount)
	}

	log.Printf("[TUI] GetPackagesList: completed successfully with %d items\n", len(items))
	return items, nil
}

// GetProjectsList fetches all projects from the database
// Since there's no direct ListProjects, we discover projects through their links
func GetProjectsList() ([]list.Item, error) {
	log.Println("[TUI] GetProjectsList: starting")
	debug.Logf("tui: GetProjectsList started")

	database, err := db.GetDB()
	if err != nil {
		log.Printf("[TUI] GetProjectsList: failed to open database: %v\n", err)
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	log.Println("[TUI] GetProjectsList: database opened successfully")

	packages, err := database.ListPackages()
	if err != nil {
		log.Printf("[TUI] GetProjectsList: failed to list packages: %v\n", err)
		return nil, fmt.Errorf("failed to list packages: %w", err)
	}

	log.Printf("[TUI] GetProjectsList: found %d packages\n", len(packages))

	// Collect unique projects from package links
	projectMap := make(map[string]*ProjectItem)

	for _, pkg := range packages {
		log.Printf("[TUI] GetProjectsList: checking projects for package %s (id=%d)\n", pkg.Name, pkg.ID)
		projects, err := database.GetProjectsForPackage(pkg.ID)
		if err != nil {
			log.Printf("[TUI] GetProjectsList: error getting projects for %s: %v\n", pkg.Name, err)
			continue
		}

		log.Printf("[TUI] GetProjectsList: found %d projects for %s\n", len(projects), pkg.Name)
		for _, proj := range projects {
			log.Printf("[TUI] GetProjectsList: processing project %s (id=%d, path=%s)\n", proj.Name, proj.ID, proj.Path)
			if _, exists := projectMap[proj.Path]; !exists {
				// Count packages for this project
				links, err := database.GetLinksForProject(proj.ID)
				packageCount := 0
				if err == nil {
					packageCount = len(links)
				}

				// Use project name if available, otherwise use directory basename
				displayName := proj.Name
				if displayName == "" {
					// Extract directory name from path
					parts := strings.Split(strings.TrimSuffix(proj.Path, "/"), "/")
					if len(parts) > 0 {
						displayName = parts[len(parts)-1]
					}
				}

				projectMap[proj.Path] = &ProjectItem{
					Path:           proj.Path,
					Name:           displayName,
					PackageCount:   packageCount,
					PackageManager: proj.PackageManager,
					UpdatedAt:      proj.UpdatedAt,
				}
				log.Printf("[TUI] GetProjectsList: added project %s (%d packages)\n", displayName, packageCount)
			}
		}
	}

	// Convert map to slice
	items := make([]list.Item, 0, len(projectMap))
	for _, proj := range projectMap {
		items = append(items, *proj)
	}

	log.Printf("[TUI] GetProjectsList: completed with %d projects\n", len(items))
	return items, nil
}

// GetCurrentProjectLinks fetches links for the current working directory
func GetCurrentProjectLinks() ([]list.Item, error) {
	// log.Println("[TUI] GetCurrentProjectLinks: starting")
	debug.Logf("tui: GetCurrentProjectLinks started")

	cwd, err := os.Getwd()
	if err != nil {
		// log.Printf("[TUI] GetCurrentProjectLinks: failed to get current directory: %v\n", err)
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}
	// log.Printf("[TUI] GetCurrentProjectLinks: current directory: %s\n", cwd)

	lock, err := lockfile.Load(cwd)
	if err != nil {
		// No lockfile means no links
		// log.Printf("[TUI] GetCurrentProjectLinks: no lockfile found or error: %v\n", err)
		return []list.Item{}, nil
	}

	packages := lock.List()
	// log.Printf("[TUI] GetCurrentProjectLinks: found %d packages in lockfile\n", len(packages))

	items := make([]list.Item, len(packages))

	for i, name := range packages {
		entry, exists := lock.Get(name)
		if !exists {
			continue
		}

		items[i] = LinkItem{
			Name:            name,
			Version:         entry.Version,
			OriginalVersion: entry.OriginalVersion,
			LinkedAt:        entry.Linked,
			SourcePath:      entry.Source,
		}
		log.Printf("[TUI] GetCurrentProjectLinks: item %d: %s@%s\n", i, name, entry.Version)
	}

	log.Printf("[TUI] GetCurrentProjectLinks: completed with %d items\n", len(items))
	return items, nil
}

// shortHash returns the first 8 characters of a hash
func shortHash(hash string) string {
	if len(hash) <= 8 {
		return hash
	}
	return hash[:8]
}