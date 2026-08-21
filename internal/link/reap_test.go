package link

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// seedDir creates dir with a single file in it, so a sweep that reclaims it has
// a non-zero size to report.
func seedDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("seed %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("payload"), 0644); err != nil {
		t.Fatalf("seed %s: %v", dir, err)
	}
}

func foundPaths(entries []TempEntry) []string {
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	sort.Strings(paths)
	return paths
}

// TestFindTempEntriesFindsAnInterruptedRelink covers the directory Link
// populates and renames into place: a process killed during population leaves
// it behind, and nothing has ever reclaimed it.
func TestFindTempEntriesFindsAnInterruptedRelink(t *testing.T) {
	project := t.TempDir()
	orphan := filepath.Join(project, ".lnpm", ".tmp-deadbeef")
	seedDir(t, orphan)

	entries, unreadable := FindTempEntries(project)
	if unreadable != 0 {
		t.Fatalf("FindTempEntries() could not read %d director(ies)", unreadable)
	}
	if len(entries) != 1 {
		t.Fatalf("FindTempEntries() found %v, want just %s", foundPaths(entries), orphan)
	}
	if entries[0].Path != orphan {
		t.Errorf("FindTempEntries() found %s, want %s", entries[0].Path, orphan)
	}
	if entries[0].Retired {
		t.Errorf("an in-progress temp directory was reported as a retired package")
	}
	if entries[0].Size == 0 {
		t.Errorf("FindTempEntries() reported size 0 for a directory holding a file")
	}
}

// TestFindTempEntriesFindsARetiredPackage covers the worse shape: a process
// killed between the two renames of the swap leaves the *previous* package
// behind under the retired name, so the directory holds a complete copy of real
// content rather than a half-written one.
func TestFindTempEntriesFindsARetiredPackage(t *testing.T) {
	project := t.TempDir()
	orphan := filepath.Join(project, ".lnpm", ".tmp-1a2b3c.old")
	seedDir(t, orphan)

	entries, unreadable := FindTempEntries(project)
	if unreadable != 0 {
		t.Fatalf("FindTempEntries() could not read %d director(ies)", unreadable)
	}
	if len(entries) != 1 {
		t.Fatalf("FindTempEntries() found %v, want just %s", foundPaths(entries), orphan)
	}
	if !entries[0].Retired {
		t.Errorf("a retired directory was not reported as holding a complete package")
	}
}

// TestFindTempEntriesFindsScopedTempDirs pins that the sweep reads more than the
// top level: a scoped package's temp directory is created inside the scope
// directory, so a one-level sweep misses it entirely.
func TestFindTempEntriesFindsScopedTempDirs(t *testing.T) {
	project := t.TempDir()
	inProgress := filepath.Join(project, ".lnpm", "@org", ".tmp-c0ffee")
	retired := filepath.Join(project, ".lnpm", "@org", ".tmp-c0ffee.old")
	seedDir(t, inProgress)
	seedDir(t, retired)

	entries, unreadable := FindTempEntries(project)
	if unreadable != 0 {
		t.Fatalf("FindTempEntries() could not read %d director(ies)", unreadable)
	}
	got := foundPaths(entries)
	want := []string{inProgress, retired}
	sort.Strings(want)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("FindTempEntries() found %v, want %v", got, want)
	}
}

// TestFindTempEntriesLeavesRealPackagesAlone is the test that matters most.
// ListLinked hides every dot-prefixed entry, so it is tempting to treat any
// dot-prefixed entry as reclaimable — but a package name may legitimately begin
// with a dot, and a scope directory must survive a temp directory inside it
// being reclaimed. A sweep that matches any dot-prefixed entry passes every
// other test in this file and destroys a real package here.
func TestFindTempEntriesLeavesRealPackagesAlone(t *testing.T) {
	project := t.TempDir()
	lnpm := filepath.Join(project, ".lnpm")

	keep := []string{
		filepath.Join(lnpm, "ordinary-pkg"),
		// A package whose name legitimately begins with a dot.
		filepath.Join(lnpm, ".hidden-pkg"),
		// Close to the temp shape but not it: no hex tail, and a name that only
		// looks like the retired form.
		filepath.Join(lnpm, ".tmp-notahexname"),
		filepath.Join(lnpm, ".tmpish"),
		filepath.Join(lnpm, "tmp-deadbeef"),
		filepath.Join(lnpm, "@org", "scoped-pkg"),
		filepath.Join(lnpm, "@org", ".hidden-scoped-pkg"),
	}
	for _, dir := range keep {
		seedDir(t, dir)
	}
	// One genuine orphan alongside them, so the sweep is doing work rather than
	// passing by finding nothing at all.
	orphan := filepath.Join(lnpm, "@org", ".tmp-99")
	seedDir(t, orphan)

	entries, unreadable := FindTempEntries(project)
	if unreadable != 0 {
		t.Fatalf("FindTempEntries() could not read %d director(ies)", unreadable)
	}
	if len(entries) != 1 || entries[0].Path != orphan {
		t.Fatalf("FindTempEntries() found %v, want just %s", foundPaths(entries), orphan)
	}

	// The scope directory itself must survive, and so must everything in it.
	for _, dir := range keep {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s no longer stats: %v", dir, err)
		}
	}
}

// TestFindTempEntriesWithoutLnpmDir pins that a project that has never linked
// anything is not an error: gc sweeps every project it knows about.
func TestFindTempEntriesWithoutLnpmDir(t *testing.T) {
	entries, unreadable := FindTempEntries(t.TempDir())
	if unreadable != 0 {
		t.Fatalf("FindTempEntries() could not read %d director(ies)", unreadable)
	}
	if len(entries) != 0 {
		t.Errorf("FindTempEntries() found %v in a project with no .lnpm directory", foundPaths(entries))
	}
}

// TestFindTempEntriesRefusesASymlinkedLnpmDirectory covers the sweep's half of
// the same hole the linker's guard closes. os.ReadDir follows a symlinked .lnpm,
// so a project whose checkout points .lnpm outside itself would have gc list -
// and then offer to delete - temp entries that belong to whatever it points at.
//
// The refusal is reported as one directory the sweep could not read, which is
// the shape this function already has for a directory it must not act on: gc
// prints the count, reclaims nothing there, and every other project still gets
// swept.
func TestFindTempEntriesRefusesASymlinkedLnpmDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	project := filepath.Join(tmpDir, "project")
	outside := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	// A temp entry the sweep would happily reclaim if it looked here at all.
	seedDir(t, filepath.Join(outside, ".tmp-deadbeef"))
	linkDirAt(t, outside, filepath.Join(project, ".lnpm"))

	entries, unreadable := FindTempEntries(project)
	if len(entries) != 0 {
		t.Errorf("FindTempEntries() found %v through a symlinked .lnpm, want nothing outside the project", foundPaths(entries))
	}
	if unreadable != 1 {
		t.Errorf("FindTempEntries() reported %d unreadable director(ies), want 1 for the .lnpm it refused", unreadable)
	}
}

// TestFindTempEntriesMatchesWhatTheConstructorsProduce ties the matcher to the
// names newTempDir actually creates, so a change to one without the other is
// caught here rather than by a leak nobody sees.
func TestFindTempEntriesMatchesWhatTheConstructorsProduce(t *testing.T) {
	project := t.TempDir()
	lnpm := filepath.Join(project, ".lnpm")
	if err := os.MkdirAll(lnpm, 0755); err != nil {
		t.Fatalf("create .lnpm: %v", err)
	}

	made, err := newTempDir(lnpm)
	if err != nil {
		t.Fatalf("newTempDir() error = %v", err)
	}
	// The retired name the swap in Link derives from it.
	retired := made + retiredSuffix
	if err := os.Mkdir(retired, 0755); err != nil {
		t.Fatalf("create retired dir: %v", err)
	}

	entries, unreadable := FindTempEntries(project)
	if unreadable != 0 {
		t.Fatalf("FindTempEntries() could not read %d director(ies)", unreadable)
	}
	got := foundPaths(entries)
	want := []string{made, retired}
	sort.Strings(want)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("FindTempEntries() found %v, want %v", got, want)
	}
}

// TestFindTempEntriesFindsAnOrphanedTempLink covers LinkSource's leftover, which
// is a link rather than a directory and so is missed by a sweep that only looks
// at directories.
func TestFindTempEntriesFindsAnOrphanedTempLink(t *testing.T) {
	project := t.TempDir()
	lnpm := filepath.Join(project, ".lnpm")
	if err := os.MkdirAll(lnpm, 0755); err != nil {
		t.Fatalf("create .lnpm: %v", err)
	}
	target := t.TempDir()
	orphan, err := newTempLink(lnpm, target)
	if err != nil {
		t.Fatalf("newTempLink() error = %v", err)
	}

	entries, unreadable := FindTempEntries(project)
	if unreadable != 0 {
		t.Fatalf("FindTempEntries() could not read %d director(ies)", unreadable)
	}
	if len(entries) != 1 || entries[0].Path != orphan {
		t.Fatalf("FindTempEntries() found %v, want just %s", foundPaths(entries), orphan)
	}
	// Sizing must not follow the link: the target is not what is being freed.
	if entries[0].Size != 0 {
		t.Errorf("FindTempEntries() sized a link at %d bytes, want 0", entries[0].Size)
	}
	if !entries[0].Link {
		t.Errorf("a leftover from LinkSource was not reported as a link")
	}
}

// TestFindTempEntriesDistinguishesADirectoryFromALink pins the difference gc
// reports on. A retired directory holds a complete copy of the previous package;
// a retired link holds a link and no copy of anything, so describing it the same
// way would state something plainly untrue about the only place these entries
// are ever mentioned.
func TestFindTempEntriesDistinguishesADirectoryFromALink(t *testing.T) {
	project := t.TempDir()
	lnpm := filepath.Join(project, ".lnpm")
	if err := os.MkdirAll(lnpm, 0755); err != nil {
		t.Fatalf("create .lnpm: %v", err)
	}
	retiredDir := filepath.Join(lnpm, ".tmp-aa11.old")
	seedDir(t, retiredDir)

	retiredLink, err := newTempLink(lnpm, t.TempDir())
	if err != nil {
		t.Fatalf("newTempLink() error = %v", err)
	}
	if err := os.Rename(retiredLink, retiredLink+retiredSuffix); err != nil {
		t.Fatalf("retire the link: %v", err)
	}
	retiredLink += retiredSuffix

	entries, unreadable := FindTempEntries(project)
	if unreadable != 0 {
		t.Fatalf("FindTempEntries() could not read %d director(ies)", unreadable)
	}
	byPath := map[string]TempEntry{}
	for _, e := range entries {
		byPath[e.Path] = e
	}
	if len(byPath) != 2 {
		t.Fatalf("FindTempEntries() found %v, want both retired entries", foundPaths(entries))
	}
	if got := byPath[retiredDir]; !got.Retired || got.Link {
		t.Errorf("retired directory reported as retired=%v link=%v, want retired=true link=false", got.Retired, got.Link)
	}
	if got := byPath[retiredLink]; !got.Retired || !got.Link {
		t.Errorf("retired link reported as retired=%v link=%v, want retired=true link=true", got.Retired, got.Link)
	}
}
