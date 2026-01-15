package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// uninstallCmd removes lnpm from the system
var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall lnpm from the system",
	Long: `Remove lnpm and all its data from your system.

This command:
  1. Removes the lnpm binary from its installation location
  2. Removes the ~/.lnpm directory (store, database, and all packages)
  3. Optionally removes the lnpm.lock files from linked projects

WARNING: This will delete all packages in your lnpm store and break all existing links.

Examples:
  lnpm uninstall          # Remove lnpm binary and store
  lnpm uninstall --force  # Skip confirmation prompts`,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		return RunUninstall(force)
	},
}

// RunUninstall removes lnpm from the system
func RunUninstall(force bool) error {
	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	lnpmDir := filepath.Join(homeDir, ".lnpm")

	// Confirm with user unless --force is set
	if !force {
		fmt.Printf("This will remove:\n")
		fmt.Printf("  - %s (lnpm binary)\n", getBinaryPath())
		fmt.Printf("  - %s (lnpm store, database, and all packages)\n", lnpmDir)
		fmt.Printf("\nThis action cannot be undone.\n")
		fmt.Printf("Are you sure? (type 'yes' to confirm): ")

		var input string
		_, err := fmt.Scanln(&input)
		if err != nil || input != "yes" {
			fmt.Println("Uninstall cancelled")
			return nil
		}
	}

	// Remove ~/.lnpm directory
	if err := os.RemoveAll(lnpmDir); err != nil {
		return fmt.Errorf("failed to remove %s: %w", lnpmDir, err)
	}
	fmt.Printf("✓ Removed %s\n", lnpmDir)

	// Try to remove the binary
	binaryPath := getBinaryPath()
	if binaryPath != "" {
		if err := os.Remove(binaryPath); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("failed to remove binary: %w", err)
			}
		} else {
			fmt.Printf("✓ Removed %s\n", binaryPath)
		}
	}

	fmt.Println("\nUninstall complete. lnpm has been removed from your system.")
	return nil
}

// getBinaryPath attempts to find the lnpm binary location
func getBinaryPath() string {
	// Try common installation paths
	installDir := os.Getenv("LNPM_INSTALL_DIR")
	if installDir == "" {
		installDir = filepath.Join(os.Getenv("HOME"), ".local", "bin")
	}

	binaryPath := filepath.Join(installDir, "lnpm")
	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath
	}

	// Try /usr/local/bin
	binaryPath = "/usr/local/bin/lnpm"
	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath
	}

	// Try /usr/bin
	binaryPath = "/usr/bin/lnpm"
	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath
	}

	// Return the default path (might not exist)
	return filepath.Join(os.Getenv("HOME"), ".local", "bin", "lnpm")
}

func init() {
	// Register uninstall command
	rootCmd.AddCommand(uninstallCmd)

	// uninstall flags
	uninstallCmd.Flags().Bool("force", false, "Skip confirmation prompts")
}
