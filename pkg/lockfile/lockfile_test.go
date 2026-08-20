package lockfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadNonExistent(t *testing.T) {
	tmpDir := t.TempDir()

	lock, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if lock == nil {
		t.Fatal("Load() returned nil")
	}
	if lock.Version != currentVersion {
		t.Errorf("Version = %d, want %d", lock.Version, currentVersion)
	}
	if lock.Packages == nil {
		t.Error("Packages is nil")
	}
	if len(lock.Packages) != 0 {
		t.Errorf("len(Packages) = %d, want 0", len(lock.Packages))
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()

	// Create lock file
	lock := &LockFile{
		Version:  currentVersion,
		Packages: make(map[string]Package),
	}

	now := time.Now().Truncate(time.Second)
	lock.Add("my-package", Package{
		Version:         "1.0.0",
		Hash:            "abc123def456",
		Source:          "/home/user/my-package",
		Linked:          now,
		OriginalVersion: "^1.0.0",
	})

	// Save
	if err := lock.Save(tmpDir); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Verify file exists
	lockPath := filepath.Join(tmpDir, lockFileName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}

	// Load
	loaded, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Verify content
	if loaded.Version != currentVersion {
		t.Errorf("Version = %d, want %d", loaded.Version, currentVersion)
	}

	pkg, ok := loaded.Get("my-package")
	if !ok {
		t.Fatal("my-package not found in loaded lock file")
	}

	if pkg.Version != "1.0.0" {
		t.Errorf("pkg.Version = %q, want %q", pkg.Version, "1.0.0")
	}
	if pkg.Hash != "abc123def456" {
		t.Errorf("pkg.Hash = %q, want %q", pkg.Hash, "abc123def456")
	}
	if pkg.Source != "/home/user/my-package" {
		t.Errorf("pkg.Source = %q, want %q", pkg.Source, "/home/user/my-package")
	}
	if pkg.OriginalVersion != "^1.0.0" {
		t.Errorf("pkg.OriginalVersion = %q, want %q", pkg.OriginalVersion, "^1.0.0")
	}
}

func TestLockFileOperations(t *testing.T) {
	lock := &LockFile{
		Version:  currentVersion,
		Packages: make(map[string]Package),
	}

	// Test Add
	lock.Add("pkg-a", Package{Version: "1.0.0", Hash: "hash-a"})
	lock.Add("pkg-b", Package{Version: "2.0.0", Hash: "hash-b"})

	// Test Has
	if !lock.Has("pkg-a") {
		t.Error("Has(pkg-a) = false, want true")
	}
	if !lock.Has("pkg-b") {
		t.Error("Has(pkg-b) = false, want true")
	}
	if lock.Has("pkg-c") {
		t.Error("Has(pkg-c) = true, want false")
	}

	// Test Get
	pkg, ok := lock.Get("pkg-a")
	if !ok {
		t.Error("Get(pkg-a) returned not ok")
	}
	if pkg.Version != "1.0.0" {
		t.Errorf("pkg-a Version = %q, want %q", pkg.Version, "1.0.0")
	}

	// Test List
	names := lock.List()
	if len(names) != 2 {
		t.Errorf("len(List()) = %d, want 2", len(names))
	}

	// Test Remove
	lock.Remove("pkg-a")
	if lock.Has("pkg-a") {
		t.Error("Has(pkg-a) = true after Remove, want false")
	}
	if !lock.Has("pkg-b") {
		t.Error("Has(pkg-b) = false after Remove of pkg-a, want true")
	}

	// Test List after remove
	names = lock.List()
	if len(names) != 1 {
		t.Errorf("len(List()) = %d after remove, want 1", len(names))
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()

	// Write invalid YAML
	lockPath := filepath.Join(tmpDir, lockFileName)
	if err := os.WriteFile(lockPath, []byte("invalid: yaml: content: ["), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(tmpDir)
	if err == nil {
		t.Error("Load() should return error for invalid YAML")
	}
}

func TestScopedPackages(t *testing.T) {
	lock := &LockFile{
		Version:  currentVersion,
		Packages: make(map[string]Package),
	}

	// Test scoped package names
	lock.Add("@org/my-package", Package{Version: "1.0.0", Hash: "hash-scoped"})
	lock.Add("@another-org/utils", Package{Version: "2.0.0", Hash: "hash-utils"})

	if !lock.Has("@org/my-package") {
		t.Error("Has(@org/my-package) = false, want true")
	}

	pkg, ok := lock.Get("@org/my-package")
	if !ok {
		t.Fatal("Get(@org/my-package) returned not ok")
	}
	if pkg.Version != "1.0.0" {
		t.Errorf("@org/my-package Version = %q, want %q", pkg.Version, "1.0.0")
	}

	// Save and reload to ensure scoped names survive serialization
	tmpDir := t.TempDir()
	if err := lock.Save(tmpDir); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if !loaded.Has("@org/my-package") {
		t.Error("Loaded lock file missing @org/my-package")
	}
	if !loaded.Has("@another-org/utils") {
		t.Error("Loaded lock file missing @another-org/utils")
	}
}

// TestPathAndRetreatPath pins the two file names the CLI has to agree on: the
// live lock file, and the snapshot `lnpm retreat` leaves for `lnpm restore`.
func TestPathAndRetreatPath(t *testing.T) {
	tmpDir := t.TempDir()

	if got, want := Path(tmpDir), filepath.Join(tmpDir, "lnpm.lock"); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
	if got, want := RetreatPath(tmpDir), filepath.Join(tmpDir, "lnpm.lock.retreat"); got != want {
		t.Errorf("RetreatPath() = %q, want %q", got, want)
	}
}

// TestLoadRetreatMissing checks that an absent snapshot is reported as "there is
// nothing here" rather than as an empty lock file, because the two mean
// different things to restore: no snapshot at all is a no-op, an empty one is a
// retreat that had nothing to record.
func TestLoadRetreatMissing(t *testing.T) {
	tmpDir := t.TempDir()

	lock, err := LoadRetreat(tmpDir)
	if err != nil {
		t.Fatalf("LoadRetreat() error: %v", err)
	}
	if lock != nil {
		t.Errorf("LoadRetreat() = %+v, want nil for a missing snapshot", lock)
	}
}

// TestLoadRetreatReadsSnapshot checks that a snapshot written next to the lock
// file parses with the same format as the lock file itself.
func TestLoadRetreatReadsSnapshot(t *testing.T) {
	tmpDir := t.TempDir()

	lock := &LockFile{Version: currentVersion, Packages: make(map[string]Package)}
	lock.Add("my-package", Package{Version: "1.2.3", Hash: "abc", OriginalVersion: "^1.0.0"})
	if err := lock.Save(tmpDir); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if err := os.Rename(Path(tmpDir), RetreatPath(tmpDir)); err != nil {
		t.Fatalf("Rename() error: %v", err)
	}

	got, err := LoadRetreat(tmpDir)
	if err != nil {
		t.Fatalf("LoadRetreat() error: %v", err)
	}
	if got == nil {
		t.Fatal("LoadRetreat() = nil, want the saved snapshot")
	}
	pkg, ok := got.Get("my-package")
	if !ok {
		t.Fatal("snapshot missing my-package")
	}
	if pkg.Version != "1.2.3" || pkg.OriginalVersion != "^1.0.0" {
		t.Errorf("my-package = %+v, want Version 1.2.3 and OriginalVersion ^1.0.0", pkg)
	}
}

// TestLoadRetreatCorrupt checks that an unreadable snapshot is an error, not a
// silent "nothing to restore".
func TestLoadRetreatCorrupt(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(RetreatPath(tmpDir), []byte("packages: {not valid yaml"), 0644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	if _, err := LoadRetreat(tmpDir); err == nil {
		t.Error("LoadRetreat() error = nil, want an error for a corrupt snapshot")
	}
}
