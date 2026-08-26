package link

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/pack"
)

// benchPackage writes a store entry of n files of size bytes each and returns a
// project path to link it into, the store path, and the file set.
//
// The default shape is 2000 files of 5KB - 10MB in total - which is the size the
// numbers quoted in verifiedReusable's comment were taken at. tests/bench_test.go
// benchmarks a whole RunPush over 2000 much smaller files; these are here rather
// than there because what they measure is one function of this package, and
// because the bytes are the point: verification's cost tracks the size of the
// reuse set, which a package of 140-byte files does not exercise.
func benchPackage(b *testing.B, n, size int) (projectPath, storePath string, files []*pack.FileInfo) {
	b.Helper()

	tmpDir := b.TempDir()
	projectPath = filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		b.Fatal(err)
	}
	storePath = filepath.Join(tmpDir, "store", "my-package")

	blob := make([]byte, size)
	for i := 0; i < n; i++ {
		// Spread over subdirectories so the walk and the per-file MkdirAll meet
		// a tree rather than one flat directory of 2000 entries.
		relPath := fmt.Sprintf("dist/%03d/f%05d.js", i%50, i)
		path := filepath.Join(storePath, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			b.Fatal(err)
		}
		// Distinct content per file, so no two files share a hash and the store
		// is not accidentally deduplicating what the linker is asked to move.
		for j := range blob {
			blob[j] = byte(i + j)
		}
		if err := os.WriteFile(path, blob, 0644); err != nil {
			b.Fatal(err)
		}
		hash, err := pack.HashFile(path)
		if err != nil {
			b.Fatal(err)
		}
		files = append(files, &pack.FileInfo{
			Path:        path,
			RelPath:     relPath,
			Size:        int64(size),
			Mode:        0644,
			ContentHash: hash,
		})
	}
	return projectPath, storePath, files
}

// BenchmarkVerifiedReusable is #332's cost on its own: one pass reading back
// every file a relink was about to leave alone.
//
// It is measured apart from Link because that is the number the design argument
// turns on, and the relink benchmarks below bury it under a temp directory, a
// rename and a tree delete. Everything it reads is in the page cache by the time
// it runs, here and in most real runs, and the comment on verifiedReusable says
// what changes when it is not.
func BenchmarkVerifiedReusable(b *testing.B) {
	projectPath, storePath, files := benchPackage(b, 2000, 5*1024)
	if _, err := New(projectPath).Link("my-package", storePath, files); err != nil {
		b.Fatal(err)
	}

	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	present, _ := scanLinked(lnpmPath)
	candidates := reusableFiles(readManifest(lnpmPath), present, files)
	if len(candidates) != len(files) {
		b.Fatalf("expected every file to be a reuse candidate, got %d of %d", len(candidates), len(files))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := verifiedReusable(lnpmPath, candidates, files); len(got) != len(files) {
			b.Fatalf("verified %d of %d files", len(got), len(files))
		}
	}
}

// BenchmarkRelinkAllReusable relinks a package nothing has changed in, which is
// the case Link's up-to-date shortcut skips the swap for. Verification is the
// whole of its cost: no file is written and no directory is renamed.
func BenchmarkRelinkAllReusable(b *testing.B) {
	projectPath, storePath, files := benchPackage(b, 2000, 5*1024)
	linker := New(projectPath)
	if _, err := linker.Link("my-package", storePath, files); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := linker.Link("my-package", storePath, files)
		if err != nil {
			b.Fatal(err)
		}
		if res.Changed != 0 {
			b.Fatalf("relink of an unchanged package materialised %d files", res.Changed)
		}
	}
}

// BenchmarkRelinkOneChanged is the case #295 exists for, and the one where
// verification is pure addition: the swap happens either way, and every file but
// one is read back only to be carried over.
func BenchmarkRelinkOneChanged(b *testing.B) {
	projectPath, storePath, files := benchPackage(b, 2000, 5*1024)
	linker := New(projectPath)
	if _, err := linker.Link("my-package", storePath, files); err != nil {
		b.Fatal(err)
	}

	// Two file sets differing in one file, alternated, so every iteration is a
	// one-file change rather than the first being a change and the rest no-ops.
	edited := make([]*pack.FileInfo, len(files))
	copy(edited, files)
	other := *files[0]
	other.ContentHash = "0000000000000000"
	edited[0] = &other

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		set := files
		if i%2 == 1 {
			set = edited
		}
		if _, err := linker.Link("my-package", storePath, set); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRelinkNothingReusable is what declining to reuse costs instead: the
// full materialisation out of the store that verification is traded against. An
// empty ContentHash is never reusable, which is how a caller that does not know
// its hashes already reaches this path.
func BenchmarkRelinkNothingReusable(b *testing.B) {
	projectPath, storePath, files := benchPackage(b, 2000, 5*1024)
	linker := New(projectPath)
	if _, err := linker.Link("my-package", storePath, files); err != nil {
		b.Fatal(err)
	}

	unhashed := make([]*pack.FileInfo, len(files))
	for i, f := range files {
		c := *f
		c.ContentHash = ""
		unhashed[i] = &c
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := linker.Link("my-package", storePath, unhashed); err != nil {
			b.Fatal(err)
		}
	}
}
