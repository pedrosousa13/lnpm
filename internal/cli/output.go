package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/pedrosousa13/lnpm/internal/ui"
)

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
