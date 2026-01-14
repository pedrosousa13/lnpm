package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion script",
	Long: `Generate shell completion script for lnpm.

To load completions:

Bash:
  $ source <(lnpm completion bash)

  # To load completions for each session, execute once:
  # Linux:
  $ lnpm completion bash > /etc/bash_completion.d/lnpm
  # macOS:
  $ lnpm completion bash > $(brew --prefix)/etc/bash_completion.d/lnpm

Zsh:
  # If shell completion is not already enabled in your environment,
  # you will need to enable it. You can execute the following once:
  $ echo "autoload -U compinit; compinit" >> ~/.zshrc

  # To load completions for each session, execute once:
  $ lnpm completion zsh > "${fpath[1]}/_lnpm"

  # You will need to start a new shell for this setup to take effect.

Fish:
  $ lnpm completion fish | source

  # To load completions for each session, execute once:
  $ lnpm completion fish > ~/.config/fish/completions/lnpm.fish

PowerShell:
  PS> lnpm completion powershell | Out-String | Invoke-Expression

  # To load completions for every new session, run:
  PS> lnpm completion powershell > lnpm.ps1
  # and source this file from your PowerShell profile.
`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return cmd.Root().GenBashCompletion(os.Stdout)
		case "zsh":
			return cmd.Root().GenZshCompletion(os.Stdout)
		case "fish":
			return cmd.Root().GenFishCompletion(os.Stdout, true)
		case "powershell":
			return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
		default:
			return fmt.Errorf("unknown shell: %s", args[0])
		}
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
}
