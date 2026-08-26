//go:build !windows

package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/fsutil"
	"github.com/pedrosousa13/lnpm/internal/pack"
)

// setUmask sets the process umask for the rest of the test and restores the
// previous value afterwards. The umask is process-global, so a test using this
// must not call t.Parallel() and must not run alongside one that does.
func setUmask(t *testing.T, mask int) {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}

// TestCopyFile_PreservesModeUnderRestrictiveUmask pins the store's copy path.
// It calls copyFile directly, so the reflink path cannot stand in for it. The
// mode argument to os.OpenFile is masked by the umask, so without an explicit
// chmod a 0755 bin script lands at 0700 under umask 0077 while the content hash
// and the database still record 0755.
func TestCopyFile_PreservesModeUnderRestrictiveUmask(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "cli.js")
	dst := filepath.Join(dir, "copied-cli.js")

	if err := os.WriteFile(src, []byte("#!/usr/bin/env node\n"), 0644); err != nil {
		t.Fatalf("Failed to write source: %v", err)
	}
	// chmod is not subject to the umask, so the fixture really is 0755.
	if err := os.Chmod(src, 0755); err != nil {
		t.Fatalf("Failed to chmod source: %v", err)
	}

	setUmask(t, 0077)

	if err := copyFile(src, dst, 0755); err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("Failed to stat copied file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0755 {
		t.Errorf("Copied file mode = %04o, want 0755 (the umask masked the mode instead of the copy setting it)", got)
	}
}

// TestStore_PreservesModeUnderRestrictiveUmask is the end-to-end acceptance
// check: a 0755 file stored under umask 0077 must still be executable in the
// store. Store tries a reflink clone before falling back to a copy, so it logs
// which one ran — a passing run only covers the path it actually took.
//
// The mode wanted is 0555 rather than the 0755 the hash and the database
// record, because Store takes the write bits off an entry's content on the way
// in (#333). The two failures this separates are still distinct: the umask
// deciding the mode lands the file at 0700 and then 0500, so the execute bit —
// which is the bit this test exists for — is missing either way it goes wrong.
func TestStore_PreservesModeUnderRestrictiveUmask(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", filepath.Join(tmpDir, "store"))

	s, err := New()
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	sourceDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(filepath.Join(sourceDir, "bin"), 0755); err != nil {
		t.Fatalf("Failed to create source dir: %v", err)
	}
	srcFile := filepath.Join(sourceDir, "bin", "cli.js")
	content := []byte("#!/usr/bin/env node\n")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatalf("Failed to write source: %v", err)
	}
	if err := os.Chmod(srcFile, 0755); err != nil {
		t.Fatalf("Failed to chmod source: %v", err)
	}

	// Record which path Store will take, so the test says what it covered.
	probe := filepath.Join(tmpDir, "reflink-probe")
	reflinkSupported := fsutil.Reflink(srcFile, probe) == nil
	if reflinkSupported {
		t.Log("filesystem supports reflink: this run covers the clone path, not copyFile")
	} else {
		t.Log("filesystem does not support reflink: this run covers the copyFile path")
	}

	files := []*pack.FileInfo{{
		RelPath:     "bin/cli.js",
		Path:        srcFile,
		Size:        int64(len(content)),
		Mode:        0755,
		ContentHash: "hash-cli",
	}}

	setUmask(t, 0077)

	destPath, err := s.Store("umask-pkg", "hash123", files, sourceDir)
	if err != nil {
		t.Fatalf("Failed to store package: %v", err)
	}

	info, err := os.Stat(filepath.Join(destPath, "bin", "cli.js"))
	if err != nil {
		t.Fatalf("Failed to stat stored file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0555 {
		t.Errorf("Stored file mode = %04o, want 0555 (the umask masked the mode instead of the store setting it, or the protection took more than the write bits)", got)
	}
}

// TestStripLifecycleScripts_PreservesManifestMode pins the permission bits of
// the store's package.json across the rewrite that removes prepare/prepublish.
// The file being rewritten is the store's own copy, which whichever path put it
// there - copyFile or the reflink clone - has already given the source's mode,
// so the rewrite has to read that mode back rather than name a number of its
// own. These rows call stripLifecycleScripts directly, so neither of those
// paths runs here: the fixture is chmodded to stand in for their result.
//
// Each row pins the umask rather than reading the ambient one, for the reason
// copyFile's own comment gives: a mode handed to os.WriteFile is masked, so a
// row can be satisfied by the mask instead of by the code. Under umask 0077 a
// hard-coded 0644 write lands at 0600 - exactly the first row's want - so an
// ambient 0077 would make that row pass without the fix. syscall.Umask is
// process-global, so these rows must not call t.Parallel().
//
// The rows are not equally strong, and the difference is worth stating rather
// than leaving a reader to assume three rows mean three guarantees. The fix has
// two halves - read the mode back instead of hard-coding 0644, and chmod past
// the umask - and only one row pins both:
//
//   - "a mode the umask would strip" (0640 under 0077) is the load-bearing row,
//     and the two halves redden it for different reasons. Hard-code the mode and
//     it lands at 0644 &^ 0077 = 0600, red because 0600 is not 0640 - the umask
//     is incidental there, 0644 alone already misses. Drop the explicit chmod
//     and the correct 0640 is masked to 0640 &^ 0077 = 0600, red because the
//     mask ate bits the code asked for. It is the only row that catches both.
//   - "a manifest kept private" (0600 under 0022) pins the hard-coded-0644 half
//     only. 0600 has no bits either umask strips, so dropping the chmod leaves
//     it green.
//   - "an ordinary manifest is unchanged" (0644 under 0022) discriminates
//     nothing - it is green against the unfixed code too. It is a regression
//     guard against an over-broad fix, not evidence for this one.
//
// TestPublishPreservesManifestModeThroughStoreAndConsumer in tests/ is a 0600
// fixture as well, so it shares the second row's blind spot: it pins the mode
// reaching the consumer, not the chmod.
func TestStripLifecycleScripts_PreservesManifestMode(t *testing.T) {
	cases := []struct {
		name  string
		mode  os.FileMode
		umask int
	}{
		{"a manifest kept private", 0600, 0022},
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

			setUmask(t, tc.umask)

			if err := stripLifecycleScripts(dir); err != nil {
				t.Fatalf("stripLifecycleScripts failed: %v", err)
			}

			// A row that never reached the rewrite would preserve the mode for
			// the wrong reason, so confirm the rewrite happened.
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("Failed to read manifest: %v", err)
			}
			if strings.Contains(string(data), "prepare") {
				t.Fatalf("prepare survived, so this row never exercised the rewrite: %s", data)
			}

			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("Failed to stat manifest: %v", err)
			}
			if got := info.Mode().Perm(); got != tc.mode {
				t.Errorf("Rewritten manifest mode = %04o, want %04o", got, tc.mode)
			}
		})
	}
}

// TestStripLifecycleScripts_LeavesAManifestWithoutLifecycleScriptsAlone covers
// the other half: a manifest with neither prepare nor prepublish returns before
// the rewrite, so its bytes and its mode must both come out untouched. The mode
// here is 0640 under umask 0077 so the assertion is not one the mask could
// satisfy on its own.
func TestStripLifecycleScripts_LeavesAManifestWithoutLifecycleScriptsAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	manifest := []byte(`{"name":"m","version":"1.0.0","scripts":{"build":"tsc"}}`)
	if err := os.WriteFile(path, manifest, 0644); err != nil {
		t.Fatalf("Failed to write manifest: %v", err)
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatalf("Failed to chmod manifest: %v", err)
	}

	setUmask(t, 0077)

	if err := stripLifecycleScripts(dir); err != nil {
		t.Fatalf("stripLifecycleScripts failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read manifest: %v", err)
	}
	if !bytes.Equal(data, manifest) {
		t.Errorf("Manifest was rewritten:\n got %s\nwant %s", data, manifest)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Failed to stat manifest: %v", err)
	}
	if got := info.Mode().Perm(); got != 0640 {
		t.Errorf("Manifest mode = %04o, want 0640", got)
	}
}
