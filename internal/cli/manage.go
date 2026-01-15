package cli

import (
	"fmt"

	"github.com/pedrosousa13/lnpm/internal/tui"
	"github.com/spf13/cobra"
)

// manageCmd starts the interactive TUI for managing links
var manageCmd = &cobra.Command{
	Use:   "manage",
	Short: "Interactive TUI for managing package links",
	Long: `Start an interactive terminal user interface for managing package links.

This command provides a visual interface to:
  - View all published packages and their linked projects
  - Browse projects and their linked packages
  - Manage links for the current project
  - Perform batch operations efficiently

The TUI requires an interactive terminal. In non-interactive environments,
it will fall back to displaying the status information.

Navigation:
  Tab/h/l    Switch between panels
  j/k/↑/↓    Navigate lists
  q/Ctrl+C   Quit
  ?          Show help

Examples:
  lnpm manage              # Start interactive TUI
  lnpm manage --watch      # Start with auto-refresh enabled`,
	RunE: func(cmd *cobra.Command, args []string) error {
		watch, _ := cmd.Flags().GetBool("watch")

		// Check if we're in an interactive terminal
		if !tui.IsTerminalInteractive() {
			fmt.Println("Non-interactive terminal detected. Falling back to status command.")
			return RunStatus()
		}

		fmt.Println("Starting lnpm TUI...")
		if watch {
			fmt.Println("Watch mode enabled (not yet implemented)")
		}

		return tui.Run()
	},
}

func init() {
	manageCmd.Flags().BoolP("watch", "w", false, "Enable auto-refresh when files change")
}