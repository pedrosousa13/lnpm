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

// TestSaveRetreatWritesTheSnapshotBesideTheLockFile covers the write half of the
// snapshot pair. A merging retreat cannot rename the lock file over an existing
// snapshot, so it reads the snapshot, adds the lock file's entries and saves it
// back through here; that has to land on the snapshot path and leave the lock
// file alone.
func TestSaveRetreatWritesTheSnapshotBesideTheLockFile(t *testing.T) {
	tmpDir := t.TempDir()

	lock := &LockFile{Version: currentVersion, Packages: make(map[string]Package)}
	lock.Add("saved-package", Package{Version: "2.0.0", Hash: "abc", OriginalVersion: "^2.0.0"})
	if err := lock.SaveRetreat(tmpDir); err != nil {
		t.Fatalf("SaveRetreat() error: %v", err)
	}

	if _, err := os.Stat(Path(tmpDir)); !os.IsNotExist(err) {
		t.Errorf("SaveRetreat() wrote a lock file too; os.Stat(%s) error = %v, want IsNotExist", Path(tmpDir), err)
	}

	got, err := LoadRetreat(tmpDir)
	if err != nil {
		t.Fatalf("LoadRetreat() error: %v", err)
	}
	if got == nil {
		t.Fatal("LoadRetreat() = nil, want the snapshot SaveRetreat just wrote")
	}
	pkg, ok := got.Get("saved-package")
	if !ok {
		t.Fatalf("snapshot missing saved-package, holds %v", got.List())
	}
	if pkg.Version != "2.0.0" || pkg.OriginalVersion != "^2.0.0" {
		t.Errorf("saved-package = %+v, want Version 2.0.0 and OriginalVersion ^2.0.0", pkg)
	}
}

// TestSaveRetreatReplacesTheSnapshotRatherThanTruncatingIt pins the one property
// that keeps a failed write from destroying an unconsumed snapshot: the file is
// replaced, not opened and truncated.
//
// A retreat that finds a snapshot already on disk merges into it and writes the
// result back over the same path. Written in place, the open truncates first, so
// a write that then failed would leave the record of what the earlier retreat
// unlinked empty, and it exists nowhere else. Written through a temp file and a
// rename, a failure cannot get that far.
//
// The truncating write is invisible once it succeeds, so what is asserted is the
// replacement itself: os.SameFile is false only because the snapshot on disk is
// a different file from the one that was there before.
func TestSaveRetreatReplacesTheSnapshotRatherThanTruncatingIt(t *testing.T) {
	tmpDir := t.TempDir()

	lock := &LockFile{Version: currentVersion, Packages: make(map[string]Package)}
	lock.Add("first-package", Package{Version: "1.0.0"})
	if err := lock.SaveRetreat(tmpDir); err != nil {
		t.Fatalf("SaveRetreat() error: %v", err)
	}
	before, err := os.Stat(RetreatPath(tmpDir))
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}

	lock.Add("second-package", Package{Version: "1.0.0"})
	if err := lock.SaveRetreat(tmpDir); err != nil {
		t.Fatalf("SaveRetreat() error: %v", err)
	}
	after, err := os.Stat(RetreatPath(tmpDir))
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}

	if os.SameFile(before, after) {
		t.Error("SaveRetreat() rewrote the snapshot in place; want a temp file renamed onto it, so a failed write cannot truncate the snapshot it is merging into")
	}
	if before.Mode().Perm() != after.Mode().Perm() {
		t.Errorf("snapshot mode = %v after the rewrite, want the %v it had before", after.Mode().Perm(), before.Mode().Perm())
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != RetreatFileName {
			t.Errorf("SaveRetreat() left %s behind, want only %s", entry.Name(), RetreatFileName)
		}
	}
}

// TestReadNormalisesAMissingVersion covers a snapshot or lock file with no
// "version:" key. It unmarshals as version 0, which is not a format anything
// ever wrote, and a merging retreat writes what it read straight back - so
// without normalising, the 0 would be persisted as if it meant something.
func TestReadNormalisesAMissingVersion(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(Path(tmpDir), []byte("packages:\n  my-package:\n    version: 1.0.0\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	lock, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if lock.Version != currentVersion {
		t.Errorf("Version = %d, want %d", lock.Version, currentVersion)
	}
}

// TestSaveRefusesAReadOnlyLockFile pins the one thing the rename gave away. A
// direct write opens the destination and so is refused by the file's own mode; a
// rename replaces the destination whatever its mode, and would quietly overwrite
// a lock file the user or a tool had marked read-only. The refusal is made
// explicitly instead, from the mode itself, so it holds wherever Chmod does.
func TestSaveRefusesAReadOnlyLockFile(t *testing.T) {
	tmpDir := t.TempDir()

	lock := &LockFile{Version: currentVersion, Packages: make(map[string]Package)}
	lock.Add("first-package", Package{Version: "1.0.0"})
	if err := lock.Save(tmpDir); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if err := os.Chmod(Path(tmpDir), 0444); err != nil {
		t.Fatalf("Chmod() error: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(Path(tmpDir), 0644) })

	lock.Add("second-package", Package{Version: "1.0.0"})
	if err := lock.Save(tmpDir); err == nil {
		t.Fatal("Save() error = nil, want a refusal to replace a read-only lock file")
	}

	got, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got.Has("second-package") {
		t.Errorf("the refused Save() landed anyway; lock file holds %v", got.List())
	}
}
