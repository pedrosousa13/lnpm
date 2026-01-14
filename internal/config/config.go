package config

import (
	"os"
	"path/filepath"
)

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
