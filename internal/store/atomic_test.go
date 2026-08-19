package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/pack"
)

// makeSource writes n files into a fresh source dir and returns the dir and
// FileInfos referencing them.
func makeSource(t *testing.T, n int) (string, []*pack.FileInfo) {
	t.Helper()
	src := t.TempDir()
	var files []*pack.FileInfo
	for i := 0; i < n; i++ {
		rel := filepath.Join("dist", "f"+string(rune('a'+i))+".js")
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
		files = append(files, &pack.FileInfo{RelPath: filepath.ToSlash(rel), Path: p, Size: 7, Mode: 0644})
	}
	return src, files
}

// noTempLeftovers asserts the package's parent dir has no leftover temp dirs.
func noTempLeftovers(t *testing.T, s *Store, name string) {
	t.Helper()
	parent := filepath.Dir(s.PackagePath(name, "x"))
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("leftover temp dir in store: %s", e.Name())
		}
	}
}

func TestStoreAtomicCommit(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())
	s, err := New()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	src, files := makeSource(t, 3)
	dest, err := s.Store("atomic-pkg", "deadbeef", files, src)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	if !s.Exists("atomic-pkg", "deadbeef") {
		t.Fatal("package should exist after Store")
	}
	got, err := s.GetFiles("atomic-pkg", "deadbeef")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 stored files, got %d", len(got))
	}
	if filepath.Base(filepath.Dir(dest)) != "atomic-pkg" {
		t.Errorf("unexpected dest path %s", dest)
	}
	noTempLeftovers(t, s, "atomic-pkg")
}

// TestStoreFinalizeKeepsExistingEntry pins the finalize step against destroying
// a store entry another publisher already committed. finalize is exercised
// directly because Store's early Exists() short-circuit means a plain
// re-publish never reaches the rename with a populated destination.
func TestStoreFinalizeKeepsExistingEntry(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())
	s, err := New()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	// A complete entry for this hash, as committed by someone who got there first.
	finalPath := s.PackagePath("clobber-pkg", "feedface")
	if err := os.MkdirAll(finalPath, 0755); err != nil {
		t.Fatalf("seed final dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(finalPath, "index.js"), []byte("sentinel"), 0644); err != nil {
		t.Fatalf("seed final file: %v", err)
	}

	// Our own fully built temp dir, about to lose the race to rename into place.
	destPath, err := os.MkdirTemp(filepath.Dir(finalPath), ".feedface.tmp-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destPath, "index.js"), []byte("loser"), 0644); err != nil {
		t.Fatalf("seed temp file: %v", err)
	}

	renamed, err := s.finalize("clobber-pkg", "feedface", destPath, finalPath)
	if err != nil {
		t.Fatalf("finalize should succeed against an existing entry: %v", err)
	}
	if renamed {
		t.Error("finalize reported the temp dir as committed, but the destination was already occupied")
	}

	content, err := os.ReadFile(filepath.Join(finalPath, "index.js"))
	if err != nil {
		t.Fatalf("existing entry was destroyed: %v", err)
	}
	if string(content) != "sentinel" {
		t.Errorf("existing entry content is %q, want %q", content, "sentinel")
	}
}

// TestStoreConcurrentSameHash runs many concurrent stores of the same content
// and verifies exactly one complete package and no temp leftovers result.
func TestStoreConcurrentSameHash(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())
	s, err := New()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			src, files := makeSource(t, 4)
			if _, err := s.Store("race-pkg", "cafebabe", files, src); err != nil {
				t.Errorf("concurrent store: %v", err)
			}
		}()
	}
	wg.Wait()

	if !s.Exists("race-pkg", "cafebabe") {
		t.Fatal("package should exist after concurrent stores")
	}
	got, err := s.GetFiles("race-pkg", "cafebabe")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("expected 4 stored files, got %d (partial/corrupt store)", len(got))
	}
	noTempLeftovers(t, s, "race-pkg")
}
