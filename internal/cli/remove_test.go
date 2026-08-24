package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRunRemoveReportsALinkItCouldNotDelete pins remove's half of #392's caller
// audit.
//
// The delete used to report nothing whatever damage it met, so remove had
// nothing to discard. Now that an index entry it cannot read refuses the
// delete, the discard would print "Removed my-package" over a store that still
// records this project as a consumer of it - and that record is what push and
// publish --push read to decide where a new version goes.
//
// remove does not fail the package over it. By the time the delete runs the
// package is unlinked, package.json is restored and the lock entry is gone, so
// the removal the user asked for has happened; counting it in the failure total
// would exit non-zero saying the package failed to remove, and a re-run cannot
// retry it - the lock file no longer holds it, so remove refuses the name
// outright. What is refused here is silence, which is the same line gc's
// removeOrphanedLinks holds on this delete.
//
// The fixture damages links_by_package rather than links_by_project, because
// linksOfProject reads the project index before this loop and would refuse
// there; the assertion below that remove got as far as unlinking the package is
// what says the delete was reached at all.
func TestRunRemoveReportsALinkItCouldNotDelete(t *testing.T) {
	project, _ := newRetreatProject(t)
	writeRetreatLock(t, project, map[string]string{"my-package": "^1.0.0"})
	pkgID := seedLinkedPackage(t, "my-package")

	damageDatabase(t, "links_by_package", linkKey(pkgID), []byte("[ not ids"))

	// remove always runs the project's package manager once it is done. An
	// empty PATH leaves the shell itself unresolvable, so the run fails
	// immediately and locally instead of installing anything.
	t.Setenv("PATH", t.TempDir())

	var err error
	out := captureStdout(t, func() { err = RunRemove("my-package", false, true) })

	if !strings.Contains(out, "my-package") || !strings.Contains(out, "will not parse") {
		t.Errorf("remove said nothing about the link record it could not delete, output was:\n%s", out)
	}
	// Reached the delete rather than refusing earlier: the package really was
	// unlinked and package.json really was restored.
	if !strings.Contains(out, "Removed my-package") {
		t.Errorf("remove did not report the removal it did perform, output was:\n%s", out)
	}
	if err != nil {
		t.Errorf("RunRemove() = %v after a store row it could not delete, want nil: the files, package.json and lnpm.lock were all removed", err)
	}
	pkgJSON := readFileString(t, filepath.Join(project, "package.json"))
	if !strings.Contains(pkgJSON, `"my-package":"^1.0.0"`) {
		t.Errorf("remove did not restore my-package's original version, package.json is now:\n%s", pkgJSON)
	}
}
