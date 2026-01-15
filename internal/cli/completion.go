package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion script",
	Long: `Generate shell completion script for lnpm.

Quick setup:
  $ lnpm completion install    # Auto-installs for your shell

Alternative - Dynamic loading (add to your shell config):
  Zsh:   eval "$(lnpm completion zsh)"
  Bash:  eval "$(lnpm completion bash)"
  Fish:  lnpm completion fish | source

Manual installation:
  $ lnpm completion zsh > ~/.zsh/completions/_lnpm    # Then add to fpath
  $ lnpm completion bash > /etc/bash_completion.d/lnpm
  $ lnpm completion fish > ~/.config/fish/completions/lnpm.fish
`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell", "install"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "install":
			return installCompletion()
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

func installCompletion() error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return fmt.Errorf("unable to detect shell from $SHELL environment variable")
	}

	// Detect shell type from path
	shellName := filepath.Base(shell)

	switch shellName {
	case "zsh":
		return installZshCompletion()
	case "bash":
		return installBashCompletion()
	case "fish":
		return installFishCompletion()
	default:
		return fmt.Errorf("unsupported shell: %s\n\nTry:\n  eval \"$(lnpm completion %s)\"", shellName, shellName)
	}
}

func installZshCompletion() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Create user completion directory
	compDir := filepath.Join(home, ".zsh", "completions")
	if err := os.MkdirAll(compDir, 0755); err != nil {
		return fmt.Errorf("failed to create completion directory: %w", err)
	}

	// Generate completion file
	compFile := filepath.Join(compDir, "_lnpm")
	file, err := os.Create(compFile)
	if err != nil {
		return fmt.Errorf("failed to create completion file: %w", err)
	}
	defer file.Close()

	if err := rootCmd.GenZshCompletion(file); err != nil {
		return fmt.Errorf("failed to generate completion: %w", err)
	}

	fmt.Printf("✓ Installed zsh completion to %s\n\n", compFile)
	fmt.Println("Add this to your ~/.zshrc (if not already present):")
	fmt.Printf("\n  fpath=(~/.zsh/completions $fpath)\n")
	fmt.Printf("  autoload -U compinit && compinit\n\n")
	fmt.Println("Then reload your shell: exec zsh")

	return nil
}

func installBashCompletion() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	var compDir string
	if runtime.GOOS == "darwin" {
		// Try Homebrew location first
		brewPrefix := os.Getenv("HOMEBREW_PREFIX")
		if brewPrefix == "" {
			brewPrefix = "/usr/local" // Default for Intel Macs
			if runtime.GOARCH == "arm64" {
				brewPrefix = "/opt/homebrew" // Default for Apple Silicon
			}
		}
		compDir = filepath.Join(brewPrefix, "etc", "bash_completion.d")

		// Fallback to user directory if Homebrew dir doesn't exist
		if _, err := os.Stat(compDir); os.IsNotExist(err) {
			compDir = filepath.Join(home, ".bash_completion.d")
		}
	} else {
		// Linux - try system directory, fall back to user
		compDir = "/etc/bash_completion.d"
		if _, err := os.Stat(compDir); os.IsNotExist(err) {
			compDir = filepath.Join(home, ".bash_completion.d")
		}
	}

	if err := os.MkdirAll(compDir, 0755); err != nil {
		return fmt.Errorf("failed to create completion directory: %w", err)
	}

	compFile := filepath.Join(compDir, "lnpm")
	file, err := os.Create(compFile)
	if err != nil {
		return fmt.Errorf("failed to create completion file: %w", err)
	}
	defer file.Close()

	if err := rootCmd.GenBashCompletion(file); err != nil {
		return fmt.Errorf("failed to generate completion: %w", err)
	}

	fmt.Printf("✓ Installed bash completion to %s\n\n", compFile)

	// Check if completion is in user home directory (not system-wide)
	relPath, err := filepath.Rel(home, compDir)
	if err == nil && !filepath.IsAbs(relPath) && relPath != ".." && !filepath.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		fmt.Println("Add this to your ~/.bashrc (if not already present):")
		fmt.Printf("\n  [ -f ~/.bash_completion.d/lnpm ] && . ~/.bash_completion.d/lnpm\n\n")
	}

	fmt.Println("Then reload your shell: exec bash")

	return nil
}

func installFishCompletion() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	compDir := filepath.Join(home, ".config", "fish", "completions")
	if err := os.MkdirAll(compDir, 0755); err != nil {
		return fmt.Errorf("failed to create completion directory: %w", err)
	}

	compFile := filepath.Join(compDir, "lnpm.fish")
	file, err := os.Create(compFile)
	if err != nil {
		return fmt.Errorf("failed to create completion file: %w", err)
	}
	defer file.Close()

	if err := rootCmd.GenFishCompletion(file, true); err != nil {
		return fmt.Errorf("failed to generate completion: %w", err)
	}

	fmt.Printf("✓ Installed fish completion to %s\n\n", compFile)
	fmt.Println("Completions will be available in new fish sessions.")

	return nil
}

func init() {
	rootCmd.AddCommand(completionCmd)
}
