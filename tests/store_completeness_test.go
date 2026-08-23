package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// gutStoreEntry reproduces what an interrupted `lnpm gc` leaves in the store:
// RemoveEntry unlinks the completeness marker first and then removes the tree,
// so a removal that dies partway - Ctrl-C, a permission error, a full disk -
// leaves a directory holding some of the package and no marker.
func (te *TestEnvironment) gutStoreEntry(packageName, keptFile, removedDir string) string {
	te.t.Helper()

	pkg, err := te.Database.GetPackageByName(packageName)
	if err != nil || pkg == nil {
		te.t.Fatalf("look up %s: pkg = %v, err = %v", packageName, pkg, err)
	}
	if err := os.Remove(filepath.Join(pkg.StorePath, ".lnpm-complete")); err != nil {
		te.t.Fatalf("remove completeness marker: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(pkg.StorePath, removedDir)); err != nil {
		te.t.Fatalf("remove %s from the store entry: %v", removedDir, err)
	}
	if _, err := os.Stat(filepath.Join(pkg.StorePath, keptFile)); err != nil {
		te.t.Fatalf("the gutted entry should still hold %s: %v", keptFile, err)
	}
	return pkg.StorePath
}

// publishGuttable publishes a package with a file at the top level and a
// directory beside it, so that removing the directory leaves an entry that is
// damaged but not empty.
func (te *TestEnvironment) publishGuttable(name string) {
	te.t.Helper()

	te.publishPkg(name, "1.0.0", map[string]string{
		"index.js":     "module.exports = require('./lib/util');",
		"lib/util.js":  "module.exports = 'util';",
		"lib/extra.js": "module.exports = 'extra';",
	})
}

// TestAddRefusesGuttedStoreEntry is the first of the two reproductions recorded
// in #330:
//
//	ADD OUTPUT: OK Added partialpkg@1.0.0 / Link type: hardlink
//	RESULT: .lnpm/partialpkg contains [.lnpm-linked index.js package.json]   (lib/ missing)
//
// add enumerated the store entry and linked whatever had survived, so the
// project got a truncated package reported as a success.
func TestAddRefusesGuttedStoreEntry(t *testing.T) {
	env := setupTest(t)

	env.publishGuttable("partialpkg")
	storePath := env.gutStoreEntry("partialpkg", "index.js", "lib")
	projectDir := env.newProject("partial-project")

	env.chdir(projectDir)
	err := cli.RunAdd("partialpkg", false, false, false)

	if err == nil {
		t.Fatalf("add linked the gutted store entry %s instead of refusing it", storePath)
	}
	for _, want := range []string{"partialpkg", "1.0.0", storePath, "gc", "publish"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q, so it does not name the package, the entry, or how to rebuild it: %v", want, err)
		}
	}
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm", "partialpkg"), false)
}

// TestDoctorReportsGuttedStoreEntry covers doctor's store check, which stat'ed
// the entry directory and therefore called a gutted package healthy - the
// "DOCTOR: Checking store completeness markers... OK / All checks passed!" line
// of the same reproduction.
func TestDoctorReportsGuttedStoreEntry(t *testing.T) {
	env := setupTest(t)

	env.publishAndAddGuttable(t, "doctor-partialpkg")
	storePath := env.gutStoreEntry("doctor-partialpkg", "index.js", "lib")

	out := captureStdout(t, func() {
		if err := cli.RunDoctor(); err == nil {
			t.Error("RunDoctor() = nil for a store holding a gutted entry, want an error so `lnpm doctor && ...` stops")
		}
	})

	if strings.Contains(out, "All checks passed!") {
		t.Errorf("RunDoctor called a store holding the gutted entry %s healthy, output was:\n%s", storePath, out)
	}
	if !strings.Contains(out, "incomplete") {
		t.Errorf("RunDoctor did not report the gutted entry %s as incomplete, output was:\n%s", storePath, out)
	}
}

// TestDoctorReportsIncompleteEntryNoPackageRowNames is the same finding reached
// from the other side. gc deletes the database row before the store entry, so
// the entry a failed removal leaves behind is one no package row points at, and
// the per-package check cannot see it at all.
func TestDoctorReportsIncompleteEntryNoPackageRowNames(t *testing.T) {
	env := setupTest(t)

	// A real published package first, so the store is one lnpm 2.x wrote. An
	// unmarked entry only means "damaged" there; in a store that has never held
	// a marker it means "not migrated yet", which doctor reports differently.
	env.simplePkg("real-pkg")

	orphan := filepath.Join(env.StoreDir, "store", "stray-pkg", "abc123")
	if err := os.MkdirAll(orphan, 0755); err != nil {
		t.Fatalf("seed store entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "index.js"), []byte("stray"), 0644); err != nil {
		t.Fatalf("seed store entry file: %v", err)
	}

	out := captureStdout(t, func() {
		if err := cli.RunDoctor(); err == nil {
			t.Error("RunDoctor() = nil for a store holding an unmarked entry, want an error")
		}
	})

	if !strings.Contains(out, orphan) {
		t.Errorf("RunDoctor did not name the unmarked entry %s, output was:\n%s", orphan, out)
	}
}

// unmigrateStore strips every completeness marker and the backfill sentinel
// from the store, leaving it in the shape lnpm 1.x wrote: entries with content
// and no bookkeeping. Markers shipped in 2.0.0, so this is what every store
// upgrading from 1.12.0 or earlier looks like.
func (te *TestEnvironment) unmigrateStore() {
	te.t.Helper()

	root := filepath.Join(te.StoreDir, "store")
	stripped := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		switch {
		case err != nil:
			return err
		case info.IsDir():
			return nil
		case info.Name() != ".lnpm-complete" && info.Name() != ".lnpm-markers-backfilled":
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		stripped++
		return nil
	})
	if err != nil {
		te.t.Fatalf("unmigrate store: %v", err)
	}
	if stripped < 2 {
		te.t.Fatalf("unmigrate store: removed %d files, want at least an entry marker and the sentinel", stripped)
	}
}

// TestAddMigratesAStoreWrittenBeforeMarkers is the upgrade from lnpm 1.x, and
// it is the criterion that decides how strict the read path is allowed to be.
// Markers shipped in 2.0.0; every store from 1.12.0 or earlier has none, and a
// read path that refused an unmarked entry outright would fail every command
// against a store lnpm itself wrote, with no recovery short of deleting the
// store and re-publishing everything.
func TestAddMigratesAStoreWrittenBeforeMarkers(t *testing.T) {
	env := setupTest(t)

	env.publishGuttable("legacy-pkg")
	env.unmigrateStore()
	projectDir := env.newProject("legacy-project")

	env.addPkg(projectDir, "legacy-pkg", false, false)

	env.AssertFileExists(filepath.Join(projectDir, ".lnpm", "legacy-pkg", "index.js"), true)
	env.AssertFileExists(filepath.Join(projectDir, ".lnpm", "legacy-pkg", "lib", "util.js"), true)
}

// TestDoctorReportsAnUnmigratedStoreAsPending is the same store seen by the one
// command that cannot fix it: doctor never opens the store, so the migration
// does not run. Listing every entry as damaged and advising a re-publish would
// be wrong on both counts, and failing the command would break
// `lnpm doctor && ...` on a store that is one command away from being fine.
func TestDoctorReportsAnUnmigratedStoreAsPending(t *testing.T) {
	env := setupTest(t)

	env.publishAndAdd("legacy-pkg")
	env.unmigrateStore()

	out := captureStdout(t, func() {
		if err := cli.RunDoctor(); err != nil {
			t.Errorf("RunDoctor() = %v for a store awaiting migration, want nil", err)
		}
	})

	if !strings.Contains(out, "PENDING") {
		t.Errorf("RunDoctor did not report the store as awaiting migration, output was:\n%s", out)
	}
	for _, unwanted := range []string{"incomplete store entry", "missing or incomplete"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("RunDoctor reported %q for a store that is merely unmigrated, output was:\n%s", unwanted, out)
		}
	}
}

// TestPushDoesNotReuseAnEntryItCannotVouchFor covers push's own completeness
// check, which is the one read path that does not go through GetFiles: push
// links the projects from the store path it decided to reuse, so an entry taken
// on trust there is materialised with nothing else looking at it.
//
// The damage here is a marker naming another hash rather than a missing one.
// push already re-stored when the marker was absent - that case was covered by
// the presence test it had before #330 - so a marker that is present but does
// not belong to the directory is the shape that separates the two behaviours.
//
// The outcome is a refusal and not a repair, and that is worth being plain
// about: push cannot rebuild the entry, because Store never renames over an
// occupied destination, so the command stops with the directory named. Blunt,
// but the alternative it replaces is pushing a store entry lnpm cannot vouch
// for into every linked project.
func TestPushDoesNotReuseAnEntryItCannotVouchFor(t *testing.T) {
	env := setupTest(t)

	pkgDir, projectDir := env.publishAndAdd("push-suspect")
	storePath := env.retagStoreMarker("push-suspect", "ffffffffffffffff")

	env.chdir(pkgDir)
	err := cli.RunPush(true)

	if err == nil {
		t.Fatalf("push reused the store entry %s, whose marker names another hash, and linked it into %s", storePath, projectDir)
	}
	if !strings.Contains(err.Error(), storePath) {
		t.Errorf("the failure does not name the entry standing in the way, so the user cannot act on it: %v", err)
	}
}

// retagStoreMarker rewrites a store entry's completeness marker so it records
// hash instead of the directory it sits in - what an entry copied or moved
// between hash directories looks like - and returns the entry's path.
func (te *TestEnvironment) retagStoreMarker(packageName, hash string) string {
	te.t.Helper()

	pkg, err := te.Database.GetPackageByName(packageName)
	if err != nil || pkg == nil {
		te.t.Fatalf("look up %s: pkg = %v, err = %v", packageName, pkg, err)
	}
	payload := []byte(`{"schemaVersion":1,"hash":"` + hash + `"}` + "\n")
	if err := os.WriteFile(filepath.Join(pkg.StorePath, ".lnpm-complete"), payload, 0644); err != nil {
		te.t.Fatalf("rewrite completeness marker: %v", err)
	}
	return pkg.StorePath
}

// publishAndAddGuttable publishes a package with the shape gutStoreEntry needs
// and links it into a fresh project, so the entry can then be damaged with the
// database still describing it as linked.
func (te *TestEnvironment) publishAndAddGuttable(t *testing.T, name string) {
	t.Helper()

	te.publishGuttable(name)
	projectDir := te.newProject(name + "-project")
	te.addPkg(projectDir, name, false, false)
}
