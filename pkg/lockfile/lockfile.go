package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// LockFile represents the lnpm.lock file format
type LockFile struct {
	Version  int                `yaml:"version"`
	Packages map[string]Package `yaml:"packages"`
}

// Package represents a locked package entry
type Package struct {
	Version         string    `yaml:"version"`
	Hash            string    `yaml:"hash"`
	Source          string    `yaml:"source"`
	Linked          time.Time `yaml:"linked"`
	OriginalVersion string    `yaml:"originalVersion,omitempty"` // For restore
}

const (
	lockFileName   = "lnpm.lock"
	currentVersion = 1
)

// Load reads a lock file from a project directory
func Load(projectPath string) (*LockFile, error) {
	path := filepath.Join(projectPath, lockFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty lock file
			return &LockFile{
				Version:  currentVersion,
				Packages: make(map[string]Package),
			}, nil
		}
		return nil, fmt.Errorf("failed to read lock file: %w", err)
	}

	var lock LockFile
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("failed to parse lock file: %w", err)
	}

	// Ensure packages map exists
	if lock.Packages == nil {
		lock.Packages = make(map[string]Package)
	}

	return &lock, nil
}

// Save writes the lock file to a project directory
func (l *LockFile) Save(projectPath string) error {
	path := filepath.Join(projectPath, lockFileName)

	data, err := yaml.Marshal(l)
	if err != nil {
		return fmt.Errorf("failed to marshal lock file: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write lock file: %w", err)
	}

	return nil
}

// Add adds or updates a package in the lock file
func (l *LockFile) Add(name string, pkg Package) {
	l.Packages[name] = pkg
}

// Remove removes a package from the lock file
func (l *LockFile) Remove(name string) {
	delete(l.Packages, name)
}

// Get returns a package from the lock file
func (l *LockFile) Get(name string) (Package, bool) {
	pkg, ok := l.Packages[name]
	return pkg, ok
}

// Has checks if a package is in the lock file
func (l *LockFile) Has(name string) bool {
	_, ok := l.Packages[name]
	return ok
}

// List returns all package names in the lock file
func (l *LockFile) List() []string {
	names := make([]string, 0, len(l.Packages))
	for name := range l.Packages {
		names = append(names, name)
	}
	return names
}
