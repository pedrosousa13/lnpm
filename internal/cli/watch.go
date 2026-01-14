package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/user/lnpm/internal/db"
	"github.com/user/lnpm/internal/watch"
)

// RunWatch executes the watch command
func RunWatch(execCmd string, ignorePatterns []string, debounceMs int) error {
	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Validate we're in a package with package.json
	pkgJSONPath := cwd + "/package.json"
	if _, err := os.Stat(pkgJSONPath); err != nil {
		return fmt.Errorf("no package.json found in current directory")
	}

	// Check if package is published
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Get package name from package.json
	pkgJSON, err := readPackageJSON(cwd)
	if err != nil {
		return fmt.Errorf("failed to read package.json: %w", err)
	}

	pkg, err := database.GetPackageByName(pkgJSON.Name)
	if err != nil {
		return fmt.Errorf("failed to look up package: %w", err)
	}

	if pkg == nil {
		return fmt.Errorf("package %s not published yet. Run 'lnpm publish' first", pkgJSON.Name)
	}

	// Get linked projects count
	projects, err := database.GetProjectsForPackage(pkg.ID)
	if err != nil {
		return fmt.Errorf("failed to get linked projects: %w", err)
	}

	fmt.Printf("Watching %s@%s\n", pkgJSON.Name, pkgJSON.Version)
	fmt.Printf("  Linked to %d project(s)\n", len(projects))
	if execCmd != "" {
		fmt.Printf("  Exec: %s\n", execCmd)
	}
	if len(ignorePatterns) > 0 {
		fmt.Printf("  Ignore: %s\n", strings.Join(ignorePatterns, ", "))
	}
	fmt.Printf("  Debounce: %dms\n", debounceMs)
	fmt.Println()
	fmt.Println("Watching for changes... (Ctrl+C to stop)")
	fmt.Println()

	// Create watcher
	w, err := watch.New(cwd, watch.Options{
		IgnorePatterns: ignorePatterns,
		DebounceMs:     debounceMs,
		ExecCmd:        execCmd,
		OnSync: func(files []string, projectCount int) {
			// Run exec command if specified
			if execCmd != "" {
				fmt.Printf("Running: %s\n", execCmd)
				cmd := exec.Command("sh", "-c", execCmd)
				cmd.Dir = cwd
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					fmt.Printf("  ✗ Exec failed: %v\n", err)
					return
				}
			}

			timestamp := time.Now().Format("15:04:05")
			if len(files) == 1 {
				fmt.Printf("[%s] ✓ Synced %s → %d project(s)\n", timestamp, files[0], projectCount)
			} else {
				fmt.Printf("[%s] ✓ Synced %d files → %d project(s)\n", timestamp, len(files), projectCount)
			}
		},
		OnError: func(err error) {
			fmt.Printf("✗ Error: %v\n", err)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	// Start watching
	if err := w.Start(); err != nil {
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	// Wait for Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nStopping watch...")
	w.Stop()

	return nil
}

// packageJSONInfo holds minimal package.json info
type packageJSONInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// readPackageJSON reads package.json and returns basic info
func readPackageJSON(dir string) (*packageJSONInfo, error) {
	data, err := os.ReadFile(dir + "/package.json")
	if err != nil {
		return nil, err
	}

	var pkgJSON packageJSONInfo
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return nil, err
	}

	return &pkgJSON, nil
}
