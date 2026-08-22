package db

import (
	"encoding/json"
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
		}{
			{tx.Bucket(bucketLinksByPackage), itob(link.PackageID)},
			{tx.Bucket(bucketLinksByProject), itob(link.ProjectID)},
		} {
			ids, err := indexIDs(index.bucket, index.key)
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

	projectPath := filepath.FromSlash("/projects/consumer")
	if err := d.InsertProject(&Project{Path: projectPath, Name: "consumer"}); err != nil {
		t.Fatalf("Failed to insert the project: %v", err)
	}
	proj, err := d.GetProjectByPath(projectPath)
	if err != nil || proj == nil {
		t.Fatalf("Failed to read the project back: %v", err)
	}

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
