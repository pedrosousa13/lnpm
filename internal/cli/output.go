package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/ui"
)

// printPeerDependencyTip prints the advice add, remove and restore give when
// they did not run the install themselves.
//
// It takes the project directory rather than reading the process working
// directory because the answer depends on it: config.DetectPackageManager reads
// the project's lock file to decide, so the tip cannot be rendered without
// knowing which project it is about.
//
// The command is derived through config.GetInstallCommand rather than spelled
// out here, so the tip cannot name a command lnpm would not have run. It is the
// same pair of calls retreat makes to run its own install, and the pair
// hooks.RunPostAdd falls back on when no post_add hook is configured — a
// configured post_add command is whatever the user put there, which this tip
// does not claim to know. Spelling the command out is what #384 was: every
// project was told 'npm install', and following that in a pnpm or yarn project
// rewrites the wrong lock file.
//
// For an npm project that derivation yields 'npm install --legacy-peer-deps',
// not a bare 'npm install'. The flag is not incidental to this sentence:
// GetInstallCommand carries it to work around npm/cli#2199, in which file:
// dependencies show as @undefined during peer dependency resolution — the
// operation this tip is advising on.
func printPeerDependencyTip(projectPath string) {
	installCmd := config.GetInstallCommand(config.DetectPackageManager(projectPath))
	fmt.Printf("\n  %s Run '%s' if you need to resolve peer dependencies\n", ui.IconTip(), installCmd)
}

// confirmDecision is the outcome of the confirmation policy: either the answer
// is already known, or the user still has to be asked.
type confirmDecision int

const (
	decisionAsk confirmDecision = iota
	decisionProceed
	decisionAbort
)

// shouldProceed applies the confirmation policy without touching stdin/stdout.
// An explicit --yes always proceeds without asking. Otherwise an interactive
// session is asked, and a non-interactive one aborts with the returned message
// rather than silently destroying data.
func shouldProceed(interactive, yes bool) (confirmDecision, string) {
	if yes {
		return decisionProceed, ""
	}
	if !interactive {
		return decisionAbort, "Refusing to proceed without confirmation; re-run with --yes"
	}
	return decisionAsk, ""
}

// confirm prompts the user for a yes/no answer, defaulting to No. It reads a
// single line from stdin. The prompt is only shown when the session is fully
// interactive (both stdin and stdout are terminals) and yes is not set. When
// yes is set the caller has already confirmed, so it proceeds without asking.
// For scripts, pipes, redirected output, or tests it does not prompt and
// reports false so destructive work never happens unconfirmed.
func confirm(prompt string, yes bool) bool {
	interactive := ui.IsTTY(os.Stdin) && ui.IsTTY(os.Stdout)
	switch decision, msg := shouldProceed(interactive, yes); decision {
	case decisionProceed:
		return true
	case decisionAbort:
		fmt.Printf("%s %s\n", ui.IconWarn(), msg)
		return false
	}
	os.Stdout.WriteString(prompt + " [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
