package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/update"
	"github.com/spf13/cobra"
)

var updateResult <-chan *update.Result

// Build stamps, mirroring the ldflags targets in cmd/lnpm. The placeholder
// values mark a binary that was never stamped at build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// SetVersion sets the CLI version and build stamps (called from main)
func SetVersion(v, c, d string) {
	version, commit, date = v, c, d
	applyVersion()
}

// applyVersion pushes the current build stamps onto the root command. Both
// init and SetVersion go through here so the two can never report differently.
func applyVersion() {
	rootCmd.Version = version
	rootCmd.SetVersionTemplate(versionTemplate(version, commit, date))
}

// versionTemplate renders the --version output. Commit and date are omitted
// when the binary carries no build stamp, rather than printing placeholders
// that answer nobody's question about which commit a binary came from.
func versionTemplate(ver, sha, builtAt string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "lnpm version %s\n", ver)
	if sha != "" && sha != "none" {
		fmt.Fprintf(&b, "commit: %s\n", sha)
	}
	if builtAt != "" && builtAt != "unknown" {
		fmt.Fprintf(&b, "built:  %s\n", builtAt)
	}
	return b.String()
}

var rootCmd = &cobra.Command{
	Use:   "lnpm",
	Short: "Local npm package development tool",
	Long: `lnpm is a fast, reliable tool for local npm package development.

It provides a better alternative to yalc with:
  - Hard links for instant, reliable syncing
  - bbolt-backed state tracking
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
	applyVersion()
	rootCmd.PersistentFlags().BoolP("debug", "d", false, "Enable debug logging")

	// Add subcommands
	rootCmd.AddCommand(publishCmd)
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(pullCmd)
	rootCmd.AddCommand(pushCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(gcCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(updateCmd)
}
