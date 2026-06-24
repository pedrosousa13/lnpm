package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// publishCmd publishes a package to the local store
var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish current package to local store",
	Long: `Publish the current package to the local lnpm store.

This command:
  1. Reads package.json for name and version
  2. Determines which files to include (respects .npmignore, files field)
  3. Calculates a content hash for deduplication
  4. Copies files to ~/.lnpm/store/{name}/{hash}/
  5. Records the package in the database

Examples:
  lnpm publish           # Publish current package
  lnpm publish --push    # Publish and update all linked projects
  lnpm publish --all     # Publish all packages in monorepo`,
	RunE: func(cmd *cobra.Command, args []string) error {
		push, _ := cmd.Flags().GetBool("push")
		all, _ := cmd.Flags().GetBool("all")
		skipHooks, _ := cmd.Flags().GetBool("skip-hooks")
		skipValidation, _ := cmd.Flags().GetBool("skip-validation")
		return RunPublish(push, all, skipHooks, skipValidation)
	},
}

// addCmd adds a package from the store to the current project
var addCmd = &cobra.Command{
	Use:   "add <package> [packages...]",
	Short: "Add packages from store to current project",
	Long: `Add packages from the local store to the current project.

This command:
  1. Finds the packages in the store
  2. Creates hard links in .lnpm/{package}/
  3. Creates a symlink in node_modules/{package}
  4. Updates package.json with file: dependency
  5. Updates lnpm.lock

Examples:
  lnpm add my-package            # Add latest version
  lnpm add pkg1 pkg2 pkg3        # Add multiple packages
  lnpm add my-package@1.0.0      # Add specific version
  lnpm add my-package --dev      # Add as devDependency
  lnpm add my-package --install  # Add and run npm install
  lnpm add my-package --pure     # Don't modify package.json`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dev, _ := cmd.Flags().GetBool("dev")
		pure, _ := cmd.Flags().GetBool("pure")
		install, _ := cmd.Flags().GetBool("install")
		return RunAddMultiple(args, dev, pure, install)
	},
}

// removeCmd removes a linked package from the current project
var removeCmd = &cobra.Command{
	Use:   "remove <package>",
	Short: "Remove a linked package from current project",
	Long: `Remove a linked package from the current project.

This command:
  1. Removes .lnpm/{package}/ directory
  2. Removes node_modules/{package} symlink
  3. Restores original dependency in package.json
  4. Updates lnpm.lock

Examples:
  lnpm remove my-package   # Remove specific package
  lnpm remove --all        # Remove all linked packages`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		var packageName string
		if len(args) > 0 {
			packageName = args[0]
		}
		if !all && packageName == "" {
			return fmt.Errorf("please specify a package name or use --all")
		}
		return RunRemove(packageName, all)
	},
}

// pushCmd pushes updates to all linked projects
var pushCmd = &cobra.Command{
	Use:   "push",
	Short: "Push updates to all linked projects",
	Long: `Push updates from the current package to all linked projects.

This command:
  1. Runs prepare scripts
  2. Packs files
  3. Updates store
  4. Re-links to all linked projects

Examples:
  lnpm push              # Push to all linked projects
  lnpm push --skip-hooks # Skip prepare scripts`,
	RunE: func(cmd *cobra.Command, args []string) error {
		skipHooks, _ := cmd.Flags().GetBool("skip-hooks")
		return RunPush(skipHooks)
	},
}

// statusCmd shows the current state of lnpm
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current state of all links",
	Long: `Show the current state of lnpm including:
  - Published packages in the store
  - Active links between packages and projects

This provides full visibility into what lnpm is managing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunStatus()
	},
}

// listCmd lists packages
var listCmd = &cobra.Command{
	Use:   "list [package]",
	Short: "List packages in project or store",
	Long: `List packages linked in the current project or available in the store.

Examples:
  lnpm list                      # List packages in current project
  lnpm list --store              # List all packages in store
  lnpm list my-package --projects   # List projects using my-package`,
	RunE: func(cmd *cobra.Command, args []string) error {
		showStore, _ := cmd.Flags().GetBool("store")
		showProjects, _ := cmd.Flags().GetBool("projects")
		var packageName string
		if len(args) > 0 {
			packageName = args[0]
		}
		return RunList(showStore, packageName, showProjects)
	},
}

// gcCmd garbage collects unused packages
var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Garbage collect unused packages",
	Long: `Remove unused packages from the store.

Examples:
  lnpm gc              # Remove packages with no links
  lnpm gc --dry-run    # Show what would be removed
  lnpm gc --older-than 30d   # Remove packages older than 30 days
  lnpm gc --fix-links  # Clean up orphaned link records`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		olderThan, _ := cmd.Flags().GetString("older-than")
		fixLinks, _ := cmd.Flags().GetBool("fix-links")

		return RunGC(dryRun, olderThan, fixLinks)
	},
}

// doctorCmd diagnoses and fixes issues
var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose and fix common issues",
	Long: `Check lnpm health and diagnose common issues.

This command checks:
  - Store directory exists and is writable
  - Database integrity
  - Orphaned links and packages
  - Cross-filesystem issues`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunDoctor()
	},
}

// retreatCmd removes all lnpm changes from the current project
var retreatCmd = &cobra.Command{
	Use:   "retreat",
	Short: "Remove all lnpm changes from current project",
	Long: `Remove all lnpm links and restore original dependencies.

This command:
  1. Restores original package.json dependencies
  2. Removes node_modules symlinks
  3. Deletes .lnpm/ directory
  4. Deletes lnpm.lock file

Use this before publishing to npm or when done with local development.

Examples:
  lnpm retreat          # Preview changes
  lnpm retreat --force  # Actually remove everything`,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		install, _ := cmd.Flags().GetBool("install")
		return RunRetreat(force, install)
	},
}

func init() {
	// Register retreat command
	rootCmd.AddCommand(retreatCmd)

	// publish flags
	publishCmd.Flags().Bool("push", false, "Push to all linked projects after publish")
	publishCmd.Flags().Bool("all", false, "Publish all packages in monorepo")
	publishCmd.Flags().Bool("skip-hooks", false, "Skip prepare scripts (prepare, prepublishOnly, prepack)")
	publishCmd.Flags().Bool("skip-validation", false, "Skip package validation before publish")

	// retreat flags
	retreatCmd.Flags().Bool("force", false, "Actually remove everything (required)")
	retreatCmd.Flags().Bool("install", false, "Run npm install after retreat (default: no)")

	// add flags
	addCmd.Flags().Bool("dev", false, "Add as devDependency")
	addCmd.Flags().Bool("pure", false, "Don't modify package.json")
	addCmd.Flags().Bool("install", false, "Run npm install after adding (default: no)")

	// remove flags
	removeCmd.Flags().Bool("all", false, "Remove all linked packages")

	// push flags
	pushCmd.Flags().Bool("skip-hooks", false, "Skip prepare scripts before push")

	// list flags
	listCmd.Flags().Bool("store", false, "List packages in store")
	listCmd.Flags().Bool("projects", false, "List projects using a package")

	// gc flags
	gcCmd.Flags().Bool("dry-run", false, "Show what would be removed")
	gcCmd.Flags().String("older-than", "", "Remove packages older than duration (e.g., 30d)")
	gcCmd.Flags().Bool("fix-links", false, "Clean up orphaned link records")
}
