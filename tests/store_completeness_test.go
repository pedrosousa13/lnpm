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

// publishAndAddGuttable publishes a package with the shape gutStoreEntry needs
// and links it into a fresh project, so the entry can then be damaged with the
// database still describing it as linked.
func (te *TestEnvironment) publishAndAddGuttable(t *testing.T, name string) {
	t.Helper()

	te.publishGuttable(name)
	projectDir := te.newProject(name + "-project")
	te.addPkg(projectDir, name, false, false)
}
