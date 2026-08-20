package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/db"
)

// newGCStore points lnpm at a fresh store and database, and returns the store
// directory and the open database for the caller to seed.
func newGCStore(t *testing.T) (string, *db.DB) {
	t.Helper()

	base := t.TempDir()
	t.Setenv("LNPM_STORE", base)
	db.ResetForTesting()
	t.Cleanup(db.ResetForTesting)

	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	return filepath.Join(base, "store"), database
}

// TestRunGCReportsEntryRemovalFailure pins that gc stops discarding the error
// from removing a store entry. A removal that fails silently is how a
// partially deleted entry gets left behind while gc claims it cleaned up, and
// the database row it would then have dropped is the only record the entry
// ever existed.
func TestRunGCReportsEntryRemovalFailure(t *testing.T) {
	storeRoot, database := newGCStore(t)

	entry := filepath.Join(storeRoot, "stuck-pkg", "f00d")
	if err := os.MkdirAll(filepath.Join(entry, "dist"), 0755); err != nil {
		t.Fatalf("seed entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry, "dist", "index.js"), []byte("payload"), 0644); err != nil {
		t.Fatalf("seed entry file: %v", err)
	}
	// A non-empty directory in the marker's place, which os.Remove refuses to
	// delete on every platform, so removing this entry fails at its first step.
	// internal/store's blockMarkerRemoval explains why the obstruction takes
	// this shape rather than a permission denial.
	occupant := filepath.Join(entry, ".lnpm-complete", "occupied")
	if err := os.MkdirAll(filepath.Dir(occupant), 0755); err != nil {
		t.Fatalf("block marker removal: %v", err)
	}
	if err := os.WriteFile(occupant, []byte("x"), 0644); err != nil {
		t.Fatalf("block marker removal: %v", err)
	}

	if err := database.InsertPackage(&db.Package{
		Name:        "stuck-pkg",
		Version:     "1.0.0",
		ContentHash: "f00d",
		StorePath:   entry,
	}); err != nil {
		t.Fatalf("insert package: %v", err)
	}

	out := captureStdout(t, func() {
		if err := RunGC(false, "", false, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})

	if !strings.Contains(out, "Failed to remove stuck-pkg") {
		t.Errorf("RunGC did not report the failed removal, output was:\n%s", out)
	}
	if strings.Contains(out, "Removed 1 package(s)") {
		t.Errorf("RunGC claimed it removed a package it could not remove, output was:\n%s", out)
	}

	packages, err := database.ListPackages()
	if err != nil {
		t.Fatalf("list packages: %v", err)
	}
	if len(packages) != 1 {
		t.Errorf("RunGC dropped the database row for an entry it failed to remove, %d package(s) left", len(packages))
	}
}
