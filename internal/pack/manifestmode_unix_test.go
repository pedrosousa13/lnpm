//go:build !windows

package pack

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// setUmask sets the process umask for the rest of the test and restores the
// previous value afterwards. The umask is process-global, so a test using this
// must not call t.Parallel() and must not run alongside one that does.
func setUmask(t *testing.T, mask int) {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}

// TestPrepareManifestPreservesManifestMode pins the permission bits of the
// manifest PrepareManifest materialises, which is the file the store then
// copies and the content hash then covers.
//
// This guarantee used to sit on store.stripLifecycleScripts, which rewrote the
// stored package.json in place; #447 moved the strip in front of the hash and
// the store's rewrite went with it. The mode still has to be carried
// deliberately rather than left to the umask: a reflinked store copy inherits
// the permissions of the file it clones, and HashFiles folds Mode.Perm() in, so
// a manifest that lands at the wrong mode is a different package hash.
//
// Each row pins the umask rather than reading the ambient one, because a mode
// handed to os.CreateTemp is masked and a row can otherwise be satisfied by the
// mask instead of by the code. Under umask 0077 an unchmodded 0644 write lands
// at 0600 - exactly the first row's want - so an ambient 0077 would make that
// row pass without the fix.
func TestPrepareManifestPreservesManifestMode(t *testing.T) {
	cases := []struct {
		name  string
		mode  os.FileMode
		umask int
	}{
		{"a mode the umask would strip", 0640, 0077},
		{"an ordinary manifest is unchanged", 0644, 0022},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "package.json")
			manifest := []byte(`{"name":"m","version":"1.0.0","scripts":{"prepare":"husky install","build":"tsc"}}`)
			if err := os.WriteFile(path, manifest, 0644); err != nil {
				t.Fatalf("Failed to write manifest: %v", err)
			}
			// chmod is not masked by the umask, so the fixture really is tc.mode.
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatalf("Failed to chmod manifest: %v", err)
			}

			entry := &FileInfo{Path: path, RelPath: "package.json", Mode: tc.mode}
			setUmask(t, tc.umask)

			cleanup, err := PrepareManifest(dir, []*FileInfo{entry})
			defer cleanup()
			if err != nil {
				t.Fatalf("PrepareManifest failed: %v", err)
			}

			// A row that never reached the rewrite would preserve the mode for
			// the wrong reason, so confirm the strip happened and that the entry
			// was repointed at what it produced.
			if entry.Path == path {
				t.Fatalf("the entry still points at the source manifest, so this row never exercised the rewrite")
			}
			data, err := os.ReadFile(entry.Path)
			if err != nil {
				t.Fatalf("Failed to read the materialised manifest: %v", err)
			}
			if strings.Contains(string(data), "prepare") {
				t.Fatalf("prepare survived, so this row never exercised the rewrite: %s", data)
			}

			info, err := os.Stat(entry.Path)
			if err != nil {
				t.Fatalf("Failed to stat the materialised manifest: %v", err)
			}
			if got := info.Mode().Perm(); got != tc.mode {
				t.Errorf("Materialised manifest mode = %04o, want %04o", got, tc.mode)
			}
		})
	}
}

// TestPrepareManifestLeavesAManifestWithoutLifecycleScriptsAlone covers the
// other half: with no workspace specifier to resolve and neither prepare nor
// prepublish to remove, nothing is materialised at all and the entry keeps
// pointing at the developer's own file.
func TestPrepareManifestLeavesAManifestWithoutLifecycleScriptsAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	manifest := []byte(`{"name":"m","version":"1.0.0","scripts":{"build":"tsc"}}`)
	if err := os.WriteFile(path, manifest, 0644); err != nil {
		t.Fatalf("Failed to write manifest: %v", err)
	}

	entry := &FileInfo{Path: path, RelPath: "package.json", Mode: 0644}
	cleanup, err := PrepareManifest(dir, []*FileInfo{entry})
	defer cleanup()
	if err != nil {
		t.Fatalf("PrepareManifest failed: %v", err)
	}

	if entry.Path != path {
		t.Errorf("the entry was repointed at %s, but there was nothing to rewrite", entry.Path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read manifest: %v", err)
	}
	if string(data) != string(manifest) {
		t.Errorf("the source manifest was rewritten:\n got %s\nwant %s", data, manifest)
	}
}
