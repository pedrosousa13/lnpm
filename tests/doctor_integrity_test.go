package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestDoctorVerifiesPublishedStoreContent is the accepting half of #439, run
// against an entry lnpm itself wrote rather than one a fixture assembled: the
// package is published and linked the ordinary way, so the files carry the
// write protection #333 applies and the hardlinks add creates.
func TestDoctorVerifiesPublishedStoreContent(t *testing.T) {
	env := setupTest(t)

	env.publishAndAdd("intact-pkg")

	out := captureStdout(t, func() {
		if err := cli.RunDoctor(true); err != nil {
			t.Errorf("RunDoctor(true) = %v for a store lnpm just wrote, want nil", err)
		}
	})

	if !strings.Contains(out, "Checking store file integrity... ") || !strings.Contains(out, "re-hashed") {
		t.Errorf("RunDoctor did not report what it re-hashed, output was:\n%s", out)
	}
}

// TestDoctorReportsPoisonedStoreContent is #439's reproduction. During #333's
// investigation a project's write reached the store through a shared inode, a
// second project was then created from the tampered file, and doctor reported
// "store file integrity... OK" the whole way through.
func TestDoctorReportsPoisonedStoreContent(t *testing.T) {
	env := setupTest(t)

	env.publishAndAdd("poisoned-pkg")
	storePath := env.poisonStoreFile("poisoned-pkg", "index.js", "module.exports = 'poisoned';")

	out := captureStdout(t, func() {
		if err := cli.RunDoctor(true); err == nil {
			t.Error("RunDoctor(true) = nil for a poisoned store entry, want an error so `lnpm doctor && ...` stops")
		}
	})

	if strings.Contains(out, "All checks passed") {
		t.Errorf("RunDoctor called the poisoned entry %s healthy, output was:\n%s", storePath, out)
	}
	for _, want := range []string{"poisoned-pkg", "1.0.0", "index.js"} {
		if !strings.Contains(out, want) {
			t.Errorf("RunDoctor did not name %q, output was:\n%s", want, out)
		}
	}
}

// TestDoctorDoesNotFaultAManifestTheStoreRewrote covers lnpm's own rewrite of a
// stored package.json. store.stripLifecycleScripts removes prepare and
// prepublish after the content hash has been taken, so for a package that has
// one the stored bytes legitimately differ from the hash recorded for them - the
// same carve-out AssertStoredContentHash has to make. A content check that read
// that as damage would fault every package with a build step in it.
func TestDoctorDoesNotFaultAManifestTheStoreRewrote(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("prepare-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'prepare-pkg';",
	})
	env.writeFile(filepath.Join(pkgDir, "package.json"),
		`{"name":"prepare-pkg","version":"1.0.0","scripts":{"prepare":"echo build"}}`)
	env.chdir(pkgDir)
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("publish prepare-pkg: %v", err)
	}

	out := captureStdout(t, func() {
		if err := cli.RunDoctor(true); err != nil {
			t.Errorf("RunDoctor(true) = %v for a manifest lnpm rewrote itself, want nil", err)
		}
	})

	if strings.Contains(out, "do not hold the content recorded for them") {
		t.Errorf("RunDoctor reported lnpm's own rewrite of package.json as damage, output was:\n%s", out)
	}
}

// poisonStoreFile replaces a file inside a published package's store entry with
// other bytes, and returns the entry's path.
//
// Removed and recreated rather than written in place, which is forced twice
// over: #333 leaves store content read-only, so an in-place write is refused
// outright, and on a hardlink store the file is one inode shared with every
// project the package is linked into, so writing through it would move the
// damage rather than plant it.
func (te *TestEnvironment) poisonStoreFile(packageName, relPath, content string) string {
	te.t.Helper()

	pkg, err := te.Database.GetPackageByName(packageName)
	if err != nil || pkg == nil {
		te.t.Fatalf("look up %s: pkg = %v, err = %v", packageName, pkg, err)
	}
	path := filepath.Join(pkg.StorePath, filepath.FromSlash(relPath))
	before, err := os.ReadFile(path)
	if err != nil {
		te.t.Fatalf("read %s: %v", path, err)
	}
	if string(before) == content {
		te.t.Fatalf("%s already holds the replacement content, so this fixture tampers with nothing", path)
	}
	if err := os.Remove(path); err != nil {
		te.t.Fatalf("remove %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0444); err != nil {
		te.t.Fatalf("rewrite %s: %v", path, err)
	}
	return pkg.StorePath
}
