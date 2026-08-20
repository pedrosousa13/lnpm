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
	lockFileName = "lnpm.lock"
	// retreatFileName is the snapshot `lnpm retreat` leaves behind in place of
	// the lock file it removes, so `lnpm restore` can put the links back.
	retreatFileName = lockFileName + ".retreat"
	currentVersion  = 1
)

// Path returns the lock file path for a project directory.
func Path(projectPath string) string {
	return filepath.Join(projectPath, lockFileName)
}

// RetreatPath returns the path of the retreat snapshot for a project directory.
func RetreatPath(projectPath string) string {
	return filepath.Join(projectPath, retreatFileName)
}

// Load reads a lock file from a project directory
func Load(projectPath string) (*LockFile, error) {
	lock, err := read(Path(projectPath))
	if err != nil {
		return nil, err
	}
	if lock == nil {
		// A missing lock file reads as an empty one.
		return &LockFile{
			Version:  currentVersion,
			Packages: make(map[string]Package),
		}, nil
	}
	return lock, nil
}

// LoadRetreat reads the retreat snapshot from a project directory. It returns
// nil when there is no snapshot, which callers must tell apart from a snapshot
// holding no packages: the first means no retreat has run, the second a retreat
// that had nothing to record.
func LoadRetreat(projectPath string) (*LockFile, error) {
	return read(RetreatPath(projectPath))
}

// read parses the lock file at path, returning nil when the file does not exist.
func read(path string) (*LockFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
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
	path := Path(projectPath)

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
