package cli

import (
	"fmt"

	"github.com/pedrosousa13/lnpm/internal/db"
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

With --tag the build is stored under that channel instead of moving 'latest', so
consumers keep the release they are on until they ask for the channel by name
with 'lnpm add <pkg>@<tag>'.

Examples:
  lnpm publish            # Publish current package
  lnpm publish --push     # Publish and update all linked projects
  lnpm publish --all      # Publish all packages in monorepo
  lnpm publish --tag beta # Publish to the beta channel, leaving latest alone
  lnpm publish --dry-run  # List exactly what would be packed, write nothing`,
	RunE: func(cmd *cobra.Command, args []string) error {
		push, _ := cmd.Flags().GetBool("push")
		all, _ := cmd.Flags().GetBool("all")
		skipHooks, _ := cmd.Flags().GetBool("skip-hooks")
		skipValidation, _ := cmd.Flags().GetBool("skip-validation")
		tag, _ := cmd.Flags().GetString("tag")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		return RunPublishWith(PublishOptions{
			Push:           push,
			All:            all,
			SkipHooks:      skipHooks,
			SkipValidation: skipValidation,
			DryRun:         dryRun,
			Tag:            tag,
		})
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

With --link the package is not copied at all: .lnpm/{package} is linked straight
at the source directory it was published from, so every source edit reaches this
project with no further command, and package.json uses link: instead of file:.
That trades away the isolation the default gives you - the project then builds
against files that have not been published, or even committed.

What follows the @ in a spec is matched in three steps, most specific first: as a
dist-tag, so 'lnpm add my-package@beta' links whatever build the beta channel
currently names; then as an exact version, against every version the store has
retained and not only the one latest names; then as a content-hash prefix. The
last two are what roll a project back to a build a later release superseded - run
'lnpm list <pkg> --versions' to see what is there. A spec matching two retained
versions is refused with their hashes rather than resolved to one of them.

A version or a hash names a build rather than a channel, so the link it writes is
pinned: no publish carries it forward, 'lnpm pull' leaves it where it is and
'lnpm gc' keeps that build for as long as the pin names it. A tag does not pin -
following a channel means being carried along it.

A spec with no @ resolves to latest, as it always has, and clears any pin the
package had: it is how a project says it wants to follow the channel again. A tag
cannot be combined with --link: that resolves to the package's source directory,
which is not the build any tag names.

Examples:
  lnpm add my-package            # Add latest version, or unpin a pinned package
  lnpm add pkg1 pkg2 pkg3        # Add multiple packages
  lnpm add my-package@1.0.0      # Pin to a specific version
  lnpm add my-package@a1b2c3d4   # Roll back to a specific published build, pinned
  lnpm add my-package@beta       # Add the build tagged beta
  lnpm add my-package --dev      # Add as devDependency
  lnpm add my-package --install  # Add and run npm install
  lnpm add my-package --pure     # Don't modify package.json
  lnpm add my-package --link     # Link to the live source instead of a store copy`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dev, _ := cmd.Flags().GetBool("dev")
		pure, _ := cmd.Flags().GetBool("pure")
		install, _ := cmd.Flags().GetBool("install")
		link, _ := cmd.Flags().GetBool("link")
		return RunAddMultiple(args, dev, pure, install, link)
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
  lnpm remove my-package             # Remove specific package
  lnpm remove my-package --install   # Remove, then run npm install
  lnpm remove --all                  # Remove all linked packages
  lnpm remove --all --yes            # Remove all without a confirmation prompt`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		yes, _ := cmd.Flags().GetBool("yes")
		install, _ := cmd.Flags().GetBool("install")
		var packageName string
		if len(args) > 0 {
			packageName = args[0]
		}
		if !all && packageName == "" {
			return fmt.Errorf("please specify a package name or use --all")
		}
		return RunRemove(packageName, all, yes, install)
	},
}

// pullCmd refreshes linked packages from the store
var pullCmd = &cobra.Command{
	Use:   "pull [package...]",
	Short: "Sync linked packages with the store",
	Long: `Re-link packages already linked in this project to the version now in
the store, picking up anything published since they were added.

With no arguments every package in lnpm.lock is refreshed.

This command:
  1. Finds the version the channel each package follows now names
  2. Refreshes .lnpm/{package}/
  3. Updates lnpm.lock

A package added as <pkg>@<tag> follows that channel, so pull moves it to
whatever the tag names and never onto ` + db.DefaultTag + `. Everything else follows
` + db.DefaultTag + `, as it always has.

A package added as <pkg>@<version> or <pkg>@<hash> is pinned to that one build
and follows no channel at all. A bare pull leaves it there and says so; naming it
is refused, because there is nothing pull can do for it. Run 'lnpm add <pkg>' to
unpin it and follow ` + db.DefaultTag + ` again.

package.json is never modified: its reference already points at .lnpm/{package}.

Examples:
  lnpm pull              # Refresh every linked package
  lnpm pull my-package   # Refresh one package`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunPull(args)
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

Push goes to the channel the build already in the store carries, so pushing an
unchanged pre-release keeps it a pre-release rather than moving ` + db.DefaultTag + ` onto
it. Content the store does not hold yet is in no channel, so it goes to
` + db.DefaultTag + `; use --tag to say otherwise.

Examples:
  lnpm push              # Push to all linked projects
  lnpm push --tag beta   # Push to the beta channel, leaving latest alone
  lnpm push --skip-hooks # Skip prepare scripts`,
	RunE: func(cmd *cobra.Command, args []string) error {
		skipHooks, _ := cmd.Flags().GetBool("skip-hooks")
		tag, _ := cmd.Flags().GetString("tag")
		return RunPushTagged(skipHooks, tag)
	},
}

// statusCmd shows the current state of lnpm
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current state of all links",
	Long: `Show the current state of lnpm including:
  - Published packages in the store, one row per retained version, with the
    dist-tags naming each
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

--store marks each stored version with the dist-tags naming it, and --projects
names every project consuming the package with the build each is on, whichever
channel it followed to get there.

--versions lists the history of one package: every version the store still
holds, newest first, with its content hash, its version, when it was published,
the dist-tags naming it and the projects on it. That is what 'lnpm add
<pkg>@<hash>' can roll a project back to - the set 'lnpm gc' has not collected,
since a version's record and its files go together.

Examples:
  lnpm list                      # List packages in current project
  lnpm list --store              # List all packages in store
  lnpm list my-package --projects   # List projects using my-package
  lnpm list my-package --versions   # List what my-package can be rolled back to`,
	RunE: func(cmd *cobra.Command, args []string) error {
		showStore, _ := cmd.Flags().GetBool("store")
		showProjects, _ := cmd.Flags().GetBool("projects")
		showVersions, _ := cmd.Flags().GetBool("versions")
		var packageName string
		if len(args) > 0 {
			packageName = args[0]
		}
		if showVersions {
			// Refused rather than ignored: the two listings answer different
			// questions about the same package, so silently serving one of them
			// leaves the user reading an answer to a question they did not ask.
			// RunList refuses a package name it cannot serve for the same reason.
			if showProjects {
				return fmt.Errorf("--versions and --projects cannot be combined: --versions lists what %s can be rolled back to, --projects lists who consumes it", packageName)
			}
			return RunListVersions(packageName)
		}
		return RunList(showStore, packageName, showProjects)
	},
}

// gcCmd garbage collects unused packages
var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Garbage collect unused packages",
	Long: `Remove unused packages from the store.

A stored version goes when no link and no dist-tag reaches it. The ` + db.DefaultTag + `
tag does not count: every publish moves it onto what it just wrote, so treating
it as a reason to keep a version would leave gc nothing it could ever collect.
A version a tag you set names is kept even with nothing linked to it, which is
what a build published to a channel is waiting for. Removing one of those takes
two steps: 'lnpm tag <pkg> <tag> --delete', then 'lnpm gc'.

Examples:
  lnpm gc              # Remove versions no link and no tag reaches
  lnpm gc --dry-run    # Show what would be removed
  lnpm gc --older-than 30d   # Remove packages older than 30 days
  lnpm gc --fix-links  # Clean up orphaned link records
  lnpm gc --yes        # Remove without a confirmation prompt (for scripts)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		olderThan, _ := cmd.Flags().GetString("older-than")
		fixLinks, _ := cmd.Flags().GetBool("fix-links")
		yes, _ := cmd.Flags().GetBool("yes")

		return RunGC(dryRun, olderThan, fixLinks, yes)
	},
}

// forgetCmd drops the record of a project whose filesystem is gone for good
var forgetCmd = &cobra.Command{
	Use:   "forget <project-path>",
	Short: "Drop the record of a project whose filesystem is gone for good",
	Long: `Remove a project and its links from the database.

gc will not collect a version whose only consumer is on a filesystem that is not
mounted where it was recorded: an unplugged drive and a deleted directory look
the same to lnpm, so it declines to judge rather than delete a package that is
still in use. That is the right answer for a drive you are going to plug back
in, and it leaves the space unreclaimable when you are not. This is how you say
you are not.

It removes the record and stops there. The versions those links named become
reachable by nothing, and the next 'lnpm gc' collects the ones no other project
consumes - under gc's own confirmation, so nothing leaves the store here.

A project still on disk is refused. Run 'lnpm retreat' inside it instead.

Examples:
  lnpm forget /mnt/external/myproject         # Ask before dropping the record
  lnpm forget /mnt/external/myproject --yes   # Drop it without a confirmation prompt`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		return RunForget(args[0], yes)
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
  - Cross-filesystem issues

The store is content-addressed, so an entry's directory name is a claim about
the bytes inside it. --verify-content re-reads every stored file and checks that
claim. It is left off by default because it costs one read of the whole store,
and the report says plainly when it was not done.

Examples:
  lnpm doctor                    # The checks that cost no more than a stat
  lnpm doctor --verify-content   # Also re-hash every file in the store`,
	RunE: func(cmd *cobra.Command, args []string) error {
		verifyContent, _ := cmd.Flags().GetBool("verify-content")

		return RunDoctor(verifyContent)
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
  4. Saves lnpm.lock as lnpm.lock.retreat, for 'lnpm restore'

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

// restoreCmd re-links the packages a previous retreat unlinked
var restoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Re-link the packages removed by 'lnpm retreat'",
	Long: `Re-link every package recorded in the snapshot 'lnpm retreat --force' left
behind, so local development can carry on after publishing to npm.

Packages added again since the retreat are kept as they are, and packages the
snapshot never saw are left alone. Nothing is unlinked.

Everything comes back as a store copy (file:.lnpm/<pkg>), because the snapshot
does not record which packages were added with --link. Run 'lnpm add --link
<pkg>' again for the ones that should point back at their live source.

Each package comes back on the exact build the snapshot recorded, found by its
content hash, so a project that was consuming a dist-tagged build gets that
build and not whatever ` + db.DefaultTag + ` names now. The tag itself is not recorded
anywhere, so the restored link follows ` + db.DefaultTag + ` and says so.

A build that is no longer in the store is reported and skipped; the snapshot is
kept so restore can be re-run after publishing it again.

Examples:
  lnpm restore   # Undo the last 'lnpm retreat --force'`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunRestore()
	},
}

// tagCmd manages dist-tags on a package already in the store
var tagCmd = &cobra.Command{
	Use:   "tag <package> <tag>",
	Short: "Point a dist-tag at a published package, or remove one",
	Long: `Manage the dist-tags of a package already in the store, without republishing
it.

Setting points the tag at the version the package currently resolves to - the
one tagged ` + db.DefaultTag + ` - so 'lnpm add <pkg>@<tag>' from then on links that build.
Removing takes the tag off and leaves the version it named in the store.

The ` + db.DefaultTag + ` tag cannot be removed: it is what every lookup by name resolves
through, so deleting it would leave the package published and invisible.

Examples:
  lnpm tag my-package beta            # Point beta at the published version
  lnpm tag my-package beta --delete   # Remove the beta tag`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		del, _ := cmd.Flags().GetBool("delete")
		return RunTag(args[0], args[1], del)
	},
}

// checkCmd scans package.json for leftover lnpm references
var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check the project for lnpm leftovers an npm publish would ship",
	Long: `Report what lnpm has left in the current project that an 'npm publish'
from here would carry into the tarball.

Two things qualify: lnpm references (file:.lnpm/ or link:.lnpm/) in
package.json, left behind by 'lnpm add'; and lnpm.lock.retreat, the snapshot
'lnpm retreat' leaves for 'lnpm restore', when neither the package.json "files"
field nor .npmignore nor .gitignore would keep it out.

In a workspace, the reference scan covers the workspace root's package.json and
every member's as well as this one, and names the package each finding came
from. A workspace whose member list will not resolve fails the check rather
than passing on the manifest here alone.

Exits non-zero if anything is found, so it can be used as a pre-publish guard
in scripts or CI before running 'npm publish'.

Examples:
  lnpm check   # Fails if lnpm has left anything publishable behind`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunCheck()
	},
}

func init() {
	// Register retreat command
	rootCmd.AddCommand(retreatCmd)

	// Register restore command
	rootCmd.AddCommand(restoreCmd)

	// Register check command
	rootCmd.AddCommand(checkCmd)

	// Register tag command
	rootCmd.AddCommand(tagCmd)

	// Register forget command
	rootCmd.AddCommand(forgetCmd)

	// tag flags
	tagCmd.Flags().Bool("delete", false, "Remove the tag instead of setting it")

	// publish flags
	publishCmd.Flags().Bool("push", false, "Push to all linked projects after publish")
	publishCmd.Flags().Bool("all", false, "Publish all packages in monorepo")
	publishCmd.Flags().Bool("skip-hooks", false, "Skip prepare scripts (prepare, prepublishOnly, prepack)")
	publishCmd.Flags().Bool("skip-validation", false, "Skip package validation before publish")
	publishCmd.Flags().String("tag", db.DefaultTag, "Channel to publish to; anything but "+db.DefaultTag+" leaves "+db.DefaultTag+" where it is")
	publishCmd.Flags().Bool("dry-run", false, "Show what would be packed and write nothing (pre_publish and prepare scripts still run, since they are usually what builds those files)")

	// retreat flags
	retreatCmd.Flags().Bool("force", false, "Actually remove everything (required)")
	retreatCmd.Flags().Bool("install", false, "Run npm install after retreat (default: no)")

	// add flags
	addCmd.Flags().Bool("dev", false, "Add as devDependency")
	addCmd.Flags().Bool("pure", false, "Don't modify package.json")
	addCmd.Flags().Bool("install", false, "Run npm install after adding (default: no)")
	addCmd.Flags().Bool("link", false, "Link to the package's live source directory instead of a store copy (the project then sees unpublished, uncommitted files)")

	// remove flags
	removeCmd.Flags().Bool("all", false, "Remove all linked packages")
	removeCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	removeCmd.Flags().Bool("install", false, "Run npm install after removing (default: no)")

	// push flags
	pushCmd.Flags().Bool("skip-hooks", false, "Skip prepare scripts (prepack, prepare) and re-push what is on disk")
	pushCmd.Flags().String("tag", "", "Channel to push to (default: the channel the build already in the store carries)")

	// list flags
	listCmd.Flags().Bool("store", false, "List packages in store")
	listCmd.Flags().Bool("projects", false, "List projects using a package")
	listCmd.Flags().Bool("versions", false, "List every retained version of a package, newest first")

	// gc flags
	gcCmd.Flags().Bool("dry-run", false, "Show what would be removed")
	gcCmd.Flags().String("older-than", "", "Remove packages older than duration (e.g., 30d)")
	gcCmd.Flags().Bool("fix-links", false, "Clean up orphaned link records")
	gcCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	// forget flags
	forgetCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")

	// doctor flags
	doctorCmd.Flags().Bool("verify-content", false, "Re-hash every file in the store and check it against the recorded hashes")
}
