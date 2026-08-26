package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/fsutil"
	"github.com/pedrosousa13/lnpm/internal/pack"
)

// tempInfix separates a temp directory's hash from the random tail os.MkdirTemp
// appends, and newTempDir below is the only place a temp directory name is
// built. The sweep in reap.go matches against the same constant and recognises
// what this function creates by calling it, so the write path and the sweep
// cannot drift into disagreeing about the name.
const tempInfix = ".tmp-"

// newTempDir creates the directory the write path populates for hash, as a
// sibling of the entry it will be renamed to.
func newTempDir(parent, hash string) (string, error) {
	return os.MkdirTemp(parent, "."+hash+tempInfix)
}

// shortHash returns the first 8 characters of a hash for display
func shortHash(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

// Store manages the package store at ~/.lnpm/store
type Store struct {
	basePath string
}

// New creates a new Store instance
func New() (*Store, error) {
	basePath, err := getStorePath()
	if err != nil {
		return nil, err
	}

	storePath := filepath.Join(basePath, "store")
	if err := os.MkdirAll(storePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	// A store written before completeness markers existed carries none, and
	// every entry in it would otherwise be refused on read. It is marked once,
	// here, under a gate that leaves 2.x stores alone.
	if err := backfillLegacyStore(storePath); err != nil {
		return nil, err
	}

	// A store written before entries were protected on the way in holds writable
	// content, and the consumer hard links into it are writable with it. It is
	// protected once, here, after the backfill so the markers that pass writes
	// exist to be left out of the protection.
	protectExistingEntries(storePath)

	return &Store{basePath: storePath}, nil
}

// getStorePath returns the lnpm store root path
func getStorePath() (string, error) {
	return config.GetStorePath()
}

// Root returns the store's base directory (…/store).
func (s *Store) Root() string {
	return s.basePath
}

// PackagePath returns the path to a package in the store
func (s *Store) PackagePath(name, hash string) string {
	return filepath.Join(s.basePath, name, hash)
}

// CheckComplete reports whether a complete package with the given hash is in
// the store. A directory alone is not enough: a deletion interrupted partway
// leaves one behind, and linking it into a consumer would install a truncated
// package.
//
// It returns an error rather than a bool deliberately. The bool it replaces was
// the shape of #330: every write path called it, no read path did, and nothing
// about `if s.Exists(...)` announced that the answer had to be acted on. A
// caller now has to handle the error, and gets one that names the entry and
// tells the user how to rebuild it, which is what a read path has to print.
func (s *Store) CheckComplete(name, hash string) error {
	return checkEntry(s.PackagePath(name, hash), name)
}

// Store populates the store with the package's files, using reflink (CoW clone)
// where the filesystem supports it and a plain copy otherwise. It never hard
// links a source file into the store: the store entry must be a private copy so
// later writes to the source cannot mutate content already addressed by hash.
func (s *Store) Store(name, hash string, files []*pack.FileInfo, sourceDir string) (string, error) {
	// Guard against path traversal: name is joined into the store path.
	if err := pack.ValidatePackageName(name); err != nil {
		return "", err
	}

	finalPath := s.PackagePath(name, hash)
	debug.Logf("store: storing %s hash=%s files=%d dest=%s", name, shortHash(hash), len(files), finalPath)

	// Same hash = same content, skip if already exists (race-safe).
	//
	// That equation is a consistency control and not tamper evidence, and this
	// is the line it is made at: the hash is a 64-bit xxhash, so two different
	// publishes of one name can be made to meet here — a different name cannot,
	// since finalPath is keyed by both. ADR-0007 records why that is accepted
	// and what it costs — in short, this return keeps the bytes already stored,
	// so a collision serves stale content rather than admitting chosen content,
	// and what actually resists tampering is the write protection below (#333).
	if s.CheckComplete(name, hash) == nil {
		debug.Logf("store: package already exists at %s, skipping", finalPath)
		return finalPath, nil
	}

	// Write into a temp dir first, then atomically rename into place. This way
	// an interrupted store never leaves a partial package at finalPath: the
	// entry appears in one step, already carrying its completeness marker.
	parent := filepath.Dir(finalPath)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return "", fmt.Errorf("failed to create store directory: %w", err)
	}
	destPath, err := newTempDir(parent, hash)
	if err != nil {
		return "", fmt.Errorf("failed to create temp store directory: %w", err)
	}
	// Remove the temp dir unless it is successfully committed via rename.
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(destPath)
		}
	}()

	// Process files in parallel: try reflink first, collect failures for copy
	total := len(files)
	var reflinkCount, copyCount int32
	var filesToCopyMu sync.Mutex
	var filesToCopy []*pack.FileInfo

	// Parallel pass: try the fast method (reflink)
	numWorkers := min(runtime.NumCPU(), 8)
	if len(files) < numWorkers {
		numWorkers = len(files)
	}

	var wg sync.WaitGroup
	fileChan := make(chan *pack.FileInfo, len(files))
	errChan := make(chan error, 1)
	var processed int32

	// Start workers for parallel linking
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range fileChan {
				destFile := filepath.Join(destPath, f.RelPath)

				// Create parent directories
				if err := os.MkdirAll(filepath.Dir(destFile), 0755); err != nil {
					select {
					case errChan <- fmt.Errorf("failed to create directory for %s: %w", f.RelPath, err):
					default:
					}
					return
				}

				linked := false

				// 1. Try reflink (CoW clone) - instant on APFS/Btrfs/XFS
				if fsutil.Reflink(f.Path, destFile) == nil {
					linked = true
					atomic.AddInt32(&reflinkCount, 1)
				}

				// 2. Queue for parallel copy if reflink didn't work
				if !linked {
					filesToCopyMu.Lock()
					filesToCopy = append(filesToCopy, f)
					filesToCopyMu.Unlock()
				}

				current := atomic.AddInt32(&processed, 1)
				if total >= 1000 && current%1000 == 0 {
					fmt.Printf("\r  Processing... %d/%d files", current, total)
				}
			}
		}()
	}

	// Queue all files
	for _, f := range files {
		fileChan <- f
	}
	close(fileChan)

	// Wait for linking phase to complete
	wg.Wait()

	// Check for errors
	select {
	case err := <-errChan:
		return "", err
	default:
	}

	// Parallel copy for files that couldn't be reflinked
	if len(filesToCopy) > 0 {
		debug.Logf("store: copying %d files in parallel", len(filesToCopy))

		// Reuse worker pool for parallel copying
		numCopyWorkers := min(runtime.NumCPU(), 8)
		if len(filesToCopy) < numCopyWorkers {
			numCopyWorkers = len(filesToCopy)
		}

		var wg2 sync.WaitGroup
		copyChan := make(chan *pack.FileInfo, len(filesToCopy))
		errChan2 := make(chan error, 1)
		var copyProcessed int32

		// Start copy workers
		for w := 0; w < numCopyWorkers; w++ {
			wg2.Add(1)
			go func() {
				defer wg2.Done()
				for f := range copyChan {
					destFile := filepath.Join(destPath, f.RelPath)
					if err := copyFile(f.Path, destFile, f.Mode); err != nil {
						select {
						case errChan2 <- fmt.Errorf("failed to copy %s: %w", f.RelPath, err):
						default:
						}
						return
					}
					atomic.AddInt32(&copyCount, 1)

					current := atomic.AddInt32(&copyProcessed, 1)
					if total >= 1000 && current%1000 == 0 {
						fmt.Printf("\r  Copying... %d/%d files", current, len(filesToCopy))
					}
				}
			}()
		}

		// Queue copy files
		for _, f := range filesToCopy {
			copyChan <- f
		}
		close(copyChan)

		// Wait for copy completion
		wg2.Wait()

		// Check for copy errors
		select {
		case err := <-errChan2:
			return "", err
		default:
		}
	}

	if total >= 1000 {
		fmt.Printf("\r                                        \r") // Clear progress line
	}

	debug.Logf("store: reflinked %d, copied %d files", reflinkCount, copyCount)

	// Strip lifecycle scripts from package.json to prevent npm from running them
	// when installed as file: dependency (matches yalc behavior)
	if err := stripLifecycleScripts(destPath); err != nil {
		return "", fmt.Errorf("failed to strip lifecycle scripts: %w", err)
	}

	// Take the write bits off the content before it is committed, so the entry
	// is never observable at a mode a consumer could write through. writeBits'
	// comment carries the reasoning; what matters to the order here is that this
	// runs after stripLifecycleScripts, which rewrites package.json by renaming
	// a temp file onto it, and before writeMarker, whose file the protection
	// leaves out and which is easier to keep out by not having written it yet.
	if err := protectTree(destPath); err != nil {
		return "", fmt.Errorf("failed to write protect store content: %w", err)
	}

	// Mark the entry complete as the last file written inside the temp dir, so
	// it commits together with the content in the rename below. Written after
	// the rename it would leave a window in which a committed entry is unmarked.
	if err := writeMarker(destPath, hash); err != nil {
		return "", err
	}

	// Atomically move the completed package into place.
	renamed, err := s.finalize(name, hash, destPath, finalPath)
	if err != nil {
		return "", err
	}
	committed = renamed

	return finalPath, nil
}

// finalize atomically renames the fully built temp directory destPath onto
// finalPath. It reports whether the rename consumed destPath, so the caller
// knows whether the deferred temp-dir cleanup still has work to do.
//
// It never deletes finalPath. finalPath is keyed by the content hash, so a
// marked entry at the destination holds this exact content, committed by
// another goroutine or process.
func (s *Store) finalize(name, hash, destPath, finalPath string) (bool, error) {
	if err := os.Rename(destPath, finalPath); err != nil {
		// A concurrent publish of the same hash may have already created it.
		// CheckComplete(name, hash) rather than a bare stat of finalPath, which
		// is the weaker question of the two: a destination that exists without a
		// marker is a partial entry, and blessing a failed rename onto one as
		// success is precisely what must not happen here.
		if s.CheckComplete(name, hash) == nil {
			return false, nil // temp dir cleaned up by deferred guard
		}
		// A destination that exists but is not a complete entry: the gutted
		// directory of #330. It is not deleted here — lnpm cannot tell what is
		// in it, and the write path is not where that gets decided — so the
		// rename cannot be retried and the user has to remove it. Saying so is
		// the difference between actionable and "rename ...: file exists",
		// which is what push prints when it hits this.
		if _, statErr := os.Stat(finalPath); statErr == nil {
			return false, fmt.Errorf("cannot store %s: %s is in the way and is not a complete store entry; delete it and publish again", name, finalPath)
		}
		return false, fmt.Errorf("failed to finalize store package: %w", err)
	}
	return true, nil
}

// GetFiles returns all files in a stored package.
//
// The completeness check is here, at the primitive, rather than at each of the
// commands that read the store. What this returns is what gets materialised
// into a consumer project, so a caller that skipped the check would link a
// gutted entry — which is exactly what add, pull and publish's relink did
// before #330, each of them having simply never asked. Walking a damaged entry
// succeeds and returns the files that survived, so there is no failure further
// down to fall back on.
func (s *Store) GetFiles(name, hash string) ([]*pack.FileInfo, error) {
	if err := s.CheckComplete(name, hash); err != nil {
		return nil, err
	}

	return EntryFiles(s.PackagePath(name, hash))
}

// EntryFiles lists the files an entry holds, in the order the walk finds them.
//
// This is the definition of "what is in a store entry", and it is one function
// rather than one per caller on purpose. What it returns is exactly what gets
// copied into a consumer project, so anything that wants to reason about the
// content of an entry — GetFiles above, and doctor's integrity check, which has
// to know which files a row set is allowed not to mention — has to be looking at
// the same set. A second walk with its own idea of what to leave out is a gap
// between what lnpm serves and what lnpm checks.
//
// Unlike GetFiles this asks nothing about completeness. doctor calls it for
// entries whose marker it has already read, and reporting the same fault twice
// under two headings helps nobody.
//
// ContentHash is left empty: the walk records what is on disk, and nothing here
// opens a file. Callers that need hashes take them from the database or compute
// them themselves.
func EntryFiles(entryPath string) ([]*pack.FileInfo, error) {
	var files []*pack.FileInfo
	err := filepath.Walk(entryPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(entryPath, path)
		if err != nil {
			return err
		}

		// The completeness marker belongs to the store, not to the package:
		// what this returns is copied into consumer projects.
		if filepath.ToSlash(relPath) == markerName {
			return nil
		}

		files = append(files, &pack.FileInfo{
			Path:    path,
			RelPath: filepath.ToSlash(relPath),
			Size:    info.Size(),
			Mode:    info.Mode(),
		})

		return nil
	})

	return files, err
}

// copyFile copies a file preserving permissions
func copyFile(src, dst string, mode os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = srcFile.Close() }()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() { _ = dstFile.Close() }()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	// OpenFile's mode argument is masked by the process umask, so the stored
	// file would not carry the exact permission bits the content hash was
	// computed from (pack folds Mode.Perm() into the hash). Set them explicitly.
	if err := dstFile.Chmod(mode); err != nil {
		return err
	}

	return dstFile.Sync()
}

// strippedScripts are the lifecycle scripts stripLifecycleScripts removes from a
// stored manifest. Named once so that ManifestStrippedInStore, which has to
// recognise this function's output, cannot come to disagree with it about which
// scripts that is.
var strippedScripts = []string{"prepare", "prepublish"}

// marshalManifest renders a parsed package.json the way a stored one is written.
//
// Extracted for the same reason as strippedScripts: recognising the rewrite
// after the fact means reproducing it byte for byte, and two copies of
// "MarshalIndent with two spaces and a trailing newline" would eventually stop
// being the same thing.
func marshalManifest(manifest map[string]interface{}) ([]byte, error) {
	output, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(output, '\n'), nil
}

// ManifestStrippedInStore reports whether data could be a manifest that
// stripLifecycleScripts wrote.
//
// It exists because that rewrite happens after publish has hashed the packed
// files, so a stored package.json legitimately fails to match the content hash
// recorded for it — but only for a package that had one of those scripts.
// doctor cannot ask the store which packages those were, so it asks this
// instead: is what is on disk in the shape the rewrite produces?
//
// Three things have to hold, and each of them is a branch stripLifecycleScripts
// returns early from. The document must parse. It must carry a scripts map, or
// the rewrite returned without writing. And neither stripped script may remain
// in it, because removing them is the only reason it writes at all. Then the
// bytes must equal their own re-marshalled form, which is what the rewrite
// emits and what a hand-edited manifest almost never is.
//
// That last test is what keeps this narrow, and it is worth being plain about
// its limit: a tampered manifest that happens to be canonical JSON with a
// scripts map and neither script in it is indistinguishable from a stripped
// one. What this rules out is the ordinary edit, not a forgery built to match.
func ManifestStrippedInStore(data []byte) bool {
	var manifest map[string]interface{}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false
	}

	scripts, ok := manifest["scripts"].(map[string]interface{})
	if !ok || scripts == nil {
		return false
	}
	for _, script := range strippedScripts {
		if _, exists := scripts[script]; exists {
			return false
		}
	}

	output, err := marshalManifest(manifest)
	if err != nil {
		return false
	}
	return bytes.Equal(data, output)
}

// stripLifecycleScripts removes prepare/prepublish scripts from package.json
// This prevents npm from running these scripts when the package is installed as a file: dependency
// Matches yalc behavior: https://github.com/wclr/yalc
func stripLifecycleScripts(destPath string) error {
	pkgJSONPath := filepath.Join(destPath, "package.json")

	// Read package.json
	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No package.json (shouldn't happen but be defensive)
			return nil
		}
		return fmt.Errorf("failed to read package.json: %w", err)
	}

	// Handle empty file (race condition with concurrent publish)
	if len(data) == 0 {
		debug.Log("store: package.json is empty, skipping script stripping")
		return nil
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		// Could be a race condition with concurrent writes, skip gracefully
		debug.Logf("store: failed to parse package.json, skipping script stripping: %v", err)
		return nil
	}

	// Check if scripts field exists
	scripts, ok := pkgJSON["scripts"].(map[string]interface{})
	if !ok || scripts == nil {
		// No scripts, nothing to strip
		return nil
	}

	// Track if we made changes
	modified := false

	// Remove lifecycle scripts that cause issues with file: dependencies
	// - prepare/prepublish: run during npm install of file: deps, can fail (e.g., husky)
	// Matches yalc behavior: https://github.com/wclr/yalc/blob/master/src/copy.ts
	for _, script := range strippedScripts {
		if _, exists := scripts[script]; exists {
			delete(scripts, script)
			modified = true
			debug.Logf("store: stripped %s script from package.json", script)
		}
	}

	if !modified {
		return nil
	}

	// Write modified package.json atomically using temp file + rename
	output, err := marshalManifest(pkgJSON)
	if err != nil {
		return fmt.Errorf("failed to marshal package.json: %w", err)
	}

	// The rewrite replaces the store's own package.json, and every path that puts
	// it there has already given it the source file's mode, so that mode is what
	// the temp file has to carry over the rename. A hard-coded mode here leaves
	// the manifest disagreeing with its siblings in the same package - in which
	// direction depends on the umask, so neither "wider" nor "narrower" is the
	// right word: 0644 written under umask 0022 widens a 0600 manifest, and the
	// same write under 0077 lands at 0600 and narrows a 0644 one.
	//
	// The paths that set that mode arrive at it differently, which is worth
	// naming because a reader checking one of them will not find the same
	// mechanism in the others. copyFile above chmods the destination explicitly.
	// fsutil.Reflink chmods on Linux (reflink_linux.go) but not on darwin, where
	// unix.Clonefile carries the mode across as part of the metadata it clones.
	// reflink_other.go has no clone path at all - Reflink always fails there, and
	// the caller falls back to copyFile.
	//
	// Perm() masks off setuid, setgid and sticky, which copyFile does preserve.
	// The claim that supports dropping them is narrow, so state it narrowly: pack
	// folds only Mode.Perm() into the content hash (internal/pack/pack.go), so
	// the permission bits are the ones the *hash* is taken over. The database row
	// is a different matter - it records the full info.Mode() from the pack walk,
	// so after this rewrite a manifest carrying setgid disagrees with its own
	// row. Nothing reads that back today (fileManifestHash re-hashes with Perm()),
	// which makes it harmless rather than intended, and it is recorded here so a
	// later reader does not have to rediscover it.
	info, err := os.Stat(pkgJSONPath)
	if err != nil {
		return fmt.Errorf("failed to stat package.json: %w", err)
	}
	mode := info.Mode().Perm()

	// Write to temp file first
	tmpPath := pkgJSONPath + ".tmp"
	if err := os.WriteFile(tmpPath, output, mode); err != nil {
		return fmt.Errorf("failed to write temp package.json: %w", err)
	}

	// WriteFile's mode argument is masked by the process umask, for the reason
	// copyFile's comment above spells out. Set the bits explicitly. Doing it on
	// the temp file rather than after the rename keeps the mode part of what the
	// rename commits, so package.json is never observable at the masked mode.
	if err := os.Chmod(tmpPath, mode); err != nil {
		_ = os.Remove(tmpPath) // Clean up on failure (error ignored)
		return fmt.Errorf("failed to set temp package.json mode: %w", err)
	}

	// Atomic rename (overwrites existing)
	if err := os.Rename(tmpPath, pkgJSONPath); err != nil {
		_ = os.Remove(tmpPath) // Clean up on failure (error ignored)
		return fmt.Errorf("failed to rename package.json: %w", err)
	}

	return nil
}
