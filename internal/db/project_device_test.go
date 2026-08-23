package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/fsutil"
)

// TestInsertProjectRecordsTheDeviceOfThePath pins the write half of the #335
// fix. gc can only tell "this project directory was deleted" from "the
// filesystem holding it is not mounted right now" if something recorded which
// filesystem that was while the project was reachable, and this is where that
// happens.
//
// It is recorded in InsertProject rather than at the four call sites that build
// a Project, so no future caller can add a fifth and silently opt out. The
// function already resolves the path against the filesystem via normalizePath,
// so consulting the filesystem here is not new behaviour for it.
func TestInsertProjectRecordsTheDeviceOfThePath(t *testing.T) {
	database := openStore(t, t.TempDir())

	project := normalizePath(t.TempDir())
	proj := &Project{Path: project, Name: "consumer"}
	if err := database.InsertProject(proj); err != nil {
		t.Fatalf("InsertProject: %v", err)
	}

	want := fsutil.DeviceIDOfPath(proj.Path)
	if want == 0 {
		t.Skip("this platform reports no device for an existing directory")
	}
	if proj.Device != want {
		t.Errorf("InsertProject left Device = %d on the struct, want %d", proj.Device, want)
	}

	stored, err := database.GetProjectByPath(project)
	if err != nil || stored == nil {
		t.Fatalf("GetProjectByPath = %v, %v", stored, err)
	}
	if stored.Device != want {
		t.Errorf("the stored record has Device = %d, want %d", stored.Device, want)
	}
}

// TestInsertProjectRefreshesTheDeviceOnUpdate covers the branch that is easy to
// miss: InsertProject upserts, and its update path copies named fields onto the
// record it found rather than replacing it wholesale. A field added to the
// struct and not to that list is written once, on first insert, and never
// refreshed - which for this field means a project whose filesystem was
// remounted keeps a stale device forever, and gc declines to collect it for good.
//
// The stale value here is a fabricated one rather than a real remount, because
// no test can remount a filesystem on the CI runners this has to pass on. What
// it establishes is that the update branch writes the field at all.
func TestInsertProjectRefreshesTheDeviceOnUpdate(t *testing.T) {
	database := openStore(t, t.TempDir())

	project := normalizePath(t.TempDir())
	proj := &Project{Path: project, Name: "consumer"}
	if err := database.InsertProject(proj); err != nil {
		t.Fatalf("InsertProject: %v", err)
	}
	real := proj.Device
	if real == 0 {
		t.Skip("this platform reports no device for an existing directory")
	}

	// Write a device that is not the one this path is on, standing in for a
	// record made before a remount.
	stale := real + 1
	if err := database.SetProjectDevice(proj.ID, stale); err != nil {
		t.Fatalf("seed a stale device: %v", err)
	}

	// A second add into the same project takes the update branch.
	again := &Project{Path: project, Name: "consumer", PackageManager: "npm"}
	if err := database.InsertProject(again); err != nil {
		t.Fatalf("InsertProject (update): %v", err)
	}
	if again.ID != proj.ID {
		t.Fatalf("the second insert created project %d rather than updating %d: this test never reached the update branch", again.ID, proj.ID)
	}

	stored, err := database.GetProjectByPath(project)
	if err != nil || stored == nil {
		t.Fatalf("GetProjectByPath = %v, %v", stored, err)
	}
	if stored.Device == stale {
		t.Errorf("the update branch left the stale device %d in place; a remounted project would never be collectable again", stale)
	}
	if stored.Device != real {
		t.Errorf("the update branch wrote Device = %d, want the path's real device %d", stored.Device, real)
	}
}

// TestInsertProjectKeepsARecordedDeviceWhenThePathIsGone pins the direction of
// the refresh. A project directory that will not stat yields no device, and
// overwriting a good recorded value with that nothing would throw away the only
// evidence gc has - turning a protected project back into a collectable one at
// exactly the moment it became unreachable.
//
// The path is normalised before it is used, through the same function
// InsertProject applies, and that is what makes the test exercise the branch it
// names. normalizePath resolves symlinks only while the path exists; once the
// directory is removed it falls back to Clean. On macOS a temp path arrives as
// /var/... and is stored as /private/var/..., and on Windows it arrives as an
// 8.3 short name and is stored expanded - so on both, a second insert of the
// raw spelling after the removal hashes to a different key, misses the by-path
// index, and takes the INSERT branch. It then writes a second record with no
// device and the test fails while the guard it is aiming at was never reached.
// Confirmed on Linux by standing a symlink in for that divergence: the second
// insert came back with a new ID and Device 0. Handing in an already-normalised
// path makes Clean a no-op, so both inserts agree and the update branch runs.
//
// The ID assertion below is what stops that regressing into a silent pass.
func TestInsertProjectKeepsARecordedDeviceWhenThePathIsGone(t *testing.T) {
	database := openStore(t, t.TempDir())

	parent := t.TempDir()
	project := filepath.Join(parent, "myproject")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	project = normalizePath(project)
	proj := &Project{Path: project, Name: "consumer"}
	if err := database.InsertProject(proj); err != nil {
		t.Fatalf("InsertProject: %v", err)
	}
	recorded := proj.Device
	if recorded == 0 {
		t.Skip("this platform reports no device for an existing directory")
	}

	if err := os.RemoveAll(project); err != nil {
		t.Fatalf("remove the project: %v", err)
	}
	again := &Project{Path: project, Name: "consumer"}
	if err := database.InsertProject(again); err != nil {
		t.Fatalf("InsertProject (update over a missing path): %v", err)
	}
	if again.ID != proj.ID {
		t.Fatalf("the second insert created project %d rather than updating %d: the by-path lookup missed, so this test never reached the device guard it exists to check", again.ID, proj.ID)
	}

	stored, err := database.GetProjectByPath(project)
	if err != nil || stored == nil {
		t.Fatalf("GetProjectByPath = %v, %v", stored, err)
	}
	if stored.Device != recorded {
		t.Errorf("the update zeroed a recorded device to %d when the path stopped existing, want %d kept", stored.Device, recorded)
	}
}

// TestProjectRecordWithNoDeviceStillReads pins that a record written before this
// field existed is readable, and reads back as an unknown device rather than
// failing. That is what makes the field additive: bolt holds these as plain
// JSON and nothing in this package decodes with DisallowUnknownFields, so no
// schemaVersion bump is involved and an older lnpm reading a newer record
// ignores the field.
func TestProjectRecordWithNoDeviceStillReads(t *testing.T) {
	database := openStore(t, t.TempDir())

	project := normalizePath(t.TempDir())
	proj := &Project{Path: project, Name: "consumer"}
	if err := database.InsertProject(proj); err != nil {
		t.Fatalf("InsertProject: %v", err)
	}
	if err := database.SetProjectDevice(proj.ID, 0); err != nil {
		t.Fatalf("clear the device: %v", err)
	}

	stored, err := database.GetProjectByID(proj.ID)
	if err != nil || stored == nil {
		t.Fatalf("GetProjectByID = %v, %v", stored, err)
	}
	if stored.Device != 0 {
		t.Errorf("a record with no device read back as %d, want 0", stored.Device)
	}
	if stored.Path != proj.Path {
		t.Errorf("the rest of the record did not survive: %+v", stored)
	}
}
