package lockfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedrosousa13/lnpm/internal/fsutil"
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

// fileIdentity returns a FileInfo describing the file at path as it is now, so
// that os.SameFile still answers about that file after path has been replaced.
//
// os.Stat is not enough, because os.SameFile is not everywhere the eager
// comparison it looks like. On unix a FileInfo carries the inode from the moment
// it was taken. On Windows the fast path of os.stat calls GetFileAttributesEx,
// which returns no file index, so os.fileStat keeps the path instead
// (saveInfoFromPath) and os.SameFile resolves it to a volume and file index only
// when it is called (loadFileId, which opens the path afresh). A FileInfo
// captured before the snapshot was replaced would therefore describe the
// replacement, and the comparison below would report the same file whether the
// write was a rename or a truncation - which is the distinction it exists to
// make.
//
// Statting through an open file closes that gap on every platform: Windows takes
// that FileInfo from GetFileInformationByHandle, which fills the volume and file
// index in immediately and leaves nothing for os.SameFile to resolve later, and
// unix does an fstat on the same descriptor. Either way the identity is the one
// the file had when it was read.
func fileIdentity(t *testing.T, path string) os.FileInfo {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open(%s) error: %v", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat(%s) error: %v", path, err)
	}
	return info
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
	before := fileIdentity(t, RetreatPath(tmpDir))

	lock.Add("second-package", Package{Version: "1.0.0"})
	if err := lock.SaveRetreat(tmpDir); err != nil {
		t.Fatalf("SaveRetreat() error: %v", err)
	}
	after := fileIdentity(t, RetreatPath(tmpDir))

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
//
// It normalises to the oldest format rather than the current one, which only
// became a distinction in 4.0.0. Every file that omits the key was written
// before the key existed, so its hashes are 3.x hashes; calling such a file
// current would tell restore to resolve those hashes against a 4.x store and
// leave it with no way to say why nothing matched.
func TestReadNormalisesAMissingVersion(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(Path(tmpDir), []byte("packages:\n  my-package:\n    version: 1.0.0\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	lock, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if lock.Version != oldestVersion {
		t.Errorf("Version = %d, want %d", lock.Version, oldestVersion)
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

// writeOversizedLockFile creates a file at path whose first bytes are invalid
// YAML and whose length is one byte over the cap, by writing the bad bytes and
// extending the file with os.Truncate rather than writing megabytes out.
//
// Both properties matter, so both are read back rather than assumed. The length
// is what the cap sees. The invalid YAML is the discriminator for where the cap
// sits: checked before the unmarshal, the caller gets the size refusal; checked
// after it, yaml.Unmarshal reports a syntax error first and the size is never
// mentioned. Asserting which error comes back pins the placement without
// measuring how long anything takes.
func writeOversizedLockFile(t *testing.T, path string) int64 {
	t.Helper()

	const head = "packages: {not valid yaml"
	size := int64(fsutil.MaxYAMLBytes) + 1

	if err := os.WriteFile(path, []byte(head), 0644); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", path, err)
	}
	if err := os.Truncate(path, size); err != nil {
		t.Fatalf("Truncate(%s, %d) error: %v", path, size, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error: %v", path, err)
	}
	if info.Size() != size {
		t.Fatalf("built %s at %d bytes, want %d", path, info.Size(), size)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open(%s) error: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	got := make([]byte, len(head))
	if _, err := io.ReadFull(f, got); err != nil {
		t.Fatalf("reading back the head of %s: %v", path, err)
	}
	if string(got) != head {
		t.Fatalf("head of %s = %q, want the invalid YAML %q", path, got, head)
	}

	return size
}

// TestLoadRefusesAnOversizedLockFile covers the cap on the lock file. The error
// has to name the file, its size and the limit: a user who hits this decides
// whether the file is corrupt or the limit is wrong, and cannot do either
// without all three.
//
// The assertion that the message is not a parse error is what pins the cap
// *before* the unmarshal - see writeOversizedLockFile for why the fixture is
// invalid YAML as well as oversized.
func TestLoadRefusesAnOversizedLockFile(t *testing.T) {
	tmpDir := t.TempDir()
	size := writeOversizedLockFile(t, Path(tmpDir))

	lock, err := Load(tmpDir)
	if err == nil {
		t.Fatalf("Load() error = nil, want a refusal; got %+v", lock)
	}
	if !errors.Is(err, fsutil.ErrFileTooLarge) {
		t.Errorf("Load() error = %v, want it to wrap fsutil.ErrFileTooLarge", err)
	}

	msg := err.Error()
	for _, want := range []string{
		Path(tmpDir),
		strconv.FormatInt(size, 10),
		strconv.FormatInt(fsutil.MaxYAMLBytes, 10),
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Load() error = %q, want it to name %q", msg, want)
		}
	}
	if strings.Contains(msg, "failed to parse") {
		t.Errorf("Load() error = %q, want a size refusal; a parse error means the file was unmarshalled before the cap was checked", msg)
	}
}

// TestLoadRetreatRefusesAnOversizedSnapshot covers the other file that shares
// the reader. The snapshot is written by lnpm itself, so it is bounded for the
// same reason the lock file is: it is a file in the project that anything could
// have replaced.
func TestLoadRetreatRefusesAnOversizedSnapshot(t *testing.T) {
	tmpDir := t.TempDir()
	writeOversizedLockFile(t, RetreatPath(tmpDir))

	if _, err := LoadRetreat(tmpDir); !errors.Is(err, fsutil.ErrFileTooLarge) {
		t.Errorf("LoadRetreat() error = %v, want it to wrap fsutil.ErrFileTooLarge", err)
	}
}

// TestLoadParsesARealisticLockFile checks the cap is not in the way of anything
// real. lnpm.lock records the packages a project has linked rather than a
// resolved dependency graph, so a few thousand entries is already far past any
// project; the file it produces has to stay comfortably under the cap and parse
// as it always did.
//
// What it does not do is cover the refusal, and it should not be counted as if
// it did. It is not revert-sensitive in either direction: delete the cap and it
// stays green, move the check after the unmarshal and it stays green, because
// every file it builds is one the cap accepts. It is a floor under
// MaxYAMLBytes - it goes red if that constant is ever lowered past what a
// realistic lock file needs - and nothing more. The refusal is
// TestLoadRefusesAnOversizedLockFile's job.
//
// Measured on this branch, 3,000 entries writes about 675KB, roughly a sixth of
// the 4 MiB cap, so the headroom it proves is real but not large.
func TestLoadParsesARealisticLockFile(t *testing.T) {
	tmpDir := t.TempDir()

	const entries = 3000
	lock := &LockFile{Version: currentVersion, Packages: make(map[string]Package)}
	for i := 0; i < entries; i++ {
		lock.Add(fmt.Sprintf("@scope/package-%05d", i), Package{
			Version:         "1.2.3",
			Hash:            "0123456789abcdef0123456789abcdef01234567",
			Source:          fmt.Sprintf("/home/user/src/package-%05d", i),
			Linked:          time.Now().Truncate(time.Second),
			OriginalVersion: "^1.2.0",
		})
	}
	if err := lock.Save(tmpDir); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	info, err := os.Stat(Path(tmpDir))
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if info.Size() >= fsutil.MaxYAMLBytes {
		t.Fatalf("%d entries wrote %d bytes, at or over the %d-byte cap; the cap is too small for a realistic lock file", entries, info.Size(), int64(fsutil.MaxYAMLBytes))
	}

	got, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(got.Packages) != entries {
		t.Errorf("len(Packages) = %d, want %d", len(got.Packages), entries)
	}
}

// TestPinSurvivesASaveAndLoad pins the field the retreat snapshot carries. The
// database's link row is the authority on a pin, but this file is the only thing
// `lnpm restore` has to rebuild that row from, so a pin that did not survive the
// round trip would put a retreated project back following latest. See ADR-0006.
func TestPinSurvivesASaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()

	lock := &LockFile{Version: currentVersion, Packages: make(map[string]Package)}
	lock.Add("pinned-pkg", Package{Version: "1.0.0", Hash: "abc123", Pinned: true})
	lock.Add("following-pkg", Package{Version: "2.0.0", Hash: "def456"})
	if err := lock.Save(tmpDir); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	reloaded, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if entry, _ := reloaded.Get("pinned-pkg"); !entry.Pinned {
		t.Error("the pin did not survive the round trip")
	}
	if entry, _ := reloaded.Get("following-pkg"); entry.Pinned {
		t.Error("an entry that was never pinned came back pinned")
	}
}

// TestAnEntryWithNoPinKeyReadsAsUnpinned covers every lock file written before
// the field existed, which is all of them. The field is optional and its absence
// has to mean what those projects are: following a channel.
//
// The direction that does lose information is the reverse - an older lnpm
// re-saving a newer lock file drops a key it has no field for - which is why the
// link row rather than this file is what any command acts on.
func TestAnEntryWithNoPinKeyReadsAsUnpinned(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(Path(tmpDir), []byte("version: 1\npackages:\n  my-package:\n    version: 1.0.0\n    hash: abc123\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	lock, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if entry, _ := lock.Get("my-package"); entry.Pinned {
		t.Error("an entry with no pinned key read as pinned, so every lock file written before the field would freeze its packages")
	}
}

// TestAnUnpinnedEntryWritesNoPinKey keeps the field off files that do not need
// it. It is what makes the addition invisible to a project that never pins: the
// lock file it commits is byte-for-byte what it was.
func TestAnUnpinnedEntryWritesNoPinKey(t *testing.T) {
	tmpDir := t.TempDir()

	lock := &LockFile{Version: currentVersion, Packages: make(map[string]Package)}
	lock.Add("plain-pkg", Package{Version: "1.0.0", Hash: "abc123"})
	if err := lock.Save(tmpDir); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	data, err := os.ReadFile(Path(tmpDir))
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	if strings.Contains(string(data), "pinned") {
		t.Errorf("an unpinned entry wrote a pinned key:\n%s", data)
	}
}
