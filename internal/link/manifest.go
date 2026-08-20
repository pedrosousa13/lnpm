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
type linkManifest struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Files         map[string]linkedFile `json:"files"`
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
func readManifest(lnpmPath string) map[string]linkedFile {
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
	return m.Files
}

// writeManifest records files in dir, which is the temp directory Link is about
// to swap into place.
//
// Failure is reported to the debug log and no further, per ADR-0001: the tree
// being committed is complete and correct either way, and a manifest that could
// not be written only costs the next relink the work this one saved. Failing the
// link over it would undo something the caller did ask for.
func writeManifest(dir string, files []*pack.FileInfo) {
	m := linkManifest{
		SchemaVersion: manifestSchemaVersion,
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
func reusableFiles(prior map[string]linkedFile, present map[string]bool, files []*pack.FileInfo) map[string]bool {
	if prior == nil {
		return nil
	}

	reusable := make(map[string]bool, len(files))
	for _, f := range files {
		was, ok := prior[f.RelPath]
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

// shipsManifestName reports whether the package itself contains a file at the
// path the manifest occupies. It does, very occasionally, happen that a name
// collides; when it does the package's file wins and the relink simply goes
// without a manifest, because there is no reading of one path holding two files
// where both survive. The cost is a full relink every time, which is what every
// relink cost before manifests existed.
func shipsManifestName(files []*pack.FileInfo) bool {
	for _, f := range files {
		if f.RelPath == manifestName {
			return true
		}
	}
	return false
}
