package tui

import (
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// Action represents a user action that can be performed in the TUI
type Action string

const (
	ActionAdd    Action = "add"
	ActionRemove Action = "remove"
	ActionPush   Action = "push"
	ActionOpen   Action = "open"
	ActionUpdate Action = "update"
)

// BatchOperation tracks a batch of unlink operations to be performed
type BatchOperation struct {
	Removals []string // Package names to remove
	Updates  []string // Package names to update
}

// ExecuteAction handles the execution of an action
func ExecuteAction(action Action, item interface{}) error {
	switch action {
	case ActionRemove:
		return handleRemoveAction(item)
	case ActionPush:
		return handlePushAction(item)
	case ActionOpen:
		return handleOpenAction(item)
	case ActionUpdate:
		return handleUpdateAction(item)
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

// handleRemoveAction removes a link from the current project
func handleRemoveAction(item interface{}) error {
	link, ok := item.(LinkItem)
	if !ok {
		return fmt.Errorf("invalid item type for remove action")
	}

	log.Printf("[TUI] Removing link: %s\n", link.Name)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Load the lockfile
	lock, err := lockfile.Load(cwd)
	if err != nil {
		return fmt.Errorf("failed to load lockfile: %w", err)
	}

	// Remove the entry from lockfile
	lock.Remove(link.Name)

	// Save the updated lockfile
	err = lock.Save(cwd)
	if err != nil {
		return fmt.Errorf("failed to save lockfile: %w", err)
	}

	// Unlink the actual files
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Get the package from database
	pkg, err := database.GetPackageByName(link.Name)
	if err != nil {
		return fmt.Errorf("failed to find package: %w", err)
	}

	// Get the project from database
	project, err := database.GetProjectByPath(cwd)
	if err != nil {
		return fmt.Errorf("failed to find project: %w", err)
	}

	// Delete the link from database
	err = database.DeleteLink(pkg.ID, project.ID)
	if err != nil {
		return fmt.Errorf("failed to delete link from database: %w", err)
	}

	log.Printf("[TUI] Successfully removed link for %s\n", link.Name)
	return nil
}

// handlePushAction pushes a package to its source
func handlePushAction(item interface{}) error {
	pkg, ok := item.(PackageItem)
	if !ok {
		return fmt.Errorf("invalid item type for push action")
	}

	log.Printf("[TUI] Pushing package: %s\n", pkg.Name)

	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Get package details from database
	dbPkg, err := database.GetPackageByName(pkg.Name)
	if err != nil {
		return fmt.Errorf("failed to find package: %w", err)
	}

	// Get all projects using this package
	projects, err := database.GetProjectsForPackage(dbPkg.ID)
	if err != nil {
		return fmt.Errorf("failed to get projects for package: %w", err)
	}

	log.Printf("[TUI] Found %d projects using %s\n", len(projects), pkg.Name)

	// Push to each project
	for _, proj := range projects {
		err := pushToProject(pkg.Name, proj.Path)
		if err != nil {
			log.Printf("[TUI] Failed to push to %s: %v\n", proj.Path, err)
			return err
		}
	}

	return nil
}

// pushToProject runs npm install in the specified project to pull updates
func pushToProject(packageName, projectPath string) error {
	log.Printf("[TUI] Pushing %s to %s\n", packageName, projectPath)

	cmd := exec.Command("npm", "install")
	cmd.Dir = projectPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// handleOpenAction opens the package source folder
func handleOpenAction(item interface{}) error {
	pkg, ok := item.(PackageItem)
	if !ok {
		return fmt.Errorf("invalid item type for open action")
	}

	log.Printf("[TUI] Opening package source: %s\n", pkg.SourcePath)

	// Try to open with default app
	var cmd *exec.Cmd
	switch {
	case fileExists("/usr/bin/open"):
		// macOS
		cmd = exec.Command("open", pkg.SourcePath)
	case fileExists("/usr/bin/xdg-open"):
		// Linux
		cmd = exec.Command("xdg-open", pkg.SourcePath)
	case fileExists("C:\\Windows\\System32\\explorer.exe"):
		// Windows
		cmd = exec.Command("explorer", pkg.SourcePath)
	default:
		return fmt.Errorf("could not find file explorer for your system")
	}

	return cmd.Run()
}

// handleUpdateAction updates a package to its latest version
func handleUpdateAction(item interface{}) error {
	pkg, ok := item.(PackageItem)
	if !ok {
		return fmt.Errorf("invalid item type for update action")
	}

	log.Printf("[TUI] Updating package: %s\n", pkg.Name)

	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Get package from database
	dbPkg, err := database.GetPackageByName(pkg.Name)
	if err != nil {
		return fmt.Errorf("failed to find package: %w", err)
	}

	// Get all projects using this package
	projects, err := database.GetProjectsForPackage(dbPkg.ID)
	if err != nil {
		return fmt.Errorf("failed to get projects for package: %w", err)
	}

	// Re-link to all projects (this updates the link)
	for _, proj := range projects {
		err := pushToProject(pkg.Name, proj.Path)
		if err != nil {
			return err
		}
	}

	return nil
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// BatchUnlinkManager manages a batch of unlink operations
type BatchUnlinkManager struct {
	removals []string
}

// NewBatchUnlinkManager creates a new batch unlink manager
func NewBatchUnlinkManager() *BatchUnlinkManager {
	return &BatchUnlinkManager{
		removals: []string{},
	}
}

// AddRemoval queues a package removal
func (b *BatchUnlinkManager) AddRemoval(packageName string) {
	b.removals = append(b.removals, packageName)
}

// Execute performs all queued removals in a single npm install
func (b *BatchUnlinkManager) Execute() error {
	if len(b.removals) == 0 {
		return nil
	}

	log.Printf("[TUI] Executing batch unlink for %d packages\n", len(b.removals))

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Load the lockfile
	lock, err := lockfile.Load(cwd)
	if err != nil {
		return fmt.Errorf("failed to load lockfile: %w", err)
	}

	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Remove all entries from lockfile and database
	for _, pkgName := range b.removals {
		lock.Remove(pkgName)

		pkg, err := database.GetPackageByName(pkgName)
		if err != nil {
			continue
		}

		project, err := database.GetProjectByPath(cwd)
		if err != nil {
			continue
		}

		_ = database.DeleteLink(pkg.ID, project.ID)
	}

	// Save the lockfile once
	err = lock.Save(cwd)
	if err != nil {
		return fmt.Errorf("failed to save lockfile: %w", err)
	}

	// Run npm install once to update node_modules
	log.Printf("[TUI] Running npm install to apply batch changes\n")
	cmd := exec.Command("npm", "install")
	cmd.Dir = cwd
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// HasRemovals checks if there are any queued removals
func (b *BatchUnlinkManager) HasRemovals() bool {
	return len(b.removals) > 0
}

// Removals returns the list of queued removals
func (b *BatchUnlinkManager) Removals() []string {
	return b.removals
}

// Clear clears all queued operations
func (b *BatchUnlinkManager) Clear() {
	b.removals = []string{}
}
