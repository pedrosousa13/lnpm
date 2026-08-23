package lockfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/pedrosousa13/lnpm/internal/fsutil"
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
	// RetreatFileName is the snapshot `lnpm retreat` leaves behind in place of
	// the lock file it removes, so `lnpm restore` can put the links back. It is
	// exported because it is lnpm's own state sitting in a project root, which
	// anything that decides what belongs to a project - packing a publish, above
	// all - has to be able to recognise without spelling the name again.
	RetreatFileName = lockFileName + ".retreat"
	currentVersion  = 1
)

// Path returns the lock file path for a project directory.
func Path(projectPath string) string {
	return filepath.Join(projectPath, lockFileName)
}

// RetreatPath returns the path of the retreat snapshot for a project directory.
func RetreatPath(projectPath string) string {
	return filepath.Join(projectPath, RetreatFileName)
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
//
// The errors name the file they came from rather than calling it "the lock
// file": the snapshot shares this reader, and a message that named the format
// instead of the file would send a user whose snapshot is corrupt to inspect a
// lock file that is perfectly fine.
//
// The read is capped before the unmarshal, because yaml.v3's parse cost is
// superlinear and a project can ship whatever lock file it likes - see
// fsutil.MaxYAMLBytes. The refusal is passed through unwrapped: it already names
// the file, and "failed to read X: X is N bytes" would give one file two
// spellings in one message.
func read(path string) (*LockFile, error) {
	data, err := fsutil.ReadFileCapped(path, fsutil.MaxYAMLBytes)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		if errors.Is(err, fsutil.ErrFileTooLarge) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	var lock LockFile
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	// Ensure packages map exists
	if lock.Packages == nil {
		lock.Packages = make(map[string]Package)
	}

	// A file with no "version:" key unmarshals as version 0, which is not a
	// format anything ever wrote. Normalising here rather than at the call sites
	// keeps a file that is read and written back - a merging retreat writes the
	// snapshot it just read - from persisting that 0 as if it meant something.
	if lock.Version == 0 {
		lock.Version = currentVersion
	}

	return &lock, nil
}

// Save writes the lock file to a project directory
func (l *LockFile) Save(projectPath string) error {
	return l.write(Path(projectPath))
}

// SaveRetreat writes the lock file as the retreat snapshot of a project
// directory. `lnpm retreat` needs it when a snapshot from an earlier retreat is
// still unconsumed: the two have to be merged and written out, which a rename of
// the lock file cannot do.
func (l *LockFile) SaveRetreat(projectPath string) error {
	return l.write(RetreatPath(projectPath))
}

// write marshals the lock file to path, through fsutil.WriteFileAtomic's
// staging file and rename.
//
// The indirection is what makes a failed write harmless. A truncating write in
// place is fine for lnpm.lock, whose contents can be rebuilt, but the retreat
// snapshot shares this writer and cannot: a merging retreat writes the snapshot
// it just read straight back over itself, so a write that failed after the open
// had truncated would destroy the only record of what an earlier retreat
// unlinked - the very file the merge exists to protect.
//
// 0644 is the mode a first write creates. A lock file that already exists keeps
// whatever mode it has, and one marked read-only is refused rather than
// replaced; both are WriteFileAtomic's doing, and why are written down there.
func (l *LockFile) write(path string) error {
	data, err := yaml.Marshal(l)
	if err != nil {
		return fmt.Errorf("failed to marshal lock file: %w", err)
	}
	return fsutil.WriteFileAtomic(path, data, 0644)
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
