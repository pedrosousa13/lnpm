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
