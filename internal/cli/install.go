package cli

import (
	"fmt"
	"os"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/shellcmd"
	"github.com/pedrosousa13/lnpm/internal/ui"
)

// runProjectInstallFn is runProjectInstall behind a variable so a test can check
// whether a command decided to install without one running: the real thing runs
// every transitive dependency's install scripts, which a test must never start.
// RunRemove and RunRetreat both go through it. Production code never reassigns
// it.
//
// The print is inside the helper because installCmd is: "Running %s..." names
// it, and runProjectInstall is what computes it, via config.DetectPackageManager
// and then config.GetInstallCommand. Hoisting the print to the call sites would
// mean calling both of those a second time, at both sites - the exact
// duplication this extraction removes.
//
// The seam is what a test needs regardless of where the print lives: stdout
// alone cannot tell an install that was suppressed from one that was merely not
// announced, because "Running %s..." prints before the command starts - gating
// the print alone would pass every output assertion while the install still
// runs. TestRunRetreatInstallsOnlyWhenAsked pins that direction by counting
// calls to this variable instead.
var runProjectInstallFn = runProjectInstall

// runProjectInstall runs the project's package manager install in dir, so
// node_modules matches the package.json its caller just rewrote.
//
// A failed install is a warning and not an error. Both callers reach here with
// their own work already on disk, so the removal or the retreat has happened
// whatever the package manager does next, and the caller's own outcome is what
// stands.
func runProjectInstall(dir string) {
	pm := config.DetectPackageManager(dir)
	installCmd := config.GetInstallCommand(pm)
	fmt.Printf("Running %s...\n", installCmd)

	// Stdin is left nil, exactly as both call sites left it before this helper
	// existed. os/exec documents that as giving the process the null device, so
	// a package manager that prompts here reads EOF rather than the user's
	// terminal - unlike hooks.runScript, which does pass os.Stdin. #385 named
	// the difference as pre-existing on both paths and out of scope, so the move
	// into this helper preserved it rather than settling it in passing.
	cmd := shellcmd.Command(installCmd)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("%s Install failed: %v\n", ui.IconWarn(), err)
	}
}
