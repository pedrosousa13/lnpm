package link

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/pack"
)

// manifestName is the file a linked package carries recording what was linked
// into it. A relink reads it to tell which files it can leave alone, which is
// what stops a push costing the whole package every time.
//
// It lives inside .lnpm/{package} rather than beside it so that it commits with
// the content: Link writes it into the temp directory and the swap renames both
// into place at once, exactly as the store's own completeness marker is written
// as the last file inside the entry it describes. A manifest kept outside the
// directory could survive a swap that did not happen, or outlive one that did,
// and would then claim a file holds content it does not - which is the one
// failure mode an incremental relink must not have.
const manifestName = ".lnpm-linked"

// manifestSchemaVersion identifies the manifest payload's shape. A manifest
// written by a different version is ignored rather than guessed at, which costs
// one full relink and nothing else.
const manifestSchemaVersion = 1

// linkedFile is what the manifest records per file: enough to decide whether
// the copy already sitting in .lnpm/{package} is the one the new link wants.
//
// Mode is part of that decision and not decoration. Reuse is a hard link, so the
// carried-over file keeps the mode of the one already there; a package whose
// only change is chmod +x on a bin script would otherwise be reported unchanged
// and stay unexecutable.
type linkedFile struct {
	Hash string      `json:"hash"`
	Mode os.FileMode `json:"mode"`
}

// linkManifest is the manifest payload.
//
// Path is the manifest's own location. A manifest describes one directory, and
// what makes it safe to act on is that the directory it describes is the one it
// is sitting in - so a .lnpm/{package} that arrived by some route other than a
// link is not believed. Copying a project's .lnpm/ into another checkout is the
// case that actually happens: the manifest comes along, still describing files
// by hash, and without this the relink there would carry the copy's content
// forward as though it had linked it. The check costs one comparison and the
// mismatch costs one full relink, after which the manifest names its new home.
//
// It is not a security boundary, and does not need to be. A package cannot get a
// file to this path at all - see withoutManifestName - so the only manifests
// here are ones a link wrote.
//
// LinkType is what the link that wrote the manifest achieved, not what it set
// out to do, so a relink that materialises nothing can report the tree it left
// alone rather than a fresh prediction that has nothing to do with it.
type linkManifest struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Path          string                `json:"path"`
	LinkType      LinkType              `json:"linkType"`
	Files         map[string]linkedFile `json:"files"`
}

// manifestPath is the location a manifest written for lnpmPath records as its
// own. It names the directory rather than the string a caller happened to reach
// it by: absolute, so a relative path still reads its own manifest, and with the
// links along the way resolved, so do two callers that spell the same directory
// differently.
//
// They routinely do. add, pull, remove and restore build their linker from the
// working directory; push and publish build theirs from the project path the
// database recorded, which is stored through db.normalizePath and so already has
// its symlinks resolved. On unix the working directory comes back from getcwd(3)
// resolved too and the two agree by accident, but on Windows it keeps whatever
// 8.3 short name it was given - C:\Users\RUNNER~1\... against the database's
// C:\Users\runneradmin\... - and every add-then-push relink rejected its own
// manifest and rewrote the whole package.
//
// The parent is what gets resolved, not lnpmPath itself. Link creates it before
// writing the manifest and readManifest has just stat'd inside it, so it exists
// at both ends where .lnpm/{package} need not; and .lnpm/{package} is itself a
// link when the package was added with --link, which this must name rather than
// follow into the package's source. The unresolved path when that fails, which
// at worst costs a full relink.
func manifestPath(lnpmPath string) string {
	abs, err := filepath.Abs(lnpmPath)
	if err != nil {
		abs = filepath.Clean(lnpmPath)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return abs
	}
	return filepath.Join(parent, filepath.Base(abs))
}

// readManifest returns what the last link recorded at lnpmPath, or nil when
// there is nothing trustworthy to read. Every failure returns nil: a missing,
// unreadable, truncated or foreign-versioned manifest means the relink falls
// back to materialising everything, which is what it did before this existed.
//
// A .lnpm/{package} that is not a real directory is refused outright. LinkSource
// leaves a link to the package's live source there, and reusing "unchanged"
// files through it would hard link whatever the author has since edited those
// files to.
func readManifest(lnpmPath string) *linkManifest {
	if info, err := os.Lstat(lnpmPath); err != nil || !info.IsDir() {
		return nil
	}

	data, err := os.ReadFile(filepath.Join(lnpmPath, manifestName))
	if err != nil {
		return nil
	}

	var m linkManifest
	if err := json.Unmarshal(data, &m); err != nil {
		debug.Logf("link: ignoring unreadable manifest in %s: %v", lnpmPath, err)
		return nil
	}
	if m.SchemaVersion != manifestSchemaVersion {
		debug.Logf("link: ignoring manifest in %s written with schema %d", lnpmPath, m.SchemaVersion)
		return nil
	}
	if want := manifestPath(lnpmPath); m.Path != want {
		debug.Logf("link: ignoring manifest in %s, which records %q rather than %q as its own location", lnpmPath, m.Path, want)
		return nil
	}
	return &m
}

// writeManifest records files in dir, which is the temp directory Link is about
// to swap into place, describing a link at lnpmPath, which is where that swap
// will put it.
//
// The two paths differ because the manifest commits with the content it
// describes: it is written inside the temp directory and renamed into place with
// everything else. What it records is the destination, since that is where the
// next relink will read it from and what it has to match.
//
// Failure is reported to the debug log and no further, per ADR-0001: the tree
// being committed is complete and correct either way, and a manifest that could
// not be written only costs the next relink the work this one saved. Failing the
// link over it would undo something the caller did ask for.
func writeManifest(dir, lnpmPath string, linkType LinkType, files []*pack.FileInfo) {
	m := linkManifest{
		SchemaVersion: manifestSchemaVersion,
		Path:          manifestPath(lnpmPath),
		LinkType:      linkType,
		Files:         make(map[string]linkedFile, len(files)),
	}
	for _, f := range files {
		m.Files[f.RelPath] = linkedFile{Hash: f.ContentHash, Mode: f.Mode}
	}

	payload, err := json.Marshal(m)
	if err != nil {
		debug.Logf("link: failed to encode manifest for %s: %v", dir, err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, manifestName), payload, 0644); err != nil {
		debug.Logf("link: failed to write manifest for %s: %v", dir, err)
	}
}

// reusableFiles returns the relative paths whose copy in the previous link is
// already what the new link wants, so it can be carried over instead of
// materialised again.
//
// present is what scanLinked found on disk, and a path missing from it is never
// reusable however well the manifest describes it. The manifest records what was
// linked, not what survived: relinking has always been the way to repair a
// .lnpm/{package} something else has damaged, and reuse is a hard link, which
// would carry a file that is no longer a regular file straight into the
// replacement.
//
// An empty ContentHash is never reusable either. Not every caller knows its
// files' hashes - a link driven straight off a walk of the store does not - and
// treating "no hash recorded" as "matches the other file with no hash recorded"
// would carry over stale content.
func reusableFiles(prior *linkManifest, present map[string]bool, files []*pack.FileInfo) map[string]bool {
	if prior == nil {
		return nil
	}

	reusable := make(map[string]bool, len(files))
	for _, f := range files {
		was, ok := prior.Files[f.RelPath]
		if ok && present[f.RelPath] && f.ContentHash != "" && was.Hash == f.ContentHash && was.Mode == f.Mode {
			reusable[f.RelPath] = true
		}
	}
	return reusable
}

// scanLinked reports what a linked package directory actually holds: the regular
// files under it, keyed by slash-separated relative path, and a count of
// everything else it met.
//
// That count is anything a link cannot have put there - an entry that is not a
// regular file, a directory holding nothing, a directory that could not be read.
// A link materialises regular files and creates only the directories those files
// need, so any of those means the tree has been changed from outside, and the
// shortcut that skips a relink entirely must not be taken over it.
//
// One walk answers both questions the shortcut needs - is every file still there,
// and is anything there that should not be - where an lstat per file answers only
// the first. Reading a directory returns its entries' types along with their
// names, so neither answer costs a stat of its own.
func scanLinked(lnpmPath string) (map[string]bool, int) {
	found := make(map[string]bool)
	unexpected := 0

	var walk func(dir, rel string)
	walk = func(dir, rel string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			unexpected++
			return
		}
		if len(entries) == 0 && rel != "" {
			unexpected++
			return
		}
		for _, e := range entries {
			childRel := e.Name()
			if rel != "" {
				childRel = rel + "/" + e.Name()
			}
			switch {
			case e.IsDir():
				walk(filepath.Join(dir, e.Name()), childRel)
			case e.Type().IsRegular():
				found[childRel] = true
			default:
				unexpected++
			}
		}
	}
	walk(lnpmPath, "")

	return found, unexpected
}

// withoutManifestName returns files with any file at the manifest's own path
// dropped, which is the set a link materialises.
//
// The name is the linker's, exactly as .lnpm-complete is the store's: internal/
// store's GetFiles keeps its marker out of what it hands to consumers on the
// grounds that the marker belongs to the store and not to the package, and the
// same holds a level down. What .lnpm/{package} holds is the package plus the
// record of the link that put it there.
//
// Reserving the name is what makes the collision impossible rather than merely
// detectable. One path cannot hold two files, and yielding it to the package
// would mean .lnpm/{package}/.lnpm-linked is sometimes a record of the link and
// sometimes content that looks like one, with nothing on the way back in able to
// tell which - a version that shipped a manifest naming the next version's
// hashes could then describe its own successor's relink.
//
// The cost is the one the store already pays for its marker: a package that
// ships a file called .lnpm-linked at its root will not find it in
// .lnpm/{package}. Nothing else about the package changes, and reuse is not lost
// the way it was when the collision disabled the manifest.
func withoutManifestName(files []*pack.FileInfo) []*pack.FileInfo {
	for i, f := range files {
		if f.RelPath != manifestName {
			continue
		}
		debug.Logf("link: not linking the package's own %s, which is the link manifest's name", manifestName)
		kept := make([]*pack.FileInfo, 0, len(files)-1)
		kept = append(kept, files[:i]...)
		kept = append(kept, files[i+1:]...)
		return kept
	}
	return files
}
