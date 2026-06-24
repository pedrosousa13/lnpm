package cli

import (
	"fmt"
	"time"

	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/update"
	"github.com/spf13/cobra"
)

var updateResult <-chan *update.Result

var version = "dev"

// SetVersion sets the CLI version (called from main)
func SetVersion(v string) {
	version = v
	rootCmd.Version = v
	rootCmd.SetVersionTemplate(fmt.Sprintf("lnpm version %s\n", v))
}

var rootCmd = &cobra.Command{
	Use:   "lnpm",
	Short: "Local npm package development tool",
	Long: `lnpm is a fast, reliable tool for local npm package development.

It provides a better alternative to yalc with:
  - Hard links for instant, reliable syncing
  - SQLite-backed state tracking
  - Full visibility into linked packages

Quick start:
  lnpm publish     # Publish current package to local store
  lnpm add <pkg>   # Add a package to current project
  lnpm push        # Push updates to all linked projects`,
	Version: version,
	// Don't dump the full usage/help text after a runtime error; the error
	// message alone is what the user needs.
	SilenceUsage: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		debugFlag, _ := cmd.Flags().GetBool("debug")
		debug.Init(debugFlag)
		// Start async version check (non-blocking)
		updateResult = update.CheckAsync(version)
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		// Wait briefly for update check result
		if updateResult == nil {
			return
		}
		select {
		case result := <-updateResult:
			update.PrintUpdateNotice(result)
		case <-time.After(500 * time.Millisecond):
			// Check took too long, skip
		}
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
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(gcCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(updateCmd)
}
