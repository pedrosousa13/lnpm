package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
)

// TestRunStatusFailsWhenAConsumerLookupFails pins the fail-closed half of
// status's headline listing.
//
// The consumer lookup used to be the one caller of it that swallowed its error
// and carried on to the next package. What that produced was the worst shape a
// listing can have: an "Active Links" table missing a project, printed with no
// mark on it, so a user reading it concludes that project is not linked. The
// only honest answer for a read status could not complete is to say so and
// print no table at all.
func TestRunStatusFailsWhenAConsumerLookupFails(t *testing.T) {
	database := newStatusStore(t)
	pkg := seedStatusLink(t, database)

	damageDatabase(t, "links_by_package", linkKey(pkg.ID), []byte("[ not ids"))

	var err error
	out := captureStdout(t, func() { err = RunStatus() })

	if err == nil {
		t.Fatalf("RunStatus() = nil for a consumer lookup it could not complete, output was:\n%s", out)
	}
	if !strings.Contains(err.Error(), pkg.Name) {
		t.Errorf("RunStatus() error = %v, want it to name the package %q whose consumers it could not read", err, pkg.Name)
	}
	if strings.Contains(out, "Active Links") {
		t.Errorf("RunStatus printed the Active Links table after a consumer lookup failed; the table would be short and nothing on it would say so. Output was:\n%s", out)
	}
}

// newStatusStore points lnpm at a fresh store and database and returns the open
// database for the caller to seed.
//
// LNPM_CONFIG is redirected at a file that does not exist alongside LNPM_STORE,
// so a read that consults the config rather than the store path finds nothing of
// the developer's own ~/.lnpm/config.yaml (#371).
//
// The redirection is worth nothing on its own, though, because config memoises
// the parsed file for the whole process: an earlier test in this package that
// populated the cache would leave a later read answering from it and never
// looking at LNPM_CONFIG at all. The cache is therefore dropped before the test
// as well as after it, which is what newDoctorStoreConfig does and for the same
// reason.
func newStatusStore(t *testing.T) *db.DB {
	t.Helper()

	base := t.TempDir()
	t.Setenv("LNPM_STORE", base)
	t.Setenv("LNPM_CONFIG", filepath.Join(base, "config.yaml"))

	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)

	db.ResetForTesting()
	t.Cleanup(db.ResetForTesting)

	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	return database
}

// seedStatusLink plants one package consumed by one project and returns the
// package.
func seedStatusLink(t *testing.T, database *db.DB) *db.Package {
	t.Helper()

	pkg := &db.Package{Name: "linked-pkg", Version: "1.0.0", ContentHash: "0123456789abcdef"}
	if err := database.InsertPackage(pkg); err != nil {
		t.Fatalf("insert package: %v", err)
	}
	proj := &db.Project{Path: t.TempDir(), Name: "consumer"}
	if err := database.InsertProject(proj); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if err := database.InsertLink(&db.Link{PackageID: pkg.ID, ProjectID: proj.ID, LinkType: "hardlink"}); err != nil {
		t.Fatalf("insert link: %v", err)
	}
	return pkg
}
