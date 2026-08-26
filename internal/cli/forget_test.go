package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/db"
)

// TestForgetDropsTheProjectSoGCCanCollect is acceptance criterion 1 and the
// whole point of the command: a drive that is gone for good leaves gc declining
// its links forever, and forget is the only thing that turns that space back
// into something gc will judge.
//
// The criterion says "the project record and its links", and the links half
// needs its own assertion rather than being read off the collection at the end.
// A DeleteProject that dropped the row alone and left every link in place still
// reaches an empty store here: gc walks packages, and a link whose project row
// is gone is not a consumer it can see, so the version is collected anyway and
// the whole test stays green while half the criterion is unmet. The link rows
// are therefore checked directly, in the window between forget and gc.
func TestForgetDropsTheProjectSoGCCanCollect(t *testing.T) {
	storeRoot, database := newGCStore(t)
	project, pkg := seedUnreachableProject(t, database, storeRoot, "offline-pkg")

	proj, err := database.GetProjectByPath(project)
	if err != nil || proj == nil {
		t.Fatalf("GetProjectByPath = %v, %v", proj, err)
	}

	// Prove the fixture is the one this command exists for: gc declines to
	// judge the link before forget runs, so the collection below is forget's
	// doing and not something gc would have done anyway.
	before := captureStdout(t, func() {
		if err := RunGC(false, "", true, true); err != nil {
			t.Fatalf("RunGC() before forget: %v", err)
		}
	})
	if names := packageNames(t, database); len(names) != 1 {
		t.Fatalf("the fixture did not reach gc as a skipped link; packages left: %v, output:\n%s", names, before)
	}

	out := captureStdout(t, func() {
		if err := RunForget(project, true); err != nil {
			t.Fatalf("RunForget() error = %v", err)
		}
	})
	t.Logf("forget output:\n%s", out)

	// The record and its links, which is the whole of the criterion. The
	// by-package read is the load-bearing one: it is what gc scans, and a link
	// left there naming a project that no longer exists is the state #382 exists
	// to clear rather than move.
	if stored, err := database.GetProjectByID(proj.ID); err != nil || stored != nil {
		t.Errorf("forget left the project record behind: %v, %v", stored, err)
	}
	if links, err := database.GetLinksForProject(proj.ID); err != nil || len(links) != 0 {
		t.Errorf("forget left %d of the project's link(s) behind (err %v); the criterion is the record and its links", len(links), err)
	}
	if links, err := database.GetLinksForPackage(pkg.ID); err != nil || len(links) != 0 {
		t.Errorf("forget left %d link(s) naming the package in links_by_package (err %v), so gc still scans a consumer that is gone", len(links), err)
	}

	// forget drops the record and stops there: the store entry is gc's to
	// remove, behind gc's own confirmation.
	if _, err := os.Stat(pkg.StorePath); err != nil {
		t.Errorf("forget removed the store entry %s itself: %v", pkg.StorePath, err)
	}
	// Which makes naming gc part of the job. Reclaiming the space is two
	// deliberate commands, and a user who has just been told the project is
	// forgotten has no other way to learn the second one is still owed.
	if !strings.Contains(out, "lnpm gc") {
		t.Errorf("forget did not name the command that reclaims the space; output was:\n%s", out)
	}

	captureStdout(t, func() {
		if err := RunGC(false, "", false, true); err != nil {
			t.Fatalf("RunGC() after forget: %v", err)
		}
	})

	if names := packageNames(t, database); len(names) != 0 {
		t.Errorf("gc did not collect a package whose only consumer was forgotten; packages left: %v", names)
	}
	if _, err := os.Stat(pkg.StorePath); !os.IsNotExist(err) {
		t.Errorf("gc left the store entry %s behind, stat error = %v", pkg.StorePath, err)
	}
}

// TestForgetRefusesAProjectThatIsStillThere is acceptance criterion 2. Forgetting
// a live project is a different operation - it would leave the directory with its
// .lnpm contents and no record of them - and must not be reachable by accident,
// least of all by a user who typed the wrong path.
func TestForgetRefusesAProjectThatIsStillThere(t *testing.T) {
	storeRoot, database := newGCStore(t)

	project := resolvedTempDir(t)
	proj, _ := seedProjectAndPackage(t, database, storeRoot, project, "live-pkg")

	var err error
	out := captureStdout(t, func() {
		err = RunForget(project, true)
	})
	t.Logf("forget output:\n%s", out)

	if err == nil {
		t.Fatalf("forget removed the record of a project that is still on disk")
	}
	if !strings.Contains(err.Error(), project) {
		t.Errorf("the refusal did not name the path it refused: %v", err)
	}

	stored, lookupErr := database.GetProjectByID(proj.ID)
	if lookupErr != nil || stored == nil {
		t.Fatalf("forget deleted a live project's record anyway: %v, %v", stored, lookupErr)
	}
}

// TestForgetFailsOnAPathItNeverRegistered is acceptance criterion 6. A silent
// success here is the worst answer available: the user believes the space is now
// gc's to reclaim, gc reclaims nothing, and the reason - a typo in the path - is
// nowhere in either command's output.
func TestForgetFailsOnAPathItNeverRegistered(t *testing.T) {
	newGCStore(t)

	unknown := filepath.Join(t.TempDir(), "never-registered")

	var err error
	out := captureStdout(t, func() {
		err = RunForget(unknown, true)
	})
	t.Logf("forget output:\n%s", out)

	if err == nil {
		t.Fatalf("forget reported success for a path lnpm has no record of")
	}
	if !strings.Contains(err.Error(), unknown) {
		t.Errorf("the failure did not name the path it could not find: %v", err)
	}
}

// TestForgetAbortsWithoutConfirmation is acceptance criterion 3. A test is a
// non-interactive session, so this is the shape a script or a CI job meets: with
// no --yes there is nobody to ask, and the record has to survive rather than be
// destroyed on the strength of nobody objecting.
func TestForgetAbortsWithoutConfirmation(t *testing.T) {
	storeRoot, database := newGCStore(t)
	project, _ := seedUnreachableProject(t, database, storeRoot, "offline-pkg")

	proj, err := database.GetProjectByPath(project)
	if err != nil || proj == nil {
		t.Fatalf("GetProjectByPath = %v, %v", proj, err)
	}

	out := captureStdout(t, func() {
		if err := RunForget(project, false); err != nil {
			t.Errorf("RunForget() error = %v", err)
		}
	})
	t.Logf("forget output:\n%s", out)

	if !strings.Contains(out, "re-run with --yes") {
		t.Errorf("forget did not say how to proceed non-interactively; output was:\n%s", out)
	}

	stored, err := database.GetProjectByID(proj.ID)
	if err != nil || stored == nil {
		t.Fatalf("forget dropped the record without a confirmation: %v, %v", stored, err)
	}
	links, err := database.GetLinksForProject(proj.ID)
	if err != nil {
		t.Fatalf("GetLinksForProject: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("forget removed %d link(s) without a confirmation, want 0 removed", 1-len(links))
	}
}

// TestForgetLeavesEntriesAnotherProjectStillUses is acceptance criterion 5, and
// it is written first among the things this command must NOT do because it is the
// one most easily assumed rather than proven.
//
// forget deletes a project's own link rows and nothing else, so a version another
// project still consumes goes on being reached by that project's link and stays
// out of gc's reach. Nothing in forget knows this - the property falls out of
// deleting only what the project holds - which is exactly why it needs a test:
// the failure mode is a future change to DeleteProject scrubbing an index by key
// rather than by ID, and no other test in this package would notice.
func TestForgetLeavesEntriesAnotherProjectStillUses(t *testing.T) {
	storeRoot, database := newGCStore(t)
	forgotten, pkg := seedUnreachableProject(t, database, storeRoot, "shared-pkg")

	// A second, live consumer of the very same store entry.
	keeper := resolvedTempDir(t)
	keeperProj := &db.Project{Path: keeper, Name: "keeper"}
	if err := database.InsertProject(keeperProj); err != nil {
		t.Fatalf("insert the second project: %v", err)
	}
	if err := database.InsertLink(&db.Link{PackageID: pkg.ID, ProjectID: keeperProj.ID, LinkType: "hardlink"}); err != nil {
		t.Fatalf("link the second project: %v", err)
	}

	captureStdout(t, func() {
		if err := RunForget(forgotten, true); err != nil {
			t.Fatalf("RunForget() error = %v", err)
		}
	})

	// The keeper's link is what has to have survived, and it has to have
	// survived as something gc can read: a link row left behind but scrubbed
	// out of links_by_package would be invisible to gc's scan and the version
	// would be collected anyway.
	links, err := database.GetLinksForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("GetLinksForPackage after forget: %v", err)
	}
	if len(links) != 1 || links[0].ProjectID != keeperProj.ID {
		t.Fatalf("forget did not leave exactly the keeper's link behind; links = %+v", links)
	}

	out := captureStdout(t, func() {
		if err := RunGC(false, "", true, true); err != nil {
			t.Fatalf("RunGC() after forget: %v", err)
		}
	})
	t.Logf("gc output:\n%s", out)

	if names := packageNames(t, database); len(names) != 1 {
		t.Errorf("gc collected a version another project still consumes; packages left: %v", names)
	}
	if _, err := os.Stat(pkg.StorePath); err != nil {
		t.Errorf("gc removed the store entry %s that the keeper still consumes: %v", pkg.StorePath, err)
	}
}

// TestGCPointsAtForgetForTheLinksItSkipped is acceptance criterion 4. The skip
// report used to end without a remedy, and said so at length, because none
// existed. It does now, and a destructive command that declines to act while
// naming nothing the user can do about it is how the space leak gc trades for
// safety goes unnoticed for good.
func TestGCPointsAtForgetForTheLinksItSkipped(t *testing.T) {
	storeRoot, database := newGCStore(t)
	project, _ := seedUnreachableProject(t, database, storeRoot, "offline-pkg")

	out := captureStdout(t, func() {
		if err := RunGC(false, "", false, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})
	t.Logf("gc output:\n%s", out)

	if !strings.Contains(out, "lnpm forget") {
		t.Errorf("gc reported a skipped link without naming the command that reclaims it; output was:\n%s", out)
	}
	// The kept-links line carries the reason the report exists and must survive
	// the remedy being added next to it.
	if !strings.Contains(out, "These links were kept") {
		t.Errorf("gc dropped the line saying why nothing was collected; output was:\n%s", out)
	}
	if !strings.Contains(out, project) {
		t.Errorf("gc did not name the project the remedy applies to; output was:\n%s", out)
	}
}

// TestForgetResolvesARelativePath pins that the argument is made absolute before
// it is looked up.
//
// The lookup normalises through EvalSymlinks, which only resolves a path that
// exists, and a project worth forgetting is by definition one that does not - so
// a relative argument falls through to filepath.Clean and stays relative, matches
// no by-path entry, and forget reports "no record" for a project it holds a
// record of. That is criterion 6's failure mode fired at the one input a user is
// most likely to type: they are standing in the mount point looking at the
// directory that is no longer there.
func TestForgetResolvesARelativePath(t *testing.T) {
	storeRoot, database := newGCStore(t)
	project, _ := seedUnreachableProject(t, database, storeRoot, "offline-pkg")

	proj, err := database.GetProjectByPath(project)
	if err != nil || proj == nil {
		t.Fatalf("GetProjectByPath = %v, %v", proj, err)
	}

	// The mount point survives the unmount, so it is somewhere a user can stand.
	t.Chdir(filepath.Dir(project))

	out := captureStdout(t, func() {
		if err := RunForget(filepath.Base(project), true); err != nil {
			t.Errorf("RunForget(%q) error = %v", filepath.Base(project), err)
		}
	})
	t.Logf("forget output:\n%s", out)

	stored, err := database.GetProjectByID(proj.ID)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}
	if stored != nil {
		t.Errorf("forget did not match the record when given a relative path; the project is still registered at %s", stored.Path)
	}
}

// TestForgetCommandIsRegistered pins the wiring. Every other test in this file
// calls RunForget directly, so all of them would pass with the cobra command
// never added to rootCmd - and 'lnpm forget' would be an unknown command while
// gc's report told users to run it.
func TestForgetCommandIsRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"forget"})
	if err != nil {
		t.Fatalf("rootCmd.Find(forget): %v; the command gc's report names is not registered", err)
	}
	if cmd == nil || cmd.Name() != "forget" {
		t.Fatalf("rootCmd.Find(forget) resolved to %q, not the forget command", cmd.Name())
	}
	if cmd.Flags().ShorthandLookup("y") == nil {
		t.Errorf("forget has no -y shorthand; gc and remove both spell --yes that way")
	}
	if cmd.Flags().Lookup("yes") == nil {
		t.Errorf("forget has no --yes flag, so it cannot be run non-interactively")
	}
}
