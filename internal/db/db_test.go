package db

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// lockMessage is the phrase users should see when the database is already held
// by a concurrent lnpm invocation.
const lockMessage = "another lnpm process"

// shortenOpenTimeout points initDB at a timeout the test can afford to wait
// out, and restores the production value afterwards.
func shortenOpenTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	previous := openTimeout
	openTimeout = d
	t.Cleanup(func() { openTimeout = previous })
}

// holdDatabaseLock opens the store's bbolt file directly, standing in for a
// second lnpm process that already holds the flock. bbolt locks per open file
// description, so a separate handle in this process conflicts exactly the way
// a separate process would.
func holdDatabaseLock(t *testing.T, storeDir string) *bolt.DB {
	t.Helper()
	holder, err := bolt.Open(filepath.Join(storeDir, "lnpm.db"), 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Failed to take the database lock: %v", err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	return holder
}

// writeSchemaV1Database writes a database in the shape the build before tags
// wrote: no tags bucket, no schema version, and one record per package name in
// bucketPackagesByName.
//
// The bucket names are spelled out rather than taken from the constants. What an
// existing user's database holds is those literal names, so a rename that broke
// every deployed store would have to fail here rather than travel silently into
// both the fixture and the code under test.
func writeSchemaV1Database(t *testing.T, storeDir string, packages ...*Package) {
	t.Helper()

	boltDB, err := bolt.Open(filepath.Join(storeDir, "lnpm.db"), 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Failed to create the old database: %v", err)
	}
	defer func() { _ = boltDB.Close() }()

	err = boltDB.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{
			"packages", "packages_by_name", "projects", "projects_by_path",
			"links", "links_by_package", "links_by_project", "files", "meta",
		} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		nextID := int64(1)
		for _, pkg := range packages {
			pkg.ID = nextID
			nextID++
			data, err := json.Marshal(pkg)
			if err != nil {
				return err
			}
			if err := tx.Bucket([]byte("packages")).Put(itob(pkg.ID), data); err != nil {
				return err
			}
			if err := tx.Bucket([]byte("packages_by_name")).Put([]byte(pkg.Name), itob(pkg.ID)); err != nil {
				return err
			}
		}
		return tx.Bucket([]byte("meta")).Put([]byte("next_id"), itob(nextID))
	})
	if err != nil {
		t.Fatalf("Failed to seed the old database: %v", err)
	}
}

// openStore points lnpm at storeDir and opens the database the way a command
// would, so whatever initDB does on open is what the test exercises.
func openStore(t *testing.T, storeDir string) *DB {
	t.Helper()

	ResetForTesting()
	t.Setenv("LNPM_STORE", storeDir)
	t.Cleanup(ResetForTesting)

	database, err := GetDB()
	if err != nil {
		t.Fatalf("Failed to open the database: %v", err)
	}
	return database
}

// TestGetDB_UpgradesADatabaseWrittenWithoutTags pins the upgrade path for a
// store an existing user already has. The record that name index pointed at was
// that package's latest by definition, so opening the store has to say so —
// otherwise the package is published, on disk and reachable by name, but no tag
// reaches it, which is the state gc now collects.
func TestGetDB_UpgradesADatabaseWrittenWithoutTags(t *testing.T) {
	storeDir := t.TempDir()
	writeSchemaV1Database(t, storeDir, &Package{
		Name:        "legacy-pkg",
		Version:     "1.2.3",
		ContentHash: "legacyhash",
		StorePath:   filepath.Join(storeDir, "store", "legacy-pkg", "legacyhash"),
	})

	database := openStore(t, storeDir)

	pkg, err := database.GetPackageByName("legacy-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("GetPackageByName after the upgrade = %v, %v; want the legacy package", pkg, err)
	}
	if pkg.ContentHash != "legacyhash" || pkg.Version != "1.2.3" {
		t.Errorf("the upgrade rewrote the package record: %+v", pkg)
	}

	tags, err := database.TagsForPackage("legacy-pkg")
	if err != nil {
		t.Fatalf("TagsForPackage: %v", err)
	}
	if got := tags[DefaultTag]; got != "legacyhash" {
		t.Errorf("the %s tag points at %q, want the hash the name index named", DefaultTag, got)
	}
	if len(tags) != 1 {
		t.Errorf("the upgrade wrote %v, want only the %s tag", tags, DefaultTag)
	}

	resolved, err := database.ResolveTag("legacy-pkg", DefaultTag)
	if err != nil || resolved == nil {
		t.Fatalf("ResolveTag after the upgrade = %v, %v; want the legacy package", resolved, err)
	}
}

// TestGetDB_UpgradeRecordsTheSchemaVersion pins the marker the upgrade leaves
// behind, and that a second open leaves a tag that has moved since alone.
//
// The marker is what a later schema change will branch on, and it is what stops
// the backfill re-reading every name on every command. It is asserted directly
// because the backfill is idempotent on its own — bucketPackagesByName tracks
// the latest tag, so re-running it writes what is already there — which means no
// observable behaviour would betray a missing marker until the next migration
// needed it, by which point it would be too late for the store that lost it.
func TestGetDB_UpgradeRecordsTheSchemaVersion(t *testing.T) {
	storeDir := t.TempDir()
	writeSchemaV1Database(t, storeDir, &Package{
		Name:        "legacy-pkg",
		Version:     "1.0.0",
		ContentHash: "hash-v1",
	})

	database := openStore(t, storeDir)
	if err := database.InsertPackage(&Package{
		Name:        "legacy-pkg",
		Version:     "2.0.0",
		ContentHash: "hash-v2",
	}); err != nil {
		t.Fatalf("publish a second version: %v", err)
	}

	// Re-open, as the next lnpm command would.
	database = openStore(t, storeDir)

	var recorded int64
	if err := database.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketMeta).Get(keySchemaVersion)
		if len(v) == 8 {
			recorded = btoi(v)
		}
		return nil
	}); err != nil {
		t.Fatalf("read the schema version: %v", err)
	}
	if recorded != schemaVersion {
		t.Errorf("the upgrade recorded schema version %d, want %d", recorded, schemaVersion)
	}

	tags, err := database.TagsForPackage("legacy-pkg")
	if err != nil {
		t.Fatalf("TagsForPackage: %v", err)
	}
	if tags[DefaultTag] != "hash-v2" {
		t.Errorf("re-opening moved the %s tag to %q, want hash-v2", DefaultTag, tags[DefaultTag])
	}
}

// TestGetDB_UpgradeToleratesADanglingNameIndex pins that a name index entry
// naming a record that is not there does not stop the store opening. gc is the
// command a user reaches for when the store is already damaged, and it cannot
// run if opening the database fails.
func TestGetDB_UpgradeToleratesADanglingNameIndex(t *testing.T) {
	storeDir := t.TempDir()
	writeSchemaV1Database(t, storeDir, &Package{
		Name:        "good-pkg",
		Version:     "1.0.0",
		ContentHash: "goodhash",
	})

	boltDB, err := bolt.Open(filepath.Join(storeDir, "lnpm.db"), 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Failed to re-open the old database: %v", err)
	}
	err = boltDB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("packages_by_name")).Put([]byte("ghost-pkg"), itob(9999))
	})
	if err != nil {
		t.Fatalf("Failed to seed the dangling index entry: %v", err)
	}
	if err := boltDB.Close(); err != nil {
		t.Fatalf("Failed to close the old database: %v", err)
	}

	database := openStore(t, storeDir)

	tags, err := database.TagsForPackage("ghost-pkg")
	if err != nil {
		t.Fatalf("TagsForPackage: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("the upgrade tagged a name with no record: %v", tags)
	}
	if tags, _ := database.TagsForPackage("good-pkg"); tags[DefaultTag] != "goodhash" {
		t.Errorf("the dangling entry stopped the rest of the upgrade: %v", tags)
	}
}

// TestGetDB_RefusesADatabaseFromANewerBuild pins that a store written by a
// build this one does not understand is refused at open rather than opened and
// operated on.
//
// Declining to migrate it is not enough on its own. The session carries on, and
// this build then applies its own rules to a database whose shape it is guessing
// at - gc most of all, which decides what to delete from reachability roots that
// a later schema may have moved.
func TestGetDB_RefusesADatabaseFromANewerBuild(t *testing.T) {
	storeDir := t.TempDir()
	writeSchemaV1Database(t, storeDir, &Package{
		Name:        "future-pkg",
		Version:     "1.0.0",
		ContentHash: "futurehash",
	})
	writeSchemaVersion(t, storeDir, schemaVersion+1)

	ResetForTesting()
	t.Setenv("LNPM_STORE", storeDir)
	t.Cleanup(ResetForTesting)

	database, err := GetDB()
	if err == nil {
		t.Fatal("GetDB opened a database written by a newer build, want a refusal")
	}
	if database != nil {
		t.Errorf("GetDB returned a usable handle alongside the refusal: %v", database)
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Errorf("GetDB error = %v, want it to say the store is from a newer lnpm", err)
	}
}

// TestGetDB_OpensADatabaseAtTheCurrentSchema is the other half: the refusal must
// fire on a newer schema only, not on the one this build writes.
func TestGetDB_OpensADatabaseAtTheCurrentSchema(t *testing.T) {
	storeDir := t.TempDir()
	writeSchemaV1Database(t, storeDir, &Package{
		Name:        "current-pkg",
		Version:     "1.0.0",
		ContentHash: "currenthash",
	})
	writeSchemaVersion(t, storeDir, schemaVersion)

	database := openStore(t, storeDir)
	if pkg, err := database.GetPackageByName("current-pkg"); err != nil || pkg == nil {
		t.Fatalf("GetPackageByName = %v, %v; want the package the store holds", pkg, err)
	}
}

// writeSchemaVersion stamps a schema version onto an existing database, so a
// test can present one this build did not write.
func writeSchemaVersion(t *testing.T, storeDir string, version int64) {
	t.Helper()

	boltDB, err := bolt.Open(filepath.Join(storeDir, "lnpm.db"), 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Failed to re-open the database: %v", err)
	}
	err = boltDB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("meta")).Put([]byte("schema_version"), itob(version))
	})
	if err != nil {
		t.Fatalf("Failed to stamp schema version %d: %v", version, err)
	}
	if err := boltDB.Close(); err != nil {
		t.Fatalf("Failed to close the database: %v", err)
	}
}

// TestGetDB_LockHeldElsewhere_NamesTheOtherProcess checks that a database the
// process cannot lock within the timeout is reported as a concurrency problem
// rather than a bare "timeout".
func TestGetDB_LockHeldElsewhere_NamesTheOtherProcess(t *testing.T) {
	ResetForTesting()
	storeDir := t.TempDir()
	t.Setenv("LNPM_STORE", storeDir)
	t.Cleanup(ResetForTesting)

	holdDatabaseLock(t, storeDir)
	shortenOpenTimeout(t, 200*time.Millisecond)

	_, err := GetDB()
	if err == nil {
		t.Fatal("Expected an error opening a database locked by another handle")
	}
	if !strings.Contains(err.Error(), lockMessage) {
		t.Errorf("Expected error to mention %q, got: %v", lockMessage, err)
	}
}

// TestGetDB_WaitsOutALockHeldLongerThanASecond checks the production timeout is
// generous enough that a second lnpm invocation waits for an in-flight one
// instead of dying almost immediately. It deliberately uses the production
// openTimeout so that shrinking it back re-breaks this test.
func TestGetDB_WaitsOutALockHeldLongerThanASecond(t *testing.T) {
	ResetForTesting()
	storeDir := t.TempDir()
	t.Setenv("LNPM_STORE", storeDir)
	t.Cleanup(ResetForTesting)

	holder := holdDatabaseLock(t, storeDir)
	time.AfterFunc(1500*time.Millisecond, func() { _ = holder.Close() })

	database, err := GetDB()
	if err != nil {
		t.Fatalf("Expected the open to wait for the lock to be released, got: %v", err)
	}
	if database == nil {
		t.Fatal("Expected a database instance")
	}
}

// TestGetDB_AfterAFailedInit_KeepsReportingTheError checks that a failed
// initialisation stays reported. sync.Once runs initDB exactly once, so every
// later caller has to be handed the error the first one saw — otherwise they
// get a nil handle with no error and panic on first use, losing the diagnostic.
//
// The singleton is package-global, so this test must not call t.Parallel() and
// must not run alongside one that does.
func TestGetDB_AfterAFailedInit_KeepsReportingTheError(t *testing.T) {
	ResetForTesting()
	storeDir := t.TempDir()
	t.Setenv("LNPM_STORE", storeDir)
	t.Cleanup(ResetForTesting)

	holdDatabaseLock(t, storeDir)
	shortenOpenTimeout(t, 200*time.Millisecond)

	if _, err := GetDB(); err == nil {
		t.Fatal("Expected the first call to fail against a locked database")
	}

	database, err := GetDB()
	if err == nil {
		t.Fatal("Expected the second call to report the failed initialisation too")
	}
	if !strings.Contains(err.Error(), lockMessage) {
		t.Errorf("Expected the second call's error to mention %q, got: %v", lockMessage, err)
	}
	if database != nil {
		t.Error("Expected no database instance from a failed initialisation")
	}
}

// TestGetDB_AfterResetFollowingAFailedInit_Succeeds is the flip side: the
// remembered error must be cleared by ResetForTesting, or a process that failed
// once could never open the database again.
//
// The singleton is package-global, so this test must not call t.Parallel() and
// must not run alongside one that does.
func TestGetDB_AfterResetFollowingAFailedInit_Succeeds(t *testing.T) {
	ResetForTesting()
	storeDir := t.TempDir()
	t.Setenv("LNPM_STORE", storeDir)
	t.Cleanup(ResetForTesting)

	holder := holdDatabaseLock(t, storeDir)
	shortenOpenTimeout(t, 200*time.Millisecond)

	if _, err := GetDB(); err == nil {
		t.Fatal("Expected the first call to fail against a locked database")
	}

	if err := holder.Close(); err != nil {
		t.Fatalf("Failed to release the database lock: %v", err)
	}
	ResetForTesting()

	database, err := GetDB()
	if err != nil {
		t.Fatalf("Expected the open to succeed once the lock was released, got: %v", err)
	}
	if database == nil {
		t.Fatal("Expected a database instance")
	}
}

// TestGetDB_NonTimeoutFailure_DoesNotBlameAnotherProcess pins the error
// classification: an open that fails for a reason other than the lock must not
// be reported as a concurrent lnpm invocation.
func TestGetDB_NonTimeoutFailure_DoesNotBlameAnotherProcess(t *testing.T) {
	ResetForTesting()
	storeDir := t.TempDir()
	t.Setenv("LNPM_STORE", storeDir)
	t.Cleanup(ResetForTesting)

	// A non-empty file that is not a bbolt database fails meta-page validation.
	// Nothing holds the lock, so this cannot be a timeout.
	dbPath := filepath.Join(storeDir, "lnpm.db")
	if err := os.WriteFile(dbPath, []byte(strings.Repeat("not a bbolt database", 256)), 0600); err != nil {
		t.Fatalf("Failed to write invalid database file: %v", err)
	}
	shortenOpenTimeout(t, 200*time.Millisecond)

	_, err := GetDB()
	if err == nil {
		t.Fatal("Expected an error opening an invalid database file")
	}
	if strings.Contains(err.Error(), lockMessage) {
		t.Errorf("Expected error not to blame another lnpm process, got: %v", err)
	}
}

// writeLinkRow inserts a link row and its two index entries directly, bypassing
// InsertLink.
//
// It exists because InsertLink now keeps one row per project and package name:
// a second call for the same pair deletes the first row rather than adding
// beside it, so the duplicate cannot be built through the public API any more.
// A database written before that rule can still hold one, and moveLinksTx's
// merge branch is the path that heals it, so the branch has to be reached with a
// duplicate built the way such a database holds one.
func writeLinkRow(t *testing.T, d *DB, link *Link) {
	t.Helper()

	err := d.db.Update(func(tx *bolt.Tx) error {
		id, err := d.nextID(tx)
		if err != nil {
			return err
		}
		link.ID = id
		link.CreatedAt = time.Now()
		link.UpdatedAt = time.Now()

		data, err := json.Marshal(link)
		if err != nil {
			return err
		}
		if err := tx.Bucket(bucketLinks).Put(itob(id), data); err != nil {
			return err
		}

		for _, index := range []struct {
			bucket *bolt.Bucket
			key    []byte
			owner  string
		}{
			{tx.Bucket(bucketLinksByPackage), itob(link.PackageID), "package"},
			{tx.Bucket(bucketLinksByProject), itob(link.ProjectID), "project"},
		} {
			ids, err := indexIDs(index.bucket, index.key, index.owner)
			if err != nil {
				return err
			}
			if err := putIndexIDs(index.bucket, index.key, append(ids, id)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to write a link row: %v", err)
	}
}

// insertVersion records one version of a package under the default tag and
// returns it with its assigned ID.
func insertVersion(t *testing.T, d *DB, name, hash string) *Package {
	t.Helper()

	pkg := &Package{
		Name:        name,
		Version:     "1.0.0",
		ContentHash: hash,
		SourcePath:  "/src/" + name,
		StorePath:   "/store/" + name + "/" + hash,
		FilesCount:  1,
		TotalSize:   1,
	}
	if err := d.InsertPackage(pkg); err != nil {
		t.Fatalf("Failed to insert %s@%s: %v", name, hash, err)
	}
	return pkg
}

// TestSetTag_MergesADuplicateLinkAProjectHolds pins the healing path in
// moveLinksTx: carrying links across a tag move must not leave a project holding
// two rows for one package.
//
// Everything that reads links treats one row per project and package as given,
// so a second row makes remove and gc report a link that nothing can clear. The
// duplicate is written directly rather than through InsertLink, which now
// prevents it - built through InsertLink the project would hold one row before
// the tag ever moves, moveLinksTx would merely repoint it, and the branch this
// test exists for would never be entered while every assertion still passed.
func TestSetTag_MergesADuplicateLinkAProjectHolds(t *testing.T) {
	database := openStore(t, t.TempDir())

	v1 := insertVersion(t, database, "merge-pkg", "h1")
	v2 := insertVersion(t, database, "merge-pkg", "h2")

	projectPath := filepath.FromSlash("/projects/merger")
	if err := database.InsertProject(&Project{Path: projectPath, Name: "merger"}); err != nil {
		t.Fatalf("Failed to insert the project: %v", err)
	}
	proj, err := database.GetProjectByPath(projectPath)
	if err != nil || proj == nil {
		t.Fatalf("Failed to read the project back: %v", err)
	}

	writeLinkRow(t, database, &Link{PackageID: v1.ID, ProjectID: proj.ID, LinkType: "reflink"})
	writeLinkRow(t, database, &Link{PackageID: v2.ID, ProjectID: proj.ID, LinkType: "reflink"})

	before, err := database.GetLinksForProject(proj.ID)
	if err != nil {
		t.Fatalf("Failed to read the project's links: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("The fixture holds %d link rows, want the duplicate this test exists for", len(before))
	}

	// Moving the default tag back onto the first version carries the second
	// version's link across, into a project that already holds one there.
	if err := database.SetTag("merge-pkg", DefaultTag, "h1"); err != nil {
		t.Fatalf("Failed to move the default tag: %v", err)
	}

	links, err := database.GetLinksForProject(proj.ID)
	if err != nil {
		t.Fatalf("Failed to read the project's links: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("The project holds %d links to merge-pkg, want 1", len(links))
	}
	if links[0].PackageID != v1.ID {
		t.Errorf("The surviving link names record %d, want the tagged version %d", links[0].PackageID, v1.ID)
	}

	projects, err := database.GetProjectsForPackage(v2.ID)
	if err != nil {
		t.Fatalf("Failed to read the consumers of the version moved off: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("The version the tag moved off still has %d consumer(s)", len(projects))
	}
}

// --- Version history ---------------------------------------------------------

// writePackageRow writes a package record straight into the bucket, without the
// timestamps insertPackageTx stamps from time.Now.
//
// GetPackageVersions orders on UpdatedAt and breaks a tie on ID, and a tie is
// the case the tie-break exists for: a coarse clock - Windows' is about 15ms -
// hands two publishes the same instant. Nothing that goes through InsertPackage
// can produce that on a Linux or macOS clock, so a test that wants it has to
// write the timestamp itself.
func writePackageRow(t *testing.T, d *DB, pkg *Package) {
	t.Helper()

	err := d.db.Update(func(tx *bolt.Tx) error {
		id, err := d.nextID(tx)
		if err != nil {
			return err
		}
		pkg.ID = id
		data, err := json.Marshal(pkg)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketPackages).Put(itob(id), data)
	})
	if err != nil {
		t.Fatalf("Failed to write a package row: %v", err)
	}
}

// TestGetPackageVersions_BreaksATimestampTieOnID pins the second half of the
// history's order.
//
// Two publishes can land inside one tick of a coarse clock, and without the
// tie-break the order would be arbitrary precisely when two versions are hardest
// to tell apart - and would differ between runs over an unchanged store. The
// records here carry one timestamp because that is the only state in which the
// tie-break is reached at all: an insert through the normal path stamps
// time.Now, which on this platform separates them every time.
func TestGetPackageVersions_BreaksATimestampTieOnID(t *testing.T) {
	storeDir := t.TempDir()
	database := openStore(t, storeDir)

	published := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, hash := range []string{"h1", "h2", "h3"} {
		writePackageRow(t, database, &Package{
			Name:        "tied-pkg",
			Version:     "1.0.0",
			ContentHash: hash,
			CreatedAt:   published,
			UpdatedAt:   published,
		})
	}

	versions, err := database.GetPackageVersions("tied-pkg")
	if err != nil {
		t.Fatalf("GetPackageVersions() error = %v", err)
	}

	var order []string
	for _, v := range versions {
		order = append(order, v.ContentHash)
	}
	if got := strings.Join(order, ","); got != "h3,h2,h1" {
		t.Errorf("GetPackageVersions() ordered three versions sharing one timestamp %s, want the most recently written first", got)
	}
}

// TestGetPackageVersions_SurfacesARecordThatWillNotUnmarshal pins that a version
// whose bytes will not parse stops the history rather than disappearing from it.
//
// A listing whose whole job is telling a user which build to roll back to must
// not drop a row for a read that failed: on screen that is indistinguishable
// from gc having collected the build, and the same lookup answers `lnpm add
// <pkg>@<hash>`, so the user would be told the build they are rolling back to
// does not exist. GetPackageByName, which the add path went through before this
// lookup existed, already surfaces a record it cannot parse.
func TestGetPackageVersions_SurfacesARecordThatWillNotUnmarshal(t *testing.T) {
	storeDir := t.TempDir()
	database := openStore(t, storeDir)

	if err := database.InsertPackage(&Package{
		Name:        "damaged-pkg",
		Version:     "1.0.0",
		ContentHash: "h1",
	}); err != nil {
		t.Fatalf("Failed to seed a version: %v", err)
	}

	err := database.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketPackages).Put(itob(9999), []byte("{ not a package"))
	})
	if err != nil {
		t.Fatalf("Failed to damage a record: %v", err)
	}

	if _, err := database.GetPackageVersions("damaged-pkg"); err == nil {
		t.Error("GetPackageVersions() skipped a record it could not read; a version missing from a rollback listing reads as one gc collected")
	}
}

// --- Unreadable link data ----------------------------------------------------

// seedLink records a package, a project and the link between them, and returns
// both so a test can damage exactly one row or index entry by ID.
func seedLink(t *testing.T, d *DB) (*Package, *Link) {
	t.Helper()

	pkg := insertVersion(t, d, "linked-pkg", "h1")
	proj := seedProject(t, d)

	link := &Link{PackageID: pkg.ID, ProjectID: proj.ID, LinkType: "hardlink"}
	if err := d.InsertLink(link); err != nil {
		t.Fatalf("Failed to insert the link: %v", err)
	}
	return pkg, link
}

// putRaw writes bytes straight into a bucket, standing in for the damage a torn
// write leaves behind.
func putRaw(t *testing.T, d *DB, bucket, key, value []byte) {
	t.Helper()

	err := d.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Put(key, value)
	})
	if err != nil {
		t.Fatalf("Failed to write into %s: %v", bucket, err)
	}
}

// TestGetLinksForPackage_ErrorsOnALinkRowThatWillNotUnmarshal pins the half of
// #329 the sweep reproduced first.
//
// gc treats this lookup's answer as the list of projects that would break if the
// version went away, so a row silently dropped is a consumer gc cannot see and a
// store entry gc deletes while a project is still using it. Per ADR-0001 the
// direction decides: this read widens a destructive set, so it fails loudly.
func TestGetLinksForPackage_ErrorsOnALinkRowThatWillNotUnmarshal(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedLink(t, database)

	putRaw(t, database, bucketLinks, itob(link.ID), []byte("{ not a link"))

	links, err := database.GetLinksForPackage(pkg.ID)
	if err == nil {
		t.Fatalf("GetLinksForPackage() returned %d link(s) and no error for a row it could not read; gc reads that as a version nothing links", len(links))
	}
	if links != nil {
		t.Errorf("GetLinksForPackage() returned %d link(s) alongside its error, which a caller could mistake for the whole list", len(links))
	}
}

// TestGetLinksForPackage_ErrorsOnAnIndexEntryThatWillNotUnmarshal pins the other
// half the sweep reproduced: the index entry, not the row, is the damaged one.
//
// It is the worse of the two, because one unreadable entry hides every link the
// package has rather than one of them.
func TestGetLinksForPackage_ErrorsOnAnIndexEntryThatWillNotUnmarshal(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, _ := seedLink(t, database)

	putRaw(t, database, bucketLinksByPackage, itob(pkg.ID), []byte("[ not ids"))

	links, err := database.GetLinksForPackage(pkg.ID)
	if err == nil {
		t.Fatalf("GetLinksForPackage() returned %d link(s) and no error for an index entry it could not read", len(links))
	}
	if links != nil {
		t.Errorf("GetLinksForPackage() returned %d link(s) alongside its error", len(links))
	}
}

// TestGetLinksForPackage_ErrorsOnAnIndexEntryNamingNoLinkRow pins the third
// shape, which the report did not name.
//
// Every path that deletes a link row scrubs the ID from both indexes inside the
// same bolt transaction - moveLinksTx, DeletePackage, deleteLinkRowTx and
// DeleteLink all do - and bolt commits a transaction whole or not at all. So no
// flow lnpm has leaves an index naming a row that is gone: reaching this state
// means the file was damaged, exactly as an unparseable row does. Tolerating it
// here would undercount the same consumers the two cases above hide.
func TestGetLinksForPackage_ErrorsOnAnIndexEntryNamingNoLinkRow(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedLink(t, database)

	err := database.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketLinks).Delete(itob(link.ID))
	})
	if err != nil {
		t.Fatalf("Failed to remove the link row: %v", err)
	}

	links, err := database.GetLinksForPackage(pkg.ID)
	if err == nil {
		t.Fatalf("GetLinksForPackage() returned %d link(s) and no error for an index entry naming no row", len(links))
	}
	if links != nil {
		t.Errorf("GetLinksForPackage() returned %d link(s) alongside its error", len(links))
	}
}

// TestInsertLink_DoesNotOverwriteAnIndexEntryItCannotRead pins the write half of
// #329, which is the half that destroys rather than hides.
//
// The index append used to decode the existing entry into a slice, ignore the
// failure, and write the slice back holding the one new ID. An entry that would
// not parse was therefore replaced by a one-element array, discarding every link
// ID it named - and those IDs are the consumers gc reads to decide what to
// delete. Losing them on the write path costs exactly what losing them on the
// read path costs, so ADR-0001 decides it the same way: refuse the insert and
// leave the damaged entry for lnpm doctor to report.
func TestInsertLink_DoesNotOverwriteAnIndexEntryItCannotRead(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, _ := seedLink(t, database)

	damaged := []byte("[ not ids")
	putRaw(t, database, bucketLinksByPackage, itob(pkg.ID), damaged)

	// A second project linking the same package is what reaches the append.
	secondProject := t.TempDir()
	if err := database.InsertProject(&Project{Path: secondProject, Name: "second"}); err != nil {
		t.Fatalf("Failed to seed a second project: %v", err)
	}
	proj, err := database.GetProjectByPath(secondProject)
	if err != nil || proj == nil {
		t.Fatalf("Failed to read the second project back: %v", err)
	}

	if err := database.InsertLink(&Link{PackageID: pkg.ID, ProjectID: proj.ID, LinkType: "hardlink"}); err == nil {
		t.Error("InsertLink() accepted a link for a package whose index entry it could not read")
	}

	var after []byte
	if err := database.db.View(func(tx *bolt.Tx) error {
		after = append(after, tx.Bucket(bucketLinksByPackage).Get(itob(pkg.ID))...)
		return nil
	}); err != nil {
		t.Fatalf("Failed to read the index entry back: %v", err)
	}
	if !bytes.Equal(after, damaged) {
		t.Errorf("InsertLink() rewrote an index entry it could not read: %q became %q; every link ID it named is gone", damaged, after)
	}
}

// TestGetLinksForPackage_ReturnsNothingForAPackageNothingLinks pins the case the
// three above have to stay distinguishable from: a package with no consumers is
// not damage, and gc must go on collecting it.
func TestGetLinksForPackage_ReturnsNothingForAPackageNothingLinks(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg := insertVersion(t, database, "unlinked-pkg", "h1")

	links, err := database.GetLinksForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("GetLinksForPackage() error = %v for a package nothing links", err)
	}
	if len(links) != 0 {
		t.Errorf("GetLinksForPackage() returned %d link(s) for a package nothing links", len(links))
	}
}

// TestGetLinksForProject_ErrorsOnAnIndexEntryThatWillNotUnmarshal pins the
// project-side twin of the links_by_package case above.
//
// An entry read as no IDs is a project told it consumes nothing while its
// node_modules is full of links lnpm put there. linksOfProject hands that to
// pull, remove and retreat: pull then reads every package as following the
// default tag and carries a beta consumer onto latest, and remove and retreat
// find no name to match when they reach the delete, having taken the files out
// by then - so every row survives, and gc goes on reading each one as a live
// consumer.
func TestGetLinksForProject_ErrorsOnAnIndexEntryThatWillNotUnmarshal(t *testing.T) {
	database := openStore(t, t.TempDir())
	_, link := seedLink(t, database)

	putRaw(t, database, bucketLinksByProject, itob(link.ProjectID), []byte("[ not ids"))

	links, err := database.GetLinksForProject(link.ProjectID)
	if err == nil {
		t.Fatalf("GetLinksForProject() returned %d link(s) and no error for an index entry it could not read", len(links))
	}
	if links != nil {
		t.Errorf("GetLinksForProject() returned %d link(s) alongside its error", len(links))
	}
}

// TestGetLinksForProject_ErrorsOnALinkRowThatWillNotUnmarshal pins the row half,
// which costs one package rather than all of them.
//
// remove and retreat look a package's name up in this list to find the row to
// delete, and they reach that lookup after taking the package out of
// node_modules. A dropped row is a name they cannot find there, so the database
// row stays behind while the files it recorded are gone, and nothing tells the
// user it was there.
func TestGetLinksForProject_ErrorsOnALinkRowThatWillNotUnmarshal(t *testing.T) {
	database := openStore(t, t.TempDir())
	_, link := seedLink(t, database)

	putRaw(t, database, bucketLinks, itob(link.ID), []byte("{ not a link"))

	links, err := database.GetLinksForProject(link.ProjectID)
	if err == nil {
		t.Fatalf("GetLinksForProject() returned %d link(s) and no error for a row it could not read", len(links))
	}
	if links != nil {
		t.Errorf("GetLinksForProject() returned %d link(s) alongside its error", len(links))
	}
}

// TestGetLinksForProject_SkipsAnIndexEntryNamingNoLinkRow pins the asymmetry
// with GetLinksForPackage, which errors on this same shape.
//
// A dangling ID in links_by_package is unreachable except through damage to the
// file. links_by_project is not. Until #355 DeletePackage's loop deleted a link
// row unconditionally while scrubbing this index only when it could parse the
// row - the row being where the project ID to scrub under is written - so an
// unparseable row was deleted with its ID left behind. Stores carry those
// dangling IDs today and nothing repairs them, and all three callers return the
// error rather than carrying on - so erroring would refuse pull, remove and
// retreat outright on a store lnpm's own bug damaged, which is to say it would
// lock the user out of the commands that clean a project up.
//
// The fixture deletes the row directly rather than by replaying that loop. The
// loop is strict as of #355 and can no longer produce the state, and a test that
// reproduced it would have to pin behaviour deliberately removed; what is under
// test is how the reader treats a dangling ID, not how one is made. The
// DeletePackage tests below hold the loop to never making another.
func TestGetLinksForProject_SkipsAnIndexEntryNamingNoLinkRow(t *testing.T) {
	database := openStore(t, t.TempDir())
	_, link := seedLink(t, database)

	err := database.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketLinks).Delete(itob(link.ID))
	})
	if err != nil {
		t.Fatalf("Failed to remove the link row: %v", err)
	}

	links, err := database.GetLinksForProject(link.ProjectID)
	if err != nil {
		t.Fatalf("GetLinksForProject() error = %v for a dangling ID pre-#355 DeletePackage left behind", err)
	}
	if len(links) != 0 {
		t.Errorf("GetLinksForProject() returned %d link(s) for an index naming one row that is gone", len(links))
	}
}

// TestGetProjectsForPackage_ErrorsOnAnIndexEntryThatWillNotUnmarshal pins the
// index half for the consumer lookup publish, push and status read.
//
// It walks links_by_package, the same index GetLinksForPackage reads, so an
// entry it cannot parse hides every consumer the package has: `lnpm publish
// --push` would report nothing to push while projects go on holding the old
// version.
func TestGetProjectsForPackage_ErrorsOnAnIndexEntryThatWillNotUnmarshal(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, _ := seedLink(t, database)

	putRaw(t, database, bucketLinksByPackage, itob(pkg.ID), []byte("[ not ids"))

	projects, err := database.GetProjectsForPackage(pkg.ID)
	if err == nil {
		t.Fatalf("GetProjectsForPackage() returned %d project(s) and no error for an index entry it could not read", len(projects))
	}
	if projects != nil {
		t.Errorf("GetProjectsForPackage() returned %d project(s) alongside its error", len(projects))
	}
}

// TestGetProjectsForPackage_ErrorsOnALinkRowThatWillNotUnmarshal pins the row
// half: one consumer dropped from a push list is one project left on a version
// the user believes it was moved off.
func TestGetProjectsForPackage_ErrorsOnALinkRowThatWillNotUnmarshal(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedLink(t, database)

	putRaw(t, database, bucketLinks, itob(link.ID), []byte("{ not a link"))

	projects, err := database.GetProjectsForPackage(pkg.ID)
	if err == nil {
		t.Fatalf("GetProjectsForPackage() returned %d project(s) and no error for a row it could not read", len(projects))
	}
	if projects != nil {
		t.Errorf("GetProjectsForPackage() returned %d project(s) alongside its error", len(projects))
	}
}

// TestGetProjectsForPackage_ErrorsOnAnIndexEntryNamingNoLinkRow pins that this
// reader holds the same line on links_by_package that GetLinksForPackage does.
//
// Every path that deletes a link row scrubs the ID from that index inside one
// bolt transaction, so a dangling ID there means the file was damaged. The two
// readers of one index must not disagree about what its damage means.
func TestGetProjectsForPackage_ErrorsOnAnIndexEntryNamingNoLinkRow(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedLink(t, database)

	err := database.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketLinks).Delete(itob(link.ID))
	})
	if err != nil {
		t.Fatalf("Failed to remove the link row: %v", err)
	}

	projects, err := database.GetProjectsForPackage(pkg.ID)
	if err == nil {
		t.Fatalf("GetProjectsForPackage() returned %d project(s) and no error for an index entry naming no row", len(projects))
	}
	if projects != nil {
		t.Errorf("GetProjectsForPackage() returned %d project(s) alongside its error", len(projects))
	}
}

// TestGetProjectsForPackage_ErrorsOnAProjectRecordThatWillNotParse pins the last
// read this lookup makes.
//
// A record that will not parse is damage wherever it is met, and dropping the
// project here costs what dropping the link costs: a consumer missing from the
// push list. GetProjectByID already refuses this shape, from #292.
func TestGetProjectsForPackage_ErrorsOnAProjectRecordThatWillNotParse(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedLink(t, database)

	putRaw(t, database, bucketProjects, itob(link.ProjectID), []byte("{ not a project"))

	projects, err := database.GetProjectsForPackage(pkg.ID)
	if err == nil {
		t.Fatalf("GetProjectsForPackage() returned %d project(s) and no error for a record it could not read", len(projects))
	}
	if projects != nil {
		t.Errorf("GetProjectsForPackage() returned %d project(s) alongside its error", len(projects))
	}
}

// TestGetProjectsForPackage_SkipsALinkNamingNoProjectRecord pins the one shape
// this lookup goes on tolerating.
//
// A link whose project record is gone is the one piece of missing data lnpm
// already answers for: doctor counts it as an orphaned link, and gc files it
// under "project not found in database" and removes it under --fix-links.
// Erroring would break publish --push, push and status on a store in exactly
// the state that repair exists to fix.
func TestGetProjectsForPackage_SkipsALinkNamingNoProjectRecord(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedLink(t, database)

	err := database.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketProjects).Delete(itob(link.ProjectID))
	})
	if err != nil {
		t.Fatalf("Failed to remove the project record: %v", err)
	}

	projects, err := database.GetProjectsForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("GetProjectsForPackage() error = %v for an orphaned link, which gc --fix-links repairs", err)
	}
	if len(projects) != 0 {
		t.Errorf("GetProjectsForPackage() returned %d project(s) for a link naming no record", len(projects))
	}
}

// seedDeletablePackage records the state DeletePackage tears down: a package
// with a file and a link. The three tests below each damage one part of it and
// hold the delete to leaving the rest alone.
func seedDeletablePackage(t *testing.T, d *DB) (*Package, *Link) {
	t.Helper()

	pkg, link := seedLink(t, d)
	if err := d.InsertFiles(pkg.ID, []*FileEntry{{
		PackageID:    pkg.ID,
		RelativePath: "index.js",
		ContentHash:  "f1",
		Size:         1,
	}}); err != nil {
		t.Fatalf("Failed to seed the package's files: %v", err)
	}
	return pkg, link
}

// assertNothingDeleted reads back every key a delete removes, so a refusal is
// held to leaving the store as it was rather than only to reporting an error.
//
// The name index is in the list because it is the proof that bolt rolled the
// transaction back: DeletePackage clears it before it ever reads the link index,
// so it is the one key a refusal cannot have skipped over.
func assertNothingDeleted(t *testing.T, d *DB, pkg *Package, link *Link) {
	t.Helper()

	err := d.db.View(func(tx *bolt.Tx) error {
		for _, want := range []struct {
			what   string
			bucket []byte
			key    []byte
		}{
			{"the package record", bucketPackages, itob(pkg.ID)},
			{"the package's files", bucketFiles, itob(pkg.ID)},
			{"the package's link index", bucketLinksByPackage, itob(pkg.ID)},
			{"the project's link index", bucketLinksByProject, itob(link.ProjectID)},
			{"the name index, which it clears before it reads the link index", bucketPackagesByName, []byte(pkg.Name)},
		} {
			if tx.Bucket(want.bucket).Get(want.key) == nil {
				t.Errorf("DeletePackage() removed %s after refusing the delete; bolt has to roll the whole transaction back", want.what)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to read the store back: %v", err)
	}
}

// TestDeletePackage_DeletesNothingWhenTheLinkIndexWillNotParse pins the first of
// the three shapes that make the package's link set unreadable.
//
// The delete used to discard this unmarshal error. linkIDs was nil, so the loop
// below never ran and no link row was deleted, and then the links_by_package key
// was deleted anyway - the one thing that named those rows. The rows became
// orphans: still there, still named by links_by_project, reachable from no
// package index at all.
func TestDeletePackage_DeletesNothingWhenTheLinkIndexWillNotParse(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedDeletablePackage(t, database)

	damaged := []byte("[ not ids")
	putRaw(t, database, bucketLinksByPackage, itob(pkg.ID), damaged)

	if err := database.DeletePackage(pkg.ID); err == nil {
		t.Fatal("DeletePackage() reported success for a package whose link index it could not read")
	}

	assertNothingDeleted(t, database, pkg, link)
	err := database.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketLinks).Get(itob(link.ID)) == nil {
			t.Error("DeletePackage() removed a link row after failing to read the index that names it")
		}
		if after := tx.Bucket(bucketLinksByPackage).Get(itob(pkg.ID)); !bytes.Equal(after, damaged) {
			t.Errorf("DeletePackage() left the link index as %q, want the damaged entry %q it could not read", after, damaged)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to read the store back: %v", err)
	}
}

// TestDeletePackage_DeletesNothingWhenALinkRowWillNotParse pins the shape that
// manufactured the dangling links_by_project IDs this issue is about.
//
// The loop reads each row only to learn which project to scrub the ID from, and
// a row it could not parse skipped the scrub - but the delete of the row ran
// regardless. The row went, the project's index went on naming it, and the ID
// dangled. That is lnpm damaging its own store out of damage it could have
// reported, and it is why GetLinksForProject has to tolerate a dangling ID
// today.
func TestDeletePackage_DeletesNothingWhenALinkRowWillNotParse(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedDeletablePackage(t, database)

	damaged := []byte("{ not a link")
	putRaw(t, database, bucketLinks, itob(link.ID), damaged)

	if err := database.DeletePackage(pkg.ID); err == nil {
		t.Fatal("DeletePackage() reported success for a package holding a link row it could not read")
	}

	assertNothingDeleted(t, database, pkg, link)
	err := database.db.View(func(tx *bolt.Tx) error {
		if after := tx.Bucket(bucketLinks).Get(itob(link.ID)); !bytes.Equal(after, damaged) {
			t.Errorf("DeletePackage() left link row %d as %q, want the damaged row %q; deleting it without scrubbing the project index is what leaves a dangling ID", link.ID, after, damaged)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to read the store back: %v", err)
	}
}

// TestDeletePackage_DeletesNothingWhenTheIndexNamesNoLinkRow pins the third
// shape, which the loop used to walk straight past.
//
// A missing row means the link set this delete is working from is not the whole
// one: the ID names a link whose project cannot be learned, so no index can be
// scrubbed for it, and carrying on would delete the package while leaving
// whatever that ID stood for unaccounted. GetLinksForPackage refuses this shape
// on this same index, and a writer that tolerates what the reader refuses would
// leave the two disagreeing about what the index's damage means.
func TestDeletePackage_DeletesNothingWhenTheIndexNamesNoLinkRow(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedDeletablePackage(t, database)

	err := database.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketLinks).Delete(itob(link.ID))
	})
	if err != nil {
		t.Fatalf("Failed to remove the link row: %v", err)
	}

	if err := database.DeletePackage(pkg.ID); err == nil {
		t.Fatal("DeletePackage() reported success for a package whose link index names a row the store does not hold")
	}

	assertNothingDeleted(t, database, pkg, link)
}

// --- Unreadable link index entries on the link-delete path -------------------

// assertLinkDeleteRolledBack holds a refused DeleteLink to leaving the store as
// it was: the row it was asked to delete, the entry it could not read, and the
// other index's entry are all still there.
//
// damagedBucket carries the bytes a test wrote with putRaw, and intactBucket the
// entry DeleteLink would have rewritten had it not refused. Both are checked
// because the two blocks ran one after the other: the by-package one used to
// have finished its own delete by the time the by-project one met damage, so a
// fix that only stops the second block would still have lost the first entry.
func assertLinkDeleteRolledBack(t *testing.T, d *DB, link *Link, damagedBucket, damagedKey, damaged, intactBucket, intactKey []byte) {
	t.Helper()

	err := d.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketLinks).Get(itob(link.ID)) == nil {
			t.Errorf("DeleteLink() removed link row %d after refusing the delete; bolt has to roll the whole transaction back", link.ID)
		}
		if after := tx.Bucket(damagedBucket).Get(damagedKey); !bytes.Equal(after, damaged) {
			t.Errorf("DeleteLink() left %s as %q, want the damaged entry %q it could not read; every link ID it named is gone", damagedBucket, after, damaged)
		}
		intact := tx.Bucket(intactBucket).Get(intactKey)
		if intact == nil {
			t.Errorf("DeleteLink() removed the %s entry after refusing the delete", intactBucket)
			return nil
		}
		var ids []int64
		if err := json.Unmarshal(intact, &ids); err != nil {
			t.Errorf("the %s entry no longer parses after a refused delete: %v", intactBucket, err)
			return nil
		}
		for _, id := range ids {
			if id == link.ID {
				return nil
			}
		}
		t.Errorf("the %s entry holds %v after a refused delete, want link %d still named", intactBucket, ids, link.ID)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to read the store back: %v", err)
	}
}

// TestDeleteLink_LeavesAPackageIndexEntryItCannotRead pins #392 on the
// links_by_package half.
//
// The scrub discarded its unmarshal, so an entry that would not parse decoded to
// no IDs at all. The filter loop then produced an empty slice, which the scrub
// reads as "this package has no links left" and answers by deleting the whole
// entry - dropping every link ID it named to remove one. Per ADR-0001 the
// direction decides: that widens a delete out of an error, so the entry is left
// alone and the delete is refused, as DeletePackage does with this same index.
func TestDeleteLink_LeavesAPackageIndexEntryItCannotRead(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedLink(t, database)

	damaged := []byte("[ not ids")
	putRaw(t, database, bucketLinksByPackage, itob(pkg.ID), damaged)

	if err := database.DeleteLink(pkg.ID, link.ProjectID); err == nil {
		t.Fatal("DeleteLink() reported success for a package index entry it could not read")
	}

	assertLinkDeleteRolledBack(t, database, link,
		bucketLinksByPackage, itob(pkg.ID), damaged,
		bucketLinksByProject, itob(link.ProjectID))
}

// TestDeleteLink_LeavesAProjectIndexEntryItCannotRead pins the same defect on
// links_by_project, which carried its own copy of the scrub.
//
// Fixing one index and not the other is a partial fix, and this half costs more
// than the other: a project's entry names every package it consumes, so losing
// it strands the lot.
func TestDeleteLink_LeavesAProjectIndexEntryItCannotRead(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedLink(t, database)

	damaged := []byte("[ not ids")
	putRaw(t, database, bucketLinksByProject, itob(link.ProjectID), damaged)

	if err := database.DeleteLink(pkg.ID, link.ProjectID); err == nil {
		t.Fatal("DeleteLink() reported success for a project index entry it could not read")
	}

	assertLinkDeleteRolledBack(t, database, link,
		bucketLinksByProject, itob(link.ProjectID), damaged,
		bucketLinksByPackage, itob(pkg.ID))
}

// TestDeleteLink_DeletesAnIndexEntryThatGenuinelyBecomesEmpty pins the case the
// two above have to stay distinguishable from, and it is the distinction that
// was the defect: an entry left holding no IDs is a package nothing links any
// more, and its key has to go. Reading an unreadable entry as that same
// "nothing left" is what turned one damaged entry into the loss of every ID it
// named, so a fix that refused both would be no fix at all.
func TestDeleteLink_DeletesAnIndexEntryThatGenuinelyBecomesEmpty(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedLink(t, database)

	if err := database.DeleteLink(pkg.ID, link.ProjectID); err != nil {
		t.Fatalf("DeleteLink() = %v for a readable store, want nil", err)
	}

	err := database.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketLinks).Get(itob(link.ID)) != nil {
			t.Errorf("DeleteLink() left link row %d behind", link.ID)
		}
		for _, left := range []struct {
			bucket []byte
			key    []byte
		}{
			{bucketLinksByPackage, itob(pkg.ID)},
			{bucketLinksByProject, itob(link.ProjectID)},
		} {
			if after := tx.Bucket(left.bucket).Get(left.key); after != nil {
				t.Errorf("DeleteLink() left %s holding %q, want the key gone once no ID is left", left.bucket, after)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to read the store back: %v", err)
	}
}

// TestDeleteLink_RemovesOnlyTheDeletedIDFromASharedEntry pins the ordinary path
// the refusal above must not swallow: an entry naming several links keeps the
// ones the delete was not for.
//
// This is the links_by_package half. Two projects consume one package, so the
// package's entry names two links and survives the delete of one. The project
// entry cannot be read for the same guarantee from this fixture - each project
// holds a single link, so the deleted link's project entry legitimately empties
// and its key goes. The sibling below builds the other shape for it.
func TestDeleteLink_RemovesOnlyTheDeletedIDFromASharedEntry(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedLink(t, database)

	secondPath := filepath.FromSlash("/projects/second-consumer")
	if err := database.InsertProject(&Project{Path: secondPath, Name: "second-consumer"}); err != nil {
		t.Fatalf("Failed to insert the second project: %v", err)
	}
	second, err := database.GetProjectByPath(secondPath)
	if err != nil || second == nil {
		t.Fatalf("Failed to read the second project back: %v", err)
	}
	kept := &Link{PackageID: pkg.ID, ProjectID: second.ID, LinkType: "hardlink"}
	if err := database.InsertLink(kept); err != nil {
		t.Fatalf("Failed to insert the second link: %v", err)
	}

	if err := database.DeleteLink(pkg.ID, link.ProjectID); err != nil {
		t.Fatalf("DeleteLink() = %v for a readable store, want nil", err)
	}

	err = database.db.View(func(tx *bolt.Tx) error {
		var ids []int64
		if err := json.Unmarshal(tx.Bucket(bucketLinksByPackage).Get(itob(pkg.ID)), &ids); err != nil {
			t.Errorf("the package index no longer parses: %v", err)
			return nil
		}
		if len(ids) != 1 || ids[0] != kept.ID {
			t.Errorf("the package index holds %v, want only link %d, the one the delete was not for", ids, kept.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to read the store back: %v", err)
	}
}

// TestDeleteLink_RemovesOnlyTheDeletedIDFromASharedProjectEntry pins the same
// guarantee on links_by_project, the index the fixture above cannot ask about.
//
// A shared project entry needs the mirror-image fixture: one project consuming
// two packages, so that deleting one link leaves the entry holding the other
// package's link rather than emptying. Both indexes go through one shared scrub
// now, but they were two inlined copies when #392 was filed and each failed the
// same way on its own - fixing one and not the other would have been a partial
// fix, and only a shared entry on each index can tell them apart.
func TestDeleteLink_RemovesOnlyTheDeletedIDFromASharedProjectEntry(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedLink(t, database)

	// A second package rather than a second version of the same one: InsertLink
	// keeps a project to one row per package name, so two versions of one name
	// would leave the project holding a single link and no shared entry.
	other := insertVersion(t, database, "other-pkg", "h2")
	kept := &Link{PackageID: other.ID, ProjectID: link.ProjectID, LinkType: "hardlink"}
	if err := database.InsertLink(kept); err != nil {
		t.Fatalf("Failed to insert the project's second link: %v", err)
	}

	if err := database.DeleteLink(pkg.ID, link.ProjectID); err != nil {
		t.Fatalf("DeleteLink() = %v for a readable store, want nil", err)
	}

	err := database.db.View(func(tx *bolt.Tx) error {
		var ids []int64
		if err := json.Unmarshal(tx.Bucket(bucketLinksByProject).Get(itob(link.ProjectID)), &ids); err != nil {
			t.Errorf("the project index no longer reads back as a list of IDs: %v", err)
			return nil
		}
		if len(ids) != 1 || ids[0] != kept.ID {
			t.Errorf("the project index holds %v, want only link %d, the one the delete was not for", ids, kept.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to read the store back: %v", err)
	}
}

// --- The refusal removeIDFromIndex gave its other callers --------------------
//
// #392 widened removeIDFromIndex from void to an error, and DeleteLink is only
// one of the four callers that now carry one. The other three each gained a
// user-visible refusal that did not exist before: the scrub used to return
// silently on an entry it could not parse, so the operation ran to completion
// and reported success. verification-discipline's rule for a read made strict is
// that every caller be enumerated and checked, and the three tests below are
// what discharges it - one per caller, each holding the operation to refusing
// and to leaving the store exactly as it was.
//
// Keeping the refusal is the ADR-0001 direction in all three. The alternative in
// each is committing a link-row delete whose ID goes on being named from an
// entry lnpm could not read, which is damage lnpm made rather than damage it
// found.

// TestSetTag_LeavesAProjectIndexEntryItCannotRead discharges the audit on
// moveLinksTx, whose scrub is reached through setTagTx - so from SetTag, and
// from every publish, since insertPackageTx calls setTagTx too.
//
// The scrub only runs on moveLinksTx's merge branch, where a project already
// holds a link on the version the tag is moving to and the duplicate is deleted.
// That is why the fixture writes its two rows directly: InsertLink keeps a
// project to one row per package name, so the duplicate this branch heals can no
// longer be built through it. TestSetTag_MergesADuplicateLinkAProjectHolds
// builds the same fixture and says more about why.
func TestSetTag_LeavesAProjectIndexEntryItCannotRead(t *testing.T) {
	database := openStore(t, t.TempDir())

	v1 := insertVersion(t, database, "merge-pkg", "h1")
	v2 := insertVersion(t, database, "merge-pkg", "h2")
	proj := seedProject(t, database)

	first := &Link{PackageID: v1.ID, ProjectID: proj.ID, LinkType: "reflink"}
	writeLinkRow(t, database, first)
	duplicate := &Link{PackageID: v2.ID, ProjectID: proj.ID, LinkType: "reflink"}
	writeLinkRow(t, database, duplicate)

	damaged := []byte("[ not ids")
	putRaw(t, database, bucketLinksByProject, itob(proj.ID), damaged)

	// Moving the default tag back onto the first version carries the second
	// version's link across, into a project that already holds one there.
	if err := database.SetTag("merge-pkg", DefaultTag, "h1"); err == nil {
		t.Fatal("SetTag() reported success for a project index entry it could not read")
	}

	err := database.db.View(func(tx *bolt.Tx) error {
		for _, l := range []*Link{first, duplicate} {
			if tx.Bucket(bucketLinks).Get(itob(l.ID)) == nil {
				t.Errorf("SetTag() removed link row %d after refusing the tag move", l.ID)
			}
		}
		if after := tx.Bucket(bucketLinksByProject).Get(itob(proj.ID)); !bytes.Equal(after, damaged) {
			t.Errorf("SetTag() left the project index as %q, want the damaged entry %q it could not read", after, damaged)
		}
		// The tag itself is the proof bolt rolled the transaction back:
		// setTagTx writes it before moveLinksTx is called at all.
		if after := tx.Bucket(bucketTags).Get(tagKey("merge-pkg", DefaultTag)); string(after) != "h2" {
			t.Errorf("the default tag names %q after a refused move, want h2, the version it named before", after)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to read the store back: %v", err)
	}
}

// TestDeletePackage_LeavesAProjectIndexEntryItCannotRead discharges the audit on
// DeletePackage's by-project scrub.
//
// #355 made this method's read of links_by_package strict and deliberately left
// this scrub of links_by_project silent, because a void scrub had nothing to
// report. It has now, and a whole package delete refuses on one project index
// entry that will not parse where it used to delete every link row and the
// package with them.
func TestDeletePackage_LeavesAProjectIndexEntryItCannotRead(t *testing.T) {
	database := openStore(t, t.TempDir())
	pkg, link := seedDeletablePackage(t, database)

	damaged := []byte("[ not ids")
	putRaw(t, database, bucketLinksByProject, itob(link.ProjectID), damaged)

	if err := database.DeletePackage(pkg.ID); err == nil {
		t.Fatal("DeletePackage() reported success for a project index entry it could not read")
	}

	assertNothingDeleted(t, database, pkg, link)
	err := database.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketLinks).Get(itob(link.ID)) == nil {
			t.Error("DeletePackage() removed a link row after refusing to scrub the project index that names it")
		}
		if after := tx.Bucket(bucketLinksByProject).Get(itob(link.ProjectID)); !bytes.Equal(after, damaged) {
			t.Errorf("DeletePackage() left the project index as %q, want the damaged entry %q it could not read", after, damaged)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to read the store back: %v", err)
	}
}

// TestInsertLink_LeavesAPackageIndexEntryItCannotRead discharges the audit on
// deleteLinkRowTx, whose one caller outside tests is InsertLink.
//
// The row it deletes is the one a project holds on another version of the same
// package name, so the path is `lnpm add pkg@<other version>` in a project
// already on one - the case the tag feature exists for. An unreadable
// links_by_package entry on the version being left behind now refuses that add
// outright.
//
// It is the by-package scrub this reaches, and only that one. deleteLinkRowTx
// scrubs links_by_project too, but InsertLink reads that index through indexIDs
// before the walk that reaches this delete, so damage there refuses earlier and
// says so with a different error.
func TestInsertLink_LeavesAPackageIndexEntryItCannotRead(t *testing.T) {
	database := openStore(t, t.TempDir())

	v1 := insertVersion(t, database, "moving-pkg", "h1")
	v2 := insertVersion(t, database, "moving-pkg", "h2")
	proj := seedProject(t, database)

	first := &Link{PackageID: v1.ID, ProjectID: proj.ID, LinkType: "hardlink"}
	if err := database.InsertLink(first); err != nil {
		t.Fatalf("Failed to insert the project's first link: %v", err)
	}

	damaged := []byte("[ not ids")
	putRaw(t, database, bucketLinksByPackage, itob(v1.ID), damaged)

	if err := database.InsertLink(&Link{PackageID: v2.ID, ProjectID: proj.ID, LinkType: "hardlink"}); err == nil {
		t.Fatal("InsertLink() reported success for a package index entry it could not read")
	}

	err := database.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketLinks).Get(itob(first.ID)) == nil {
			t.Errorf("InsertLink() removed link row %d, the row it was superseding, after refusing the insert", first.ID)
		}
		if after := tx.Bucket(bucketLinksByPackage).Get(itob(v1.ID)); !bytes.Equal(after, damaged) {
			t.Errorf("InsertLink() left the package index as %q, want the damaged entry %q it could not read", after, damaged)
		}
		// The project index is what says no new row was written either: it is
		// appended to at the end of the insert, so a committed insert would
		// leave two IDs here.
		var ids []int64
		if err := json.Unmarshal(tx.Bucket(bucketLinksByProject).Get(itob(proj.ID)), &ids); err != nil {
			t.Errorf("the project index no longer reads back as a list of IDs: %v", err)
			return nil
		}
		if len(ids) != 1 || ids[0] != first.ID {
			t.Errorf("the project index holds %v after a refused insert, want only link %d, the row it already had", ids, first.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to read the store back: %v", err)
	}
}

// --- Unreadable project records ----------------------------------------------

// seedProject records one project and returns it with its assigned ID.
func seedProject(t *testing.T, d *DB) *Project {
	t.Helper()

	projectPath := filepath.FromSlash("/projects/consumer")
	if err := d.InsertProject(&Project{Path: projectPath, Name: "consumer"}); err != nil {
		t.Fatalf("Failed to insert the project: %v", err)
	}
	proj, err := d.GetProjectByPath(projectPath)
	if err != nil || proj == nil {
		t.Fatalf("Failed to read the project back: %v", err)
	}
	return proj
}

// TestGetProjectByID_ReturnsNoProjectWhenTheRecordWillNotParse pins the half of
// #292 that lives in this method rather than in gc, on a record that is not
// valid JSON at all.
//
// The lookup used to allocate the project before unmarshalling into it, so a
// record it could not read came back as a non-nil project alongside the error,
// and its callers check the project rather than the error. json.Unmarshal
// validates a document before decoding any of it, so this shape decoded nothing
// and handed back a project whose Path was empty - which gc went on to stat.
// Returning nil with the error follows what #329 settled for
// GetLinksForPackage: a failed read hands back nothing that can be mistaken for
// an answer.
func TestGetProjectByID_ReturnsNoProjectWhenTheRecordWillNotParse(t *testing.T) {
	database := openStore(t, t.TempDir())
	proj := seedProject(t, database)

	putRaw(t, database, bucketProjects, itob(proj.ID), []byte("{ not a project"))

	got, err := database.GetProjectByID(proj.ID)
	if err == nil {
		t.Fatal("GetProjectByID() returned no error for a record it could not read")
	}
	if got != nil {
		t.Errorf("GetProjectByID() returned a project with Path %q alongside its error; a caller reading the project instead of the error cannot tell that apart from a project that is there", got.Path)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", proj.ID)) {
		t.Errorf("GetProjectByID() error = %v, want it to name project %d", err, proj.ID)
	}
}

// TestGetProjectByID_ReturnsNoProjectWhenAValueHasTheWrongType pins the other
// damage shape, which the test above does not reach and which failed in the
// opposite direction.
//
// A document that is valid JSON but holds a value of the wrong type is decoded
// normally up to the mismatch and decoding continues past it, so any prefix of
// the fields can survive - here Path does. Pre-fix that came back as a non-nil
// project carrying a real path, indistinguishable from a healthy record, and gc
// stat'd the directory, found it and swept past. So this shape hid damage rather
// than inventing an orphan, which is why the all-zero case is one shape and not
// the shape. Both end at the same rule: a record that will not read is not a
// project, whatever decoded before the failure.
func TestGetProjectByID_ReturnsNoProjectWhenAValueHasTheWrongType(t *testing.T) {
	database := openStore(t, t.TempDir())
	proj := seedProject(t, database)

	// name takes a string, so this parses as JSON and then fails to decode -
	// after path has already been set.
	putRaw(t, database, bucketProjects, itob(proj.ID),
		fmt.Appendf(nil, `{"id":%d,"path":%q,"name":123}`, proj.ID, proj.Path))

	got, err := database.GetProjectByID(proj.ID)
	if err == nil {
		t.Fatal("GetProjectByID() returned no error for a record whose value has the wrong type")
	}
	if got != nil {
		t.Errorf("GetProjectByID() returned a project with Path %q alongside its error; that path is real, so a caller reading the project cannot tell the record is damaged at all", got.Path)
	}
}

// TestGetProjectByID_ReturnsNothingForAnIDNoRecordAnswers pins the case the one
// above has to stay distinguishable from: an ID with no record is not damage.
// gc classifies a link naming one as orphaned and removes it, so turning this
// into an error would refuse the repair the --fix-links flag exists for.
func TestGetProjectByID_ReturnsNothingForAnIDNoRecordAnswers(t *testing.T) {
	database := openStore(t, t.TempDir())

	proj, err := database.GetProjectByID(4242)
	if err != nil {
		t.Fatalf("GetProjectByID() error = %v for an ID no record answers", err)
	}
	if proj != nil {
		t.Errorf("GetProjectByID() returned a project for an ID no record answers: %+v", proj)
	}
}

// TestGetProjectByPath_ReturnsNoProjectWhenTheRecordWillNotParse is #391: the
// by-path lookup reached that issue still holding the shape #292 had removed
// from the by-ID one. It allocated the project and unmarshalled into it, so a
// record it could not read came back as a non-nil, half-built project alongside
// the error - the reverse of "yields no project", and worse, because a caller
// reading the project rather than the error finds one there.
//
// This is the syntax-error shape: json.Unmarshal validates a document before
// decoding any of it, so nothing is decoded and every field stays zero. The
// project handed back therefore had ID 0, which is not an ID the store ever
// assigns - nextID starts above it - so a caller that went on to use it named no
// record at all.
func TestGetProjectByPath_ReturnsNoProjectWhenTheRecordWillNotParse(t *testing.T) {
	database := openStore(t, t.TempDir())
	proj := seedProject(t, database)

	putRaw(t, database, bucketProjects, itob(proj.ID), []byte("{ not a project"))

	got, err := database.GetProjectByPath(proj.Path)
	if err == nil {
		t.Fatal("GetProjectByPath() returned no error for a record it could not read")
	}
	if got != nil {
		t.Errorf("GetProjectByPath() returned a project with Path %q and ID %d alongside its error; a caller reading the project instead of the error cannot tell that apart from a project that is there", got.Path, got.ID)
	}
	if !strings.Contains(err.Error(), proj.Path) {
		t.Errorf("GetProjectByPath() error = %v, want it to name the path %q it was asked about", err, proj.Path)
	}
}

// TestGetProjectByPath_ReturnsNoProjectWhenAValueHasTheWrongType pins the other
// damage shape, which the test above does not reach.
//
// A document that is valid JSON but gives one of the string fields the wrong
// type loses that field alone: the decoder records the type error and carries on,
// so the rest is populated - here both Path and ID are. (Damage to one of the
// time fields stops the decode instead, which is a different shape;
// GetProjectByPath's doc comment carries the measurement for both.) That made
// this the dangerous shape for this lookup: the half-built project carried a real
// directory and a real ID, so it was indistinguishable from a healthy record, and
// a caller reading it went on to act on a project the store cannot actually read.
func TestGetProjectByPath_ReturnsNoProjectWhenAValueHasTheWrongType(t *testing.T) {
	database := openStore(t, t.TempDir())
	proj := seedProject(t, database)

	// name takes a string, so this parses as JSON and then fails to decode,
	// leaving id and path set. The path is a directory that exists, which is what
	// makes the surviving fields look healthy.
	putRaw(t, database, bucketProjects, itob(proj.ID),
		fmt.Appendf(nil, `{"id":%d,"path":%q,"name":123}`, proj.ID, filepath.ToSlash(t.TempDir())))

	got, err := database.GetProjectByPath(proj.Path)
	if err == nil {
		t.Fatal("GetProjectByPath() returned no error for a record whose value has the wrong type")
	}
	if got != nil {
		t.Errorf("GetProjectByPath() returned a project with Path %q and ID %d alongside its error; that path is a live directory and that ID names a row, so a caller reading the project cannot tell the record is damaged at all", got.Path, got.ID)
	}
}

// TestGetProjectByPath_ReturnsNothingForAPathNoRecordAnswers pins the case the
// two above have to stay distinguishable from: a path with no record is not
// damage. The callers that can reach it handle it - linksOfProject returns no
// links and says in as many words that this is not an error, and remove and
// retreat both check the project before using its ID - so turning it into an
// error would refuse work that has nothing wrong with it. The add and restore
// sites dereference it unchecked, which is safe there for a different reason:
// each registers the project immediately before looking it up.
func TestGetProjectByPath_ReturnsNothingForAPathNoRecordAnswers(t *testing.T) {
	database := openStore(t, t.TempDir())

	proj, err := database.GetProjectByPath(filepath.FromSlash("/projects/never-added"))
	if err != nil {
		t.Fatalf("GetProjectByPath() error = %v for a path no record answers", err)
	}
	if proj != nil {
		t.Errorf("GetProjectByPath() returned a project for a path no record answers: %+v", proj)
	}
}

// insertProjectFor registers a project at path and returns the record the
// database assigned an ID to.
func insertProjectFor(t *testing.T, d *DB, path, name string) *Project {
	t.Helper()

	if err := d.InsertProject(&Project{Path: path, Name: name}); err != nil {
		t.Fatalf("Failed to insert the project at %s: %v", path, err)
	}
	proj, err := d.GetProjectByPath(path)
	if err != nil || proj == nil {
		t.Fatalf("Failed to read the project at %s back: %v", path, err)
	}
	return proj
}

// linkOnPackage returns the one link the project holds, failing when it holds
// none or several - a project holds one row per package name, which is the
// invariant InsertLink is responsible for.
func linkOnPackage(t *testing.T, d *DB, projectID int64) *Link {
	t.Helper()

	links, err := d.GetLinksForProject(projectID)
	if err != nil {
		t.Fatalf("Failed to read the project's links: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("The project holds %d link rows, want exactly one", len(links))
	}
	return links[0]
}

// TestSetTag_LeavesAPinnedLinkOnTheBuildItNames pins ADR-0006's correction to
// #300: a tag move must not carry a pinned link forward, whatever tag the link
// records.
//
// A pin records no tag, so Link.tag() reads it as the default one and the filter
// on the tag alone cannot tell it from an ordinary latest-follower. That matters
// for a pin on the build latest currently names - `lnpm add mylib@1.3.0` while
// 1.3.0 is the current release produces exactly that - because the very next
// publish is a tag move off that record. Carried forward, the pin roots the new
// build instead of the pinned one, the pinned build loses its last reference and
// the next gc collects it: #300's failure arriving through publish rather than
// through pull.
func TestSetTag_LeavesAPinnedLinkOnTheBuildItNames(t *testing.T) {
	database := openStore(t, t.TempDir())

	pinned := insertVersion(t, database, "pin-pkg", "h1")
	next := insertVersion(t, database, "pin-pkg", "h2")

	// Inserting the second version already moved the default tag onto it, so put
	// it back: the case this test is about is a pin on the build the tag is
	// about to move off, which is the only one a tag move can reach at all.
	if err := database.SetTag("pin-pkg", DefaultTag, "h1"); err != nil {
		t.Fatalf("Failed to put the default tag back on the pinned build: %v", err)
	}

	proj := insertProjectFor(t, database, filepath.FromSlash("/projects/pinner"), "pinner")
	if err := database.InsertLink(&Link{PackageID: pinned.ID, ProjectID: proj.ID, LinkType: "hardlink", Pinned: true}); err != nil {
		t.Fatalf("Failed to record the pinned link: %v", err)
	}

	// What a publish of a new build does: move the default tag off the record
	// the project is pinned to.
	if err := database.SetTag("pin-pkg", DefaultTag, "h2"); err != nil {
		t.Fatalf("Failed to move the default tag: %v", err)
	}

	link := linkOnPackage(t, database, proj.ID)
	if link.PackageID != pinned.ID {
		t.Errorf("the tag move carried the pinned link onto package %d, want it left on the pinned %d", link.PackageID, pinned.ID)
	}
	if !link.Pinned {
		t.Errorf("the tag move cleared the pin on a link it was not allowed to move")
	}
	if links, err := database.GetLinksForPackage(next.ID); err != nil {
		t.Fatalf("Failed to read the new build's links: %v", err)
	} else if len(links) != 0 {
		t.Errorf("the new build gained %d link(s) from a pinned consumer", len(links))
	}
}

// TestSetTag_StillCarriesAnUnpinnedLinkForward is the other half of the test
// above: the pin is what holds a link back, not the tag move being weakened.
func TestSetTag_StillCarriesAnUnpinnedLinkForward(t *testing.T) {
	database := openStore(t, t.TempDir())

	first := insertVersion(t, database, "follow-pkg", "h1")
	next := insertVersion(t, database, "follow-pkg", "h2")

	if err := database.SetTag("follow-pkg", DefaultTag, "h1"); err != nil {
		t.Fatalf("Failed to put the default tag back on the first build: %v", err)
	}

	proj := insertProjectFor(t, database, filepath.FromSlash("/projects/follower"), "follower")
	if err := database.InsertLink(&Link{PackageID: first.ID, ProjectID: proj.ID, LinkType: "hardlink"}); err != nil {
		t.Fatalf("Failed to record the link: %v", err)
	}

	if err := database.SetTag("follow-pkg", DefaultTag, "h2"); err != nil {
		t.Fatalf("Failed to move the default tag: %v", err)
	}

	if link := linkOnPackage(t, database, proj.ID); link.PackageID != next.ID {
		t.Errorf("the tag move left an unpinned link on package %d, want it carried onto %d", link.PackageID, next.ID)
	}
}

// TestInsertLink_RecordsAPinOnTheVersionTheProjectAlreadyHolds pins the update
// branch ADR-0006 warns about. When the incoming link names the record the
// project is already on, InsertLink updates that row in place and copies only
// the fields it is told to copy. `lnpm add mylib@<hash-of-current-build>` in a
// project already on that build takes exactly that branch, so a pin that is not
// copied there is reported as recorded and is not.
func TestInsertLink_RecordsAPinOnTheVersionTheProjectAlreadyHolds(t *testing.T) {
	database := openStore(t, t.TempDir())

	pkg := insertVersion(t, database, "repin-pkg", "h1")
	proj := insertProjectFor(t, database, filepath.FromSlash("/projects/repinner"), "repinner")

	if err := database.InsertLink(&Link{PackageID: pkg.ID, ProjectID: proj.ID, LinkType: "hardlink"}); err != nil {
		t.Fatalf("Failed to record the link: %v", err)
	}
	if err := database.InsertLink(&Link{PackageID: pkg.ID, ProjectID: proj.ID, LinkType: "hardlink", Pinned: true}); err != nil {
		t.Fatalf("Failed to record the pinned link: %v", err)
	}

	if link := linkOnPackage(t, database, proj.ID); !link.Pinned {
		t.Errorf("InsertLink left the row unpinned after an add that pins the build the project was already on")
	}
}

// TestInsertLink_ClearsAPinOnTheVersionTheProjectAlreadyHolds is the unpin, and
// the same branch again. `lnpm add mylib` with no build identifier is what
// unpins, and the moment a user most wants it is while the pinned build is still
// the current record - which is the case that reaches the in-place update. A
// branch that leaves the old value alone reports success and changes nothing.
func TestInsertLink_ClearsAPinOnTheVersionTheProjectAlreadyHolds(t *testing.T) {
	database := openStore(t, t.TempDir())

	pkg := insertVersion(t, database, "unpin-pkg", "h1")
	proj := insertProjectFor(t, database, filepath.FromSlash("/projects/unpinner"), "unpinner")

	if err := database.InsertLink(&Link{PackageID: pkg.ID, ProjectID: proj.ID, LinkType: "hardlink", Pinned: true}); err != nil {
		t.Fatalf("Failed to record the pinned link: %v", err)
	}
	if err := database.InsertLink(&Link{PackageID: pkg.ID, ProjectID: proj.ID, LinkType: "hardlink"}); err != nil {
		t.Fatalf("Failed to record the unpinned link: %v", err)
	}

	if link := linkOnPackage(t, database, proj.ID); link.Pinned {
		t.Errorf("InsertLink kept the pin after an add that unpins the build the project was already on")
	}
}
