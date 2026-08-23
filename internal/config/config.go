package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pedrosousa13/lnpm/internal/debug"
	"gopkg.in/yaml.v3"
)

// Config represents the lnpm configuration
type Config struct {
	// StorePath is the path to the lnpm store (default: ~/.lnpm)
	StorePath string `yaml:"store_path,omitempty"`

	// LinkMode is the default link mode: "hardlink" or "copy"
	LinkMode string `yaml:"link_mode,omitempty"`

	// ManageGitignore controls automatic .gitignore management (default: true)
	ManageGitignore *bool `yaml:"manage_gitignore,omitempty"`

	// FollowSymlinkedNodeModules permits lnpm to create and delete through a
	// node_modules - or a scope directory beneath it - that is not a real
	// directory in the project. Off by default, because a repository can commit
	// a link at either name and aim lnpm's writes and its recursive delete at
	// whatever it points at. Relocating node_modules to another volume, a RAM
	// disk or out of a synced folder is a setup people really run, which is why
	// there is a way to say yes at all.
	//
	// A link is what it is named for and what it exists for, but what it
	// actually waives is the whole check: a regular file, a fifo or a device at
	// either path is accepted too, exactly as it was before the check existed.
	FollowSymlinkedNodeModules bool `yaml:"follow_symlinked_node_modules,omitempty"`

	// Hooks configuration
	Hooks HooksConfig `yaml:"hooks,omitempty"`
}

// HooksConfig contains hook commands and settings
type HooksConfig struct {
	PrePublish  string `yaml:"pre_publish,omitempty"`
	PostPublish string `yaml:"post_publish,omitempty"`
	PostAdd     string `yaml:"post_add,omitempty"`
	SkipPrepare bool   `yaml:"skip_prepare,omitempty"`  // Skip prepare/prepublishOnly/prepack scripts
	SkipPostAdd bool   `yaml:"skip_post_add,omitempty"` // Skip post-add hook (npm install)
}

var (
	globalConfig     *Config
	globalConfigOnce sync.Once
	globalConfigErr  error
)

// LoadConfig loads the global configuration
func LoadConfig() (*Config, error) {
	globalConfigOnce.Do(func() {
		globalConfig, globalConfigErr = loadConfigFile()
	})
	return globalConfig, globalConfigErr
}

// loadConfigFile reads the config file from disk
func loadConfigFile() (*Config, error) {
	cfg := &Config{
		LinkMode: "hardlink",
	}

	// Find config file
	configPath := getConfigPath()
	debug.Logf("config: loading from %s", configPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // Return defaults
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// SaveConfig saves the configuration to disk
func SaveConfig(cfg *Config) error {
	configPath := getConfigPath()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

// ResetForTesting clears the memoized config so a test can point LNPM_CONFIG at
// a different file and have it take effect.
// This should only be used in tests.
func ResetForTesting() {
	globalConfig = nil
	globalConfigErr = nil
	globalConfigOnce = sync.Once{}
}

// getConfigPath returns the path to the config file
func getConfigPath() string {
	if configPath := os.Getenv("LNPM_CONFIG"); configPath != "" {
		return configPath
	}

	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".lnpm", "config.yaml")
}

// GetConfigPath exports the config path for CLI
func GetConfigPath() string {
	return getConfigPath()
}

// Get returns the loaded configuration (or defaults)
func Get() *Config {
	cfg, _ := LoadConfig()
	if cfg == nil {
		return &Config{
			LinkMode: "hardlink",
		}
	}
	return cfg
}

// ShouldManageGitignore returns whether .gitignore should be auto-managed
func (c *Config) ShouldManageGitignore() bool {
	if c.ManageGitignore == nil {
		return true // default: enabled
	}
	return *c.ManageGitignore
}

// PackageManager represents a supported package manager
type PackageManager string

const (
	NPM  PackageManager = "npm"
	Yarn PackageManager = "yarn"
	PNPM PackageManager = "pnpm"
	Bun  PackageManager = "bun"
)

// GetStorePath returns the lnpm store path, honoring (in order): the
// LNPM_STORE env var, the store_path config option, then ~/.lnpm.
func GetStorePath() (string, error) {
	// 1. LNPM_STORE env var wins (used by tests/CI and one-off overrides).
	if storePath := os.Getenv("LNPM_STORE"); storePath != "" {
		return storePath, nil
	}

	// 2. store_path from config, if set.
	if cfg, err := LoadConfig(); err == nil && cfg != nil && cfg.StorePath != "" {
		return expandPath(cfg.StorePath)
	}

	// 3. Default to ~/.lnpm
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(homeDir, ".lnpm"), nil
}

// expandPath expands a leading ~ to the user's home directory.
func expandPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// GetPackageStorePath returns the path to the package store
func GetPackageStorePath() (string, error) {
	storePath, err := GetStorePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(storePath, "store"), nil
}

// DetectPackageManager detects the package manager used in a project directory
func DetectPackageManager(projectPath string) PackageManager {
	// Check for lock files in order of preference
	lockFiles := []struct {
		file    string
		manager PackageManager
	}{
		{"bun.lockb", Bun}, // Bun < 1.2 binary lockfile
		{"bun.lock", Bun},  // Bun 1.2+ text lockfile
		{"pnpm-lock.yaml", PNPM},
		{"yarn.lock", Yarn},
		{"package-lock.json", NPM},
	}

	for _, lf := range lockFiles {
		if _, err := os.Stat(filepath.Join(projectPath, lf.file)); err == nil {
			return lf.manager
		}
	}

	// Default to npm if no lock file found
	return NPM
}

// GetInstallCommand returns the install command for a package manager
// For npm, uses --legacy-peer-deps to work around npm bug with file: dependencies
// showing as @undefined during peer dep resolution (npm/cli#2199)
func GetInstallCommand(pm PackageManager) string {
	switch pm {
	case Bun:
		return "bun install"
	case PNPM:
		return "pnpm install"
	case Yarn:
		return "yarn install"
	default:
		return "npm install --legacy-peer-deps"
	}
}
