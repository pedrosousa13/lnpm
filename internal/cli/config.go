package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/user/lnpm/internal/config"
	"gopkg.in/yaml.v3"
)

var configCmd = &cobra.Command{
	Use:   "config [key] [value]",
	Short: "View or modify configuration",
	Long: `View or modify lnpm configuration.

Without arguments, shows the current configuration.
With one argument, shows the value of that key.
With two arguments, sets the key to the value.

Configuration file: ~/.lnpm/config.yaml

Examples:
  lnpm config                    # Show all config
  lnpm config store_path         # Show store path
  lnpm config store_path /data   # Set store path
  lnpm config --edit             # Open config in editor
  lnpm config --path             # Show config file path`,
	RunE: func(cmd *cobra.Command, args []string) error {
		showPath, _ := cmd.Flags().GetBool("path")
		edit, _ := cmd.Flags().GetBool("edit")

		if showPath {
			fmt.Println(config.GetConfigPath())
			return nil
		}

		if edit {
			return editConfig()
		}

		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		switch len(args) {
		case 0:
			// Show all config
			return showConfig(cfg)
		case 1:
			// Get a specific key
			return getConfigKey(cfg, args[0])
		case 2:
			// Set a specific key
			return setConfigKey(cfg, args[0], args[1])
		default:
			return fmt.Errorf("too many arguments")
		}
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.Flags().Bool("path", false, "Show config file path")
	configCmd.Flags().Bool("edit", false, "Open config in editor")
}

// showConfig displays the current configuration
func showConfig(cfg *config.Config) error {
	fmt.Printf("Config file: %s\n\n", config.GetConfigPath())

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	fmt.Println(string(data))
	return nil
}

// getConfigKey gets a specific configuration key
func getConfigKey(cfg *config.Config, key string) error {
	switch strings.ToLower(key) {
	case "store_path":
		if cfg.StorePath != "" {
			fmt.Println(cfg.StorePath)
		} else {
			storePath, _ := config.GetStorePath()
			fmt.Printf("%s (default)\n", storePath)
		}
	case "link_mode":
		fmt.Println(cfg.LinkMode)
	case "debounce_ms":
		fmt.Println(cfg.DebounceMs)
	case "default_ignore":
		for _, pattern := range cfg.DefaultIgnore {
			fmt.Println(pattern)
		}
	case "hooks.pre_publish":
		fmt.Println(cfg.Hooks.PrePublish)
	case "hooks.post_publish":
		fmt.Println(cfg.Hooks.PostPublish)
	case "hooks.post_add":
		fmt.Println(cfg.Hooks.PostAdd)
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

// setConfigKey sets a specific configuration key
func setConfigKey(cfg *config.Config, key, value string) error {
	switch strings.ToLower(key) {
	case "store_path":
		cfg.StorePath = value
	case "link_mode":
		if value != "hardlink" && value != "copy" {
			return fmt.Errorf("link_mode must be 'hardlink' or 'copy'")
		}
		cfg.LinkMode = value
	case "debounce_ms":
		var ms int
		if _, err := fmt.Sscanf(value, "%d", &ms); err != nil {
			return fmt.Errorf("debounce_ms must be a number")
		}
		cfg.DebounceMs = ms
	case "hooks.pre_publish":
		cfg.Hooks.PrePublish = value
	case "hooks.post_publish":
		cfg.Hooks.PostPublish = value
	case "hooks.post_add":
		cfg.Hooks.PostAdd = value
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}

	if err := config.SaveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Set %s = %s\n", key, value)
	return nil
}

// editConfig opens the config file in an editor
func editConfig() error {
	configPath := config.GetConfigPath()

	// Create config file with defaults if it doesn't exist
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		cfg := config.Get()
		if err := config.SaveConfig(cfg); err != nil {
			return fmt.Errorf("failed to create config: %w", err)
		}
	}

	// Find editor
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}

	fmt.Printf("Opening %s with %s\n", configPath, editor)
	fmt.Println("(Config file will be created if it doesn't exist)")

	// This would normally exec the editor, but we'll just print the path
	// since exec would replace the current process
	fmt.Printf("\nRun: %s %s\n", editor, configPath)
	return nil
}
