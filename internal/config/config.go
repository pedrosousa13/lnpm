package config

import (
	"os"
	"path/filepath"
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

	// DefaultIgnore is a list of patterns to always ignore in watch mode
	DefaultIgnore []string `yaml:"default_ignore,omitempty"`

	// DebounceMs is the default debounce time for watch mode
	DebounceMs int `yaml:"debounce_ms,omitempty"`

	// ManageGitignore controls automatic .gitignore management (default: true)
	ManageGitignore *bool `yaml:"manage_gitignore,omitempty"`

	// Hooks configuration
	Hooks HooksConfig `yaml:"hooks,omitempty"`
}

// HooksConfig contains hook commands
type HooksConfig struct {
	PrePublish  string `yaml:"pre_publish,omitempty"`
	PostPublish string `yaml:"post_publish,omitempty"`
	PostAdd     string `yaml:"post_add,omitempty"`
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
		LinkMode:   "hardlink",
		DebounceMs: 100,
		DefaultIgnore: []string{
			"node_modules",
			".git",
			"*.log",
		},
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
			LinkMode:   "hardlink",
			DebounceMs: 100,
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

// GetStorePath returns the lnpm store path
func GetStorePath() (string, error) {
	// Check LNPM_STORE environment variable first
	if storePath := os.Getenv("LNPM_STORE"); storePath != "" {
		return storePath, nil
	}

	// Default to ~/.lnpm
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(homeDir, ".lnpm"), nil
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
		{"bun.lockb", Bun},
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
func GetInstallCommand(pm PackageManager) string {
	switch pm {
	case Bun:
		return "bun install"
	case PNPM:
		return "pnpm install"
	case Yarn:
		return "yarn install"
	default:
		return "npm install"
	}
}
