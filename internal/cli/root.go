package cli

import (
	"fmt"

	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/spf13/cobra"
)

var version = "dev"

// SetVersion sets the CLI version (called from main)
func SetVersion(v string) {
	version = v
}

var rootCmd = &cobra.Command{
	Use:   "lnpm",
	Short: "Local npm package development tool",
	Long: `lnpm is a fast, reliable tool for local npm package development.

It provides a better alternative to yalc with:
  - Hard links for instant, reliable syncing
  - SQLite-backed state tracking
  - Watch mode for automatic updates
  - Full visibility into linked packages

Quick start:
  lnpm publish     # Publish current package to local store
  lnpm add <pkg>   # Add a package to current project
  lnpm push        # Push updates to all linked projects
  lnpm watch       # Watch and auto-sync changes`,
	Version: version,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		debugFlag, _ := cmd.Flags().GetBool("debug")
		debug.Init(debugFlag)
	},
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.SetVersionTemplate(fmt.Sprintf("lnpm version %s\n", version))
	rootCmd.PersistentFlags().BoolP("debug", "d", false, "Enable debug logging")

	// Add subcommands
	rootCmd.AddCommand(publishCmd)
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(pushCmd)
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(gcCmd)
	rootCmd.AddCommand(doctorCmd)
}
