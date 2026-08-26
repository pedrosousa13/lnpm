package tests

import (
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestDoctorHealthyStore is the integration counterpart to the unit tests in
// internal/cli: doctor run over a real published-and-linked store passes every
// check. setupTest points LNPM_STORE at a temp dir, and config.GetStorePath
// consults that variable before the config file or the home directory, so a
// real ~/.lnpm on the machine running the test is never reached.
func TestDoctorHealthyStore(t *testing.T) {
	env := setupTest(t)
	env.publishAndAdd("doctor-pkg")

	// Content verification is on, so that "healthy" here means every check ran
	// and passed. A default run leaves Check 6 out, and the summary then says so
	// rather than claiming a full pass - which is the honest answer, but not the
	// one this test is about.
	out := captureStdout(t, func() {
		if err := cli.RunDoctor(true); err != nil {
			t.Errorf("RunDoctor() error = %v", err)
		}
	})

	// The "OK" is the marker, not prose: captureStdout replaces stdout with a
	// pipe, so ui.IconOK renders its ASCII fallback. Asserting the whole line
	// keeps a change to where the markers come from - #366 moved them out of
	// internal/cli - from silently altering what doctor prints.
	if !strings.Contains(out, "OK All checks passed!") {
		t.Errorf("RunDoctor did not pass on a healthy store, output was:\n%s", out)
	}
	// The check labels themselves mention orphans, so the assertion keys on the
	// findings' wording ("N orphaned package(s)") rather than the word alone.
	for _, unwanted := range []string{"NOT FOUND", "orphaned package(s)", "orphaned link(s)", "missing files"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("RunDoctor reported %q on a healthy store, output was:\n%s", unwanted, out)
		}
	}
}

// TestDoctorReportsOrphanedPackage pins the warning path: a package that was
// published but never linked is an orphan, and doctor must both count it and
// point at gc, rather than reporting the store healthy.
func TestDoctorReportsOrphanedPackage(t *testing.T) {
	env := setupTest(t)
	env.simplePkg("doctor-orphan")

	out := captureStdout(t, func() {
		if err := cli.RunDoctor(false); err != nil {
			t.Errorf("RunDoctor() error = %v", err)
		}
	})

	// The leading "!" is ui.IconWarn's ASCII fallback, which captureStdout's
	// pipe selects; it is asserted here for the reason TestDoctorHealthyStore
	// gives for "OK".
	if !strings.Contains(out, "! 1 orphaned package(s)") {
		t.Errorf("RunDoctor did not report the orphaned package, output was:\n%s", out)
	}
	if !strings.Contains(out, "lnpm gc") {
		t.Errorf("RunDoctor did not suggest gc for the orphan, output was:\n%s", out)
	}
	if !strings.Contains(out, "! Found 1 warning(s)") {
		t.Errorf("RunDoctor did not summarize the orphan as a warning, output was:\n%s", out)
	}
	if strings.Contains(out, "All checks passed!") {
		t.Errorf("RunDoctor reported all checks passed despite an orphan, output was:\n%s", out)
	}
}
