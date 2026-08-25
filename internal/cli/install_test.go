package cli

import "testing"

// recordInstalls replaces runProjectInstallFn for the rest of the test with one
// that records the directory it is handed and installs nothing, so a test can
// check what a command decided about installing without a real package manager
// starting. The returned function reports the recorded directories, in order.
//
// Don't use t.Parallel() in callers - this helper swaps the process-wide
// runProjectInstallFn var, so a caller must also not run alongside a test that
// does.
func recordInstalls(t *testing.T) func() []string {
	t.Helper()

	var ran []string
	prev := runProjectInstallFn
	runProjectInstallFn = func(dir string) { ran = append(ran, dir) }
	t.Cleanup(func() { runProjectInstallFn = prev })

	return func() []string { return ran }
}

// TestRunRetreatInstallsOnlyWhenAsked pins what --install decides, at the seam
// rather than at stdout.
//
// Why not read the output. runProjectInstall prints "Running %s..." before the
// command starts, so stdout cannot tell an install that was suppressed from one
// that was merely not announced: a change that gated the announcement alone
// would pass every output-based assertion while every transitive dependency's
// install scripts still ran. Counting calls to runProjectInstallFn is what tells
// the two apart, and that was measured rather than argued - a build gating the
// announcement and the failure warning while running the install unconditionally
// printed output byte-identical to the correct build's in both flag states, and
// turned this test's second row red.
//
// Each row answers for a different revert, so neither is spare. The second row
// is the one that moves when the install stops going through the seam at all.
// The first only moves when the seam is called for a retreat that did not ask
// for an install - measured by calling runProjectInstallFn unconditionally,
// which turns that row red and leaves the second green.
//
// Don't add t.Parallel() here: recordInstalls swaps a process-wide var.
func TestRunRetreatInstallsOnlyWhenAsked(t *testing.T) {
	tests := []struct {
		name         string
		runInstall   bool
		wantInstalls int
	}{
		{"without the flag retreat installs nothing", false, 0},
		{"with the flag retreat installs once", true, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project, _ := newRetreatProject(t)
			writeRetreatLock(t, project, map[string]string{"good-pkg": "^1.0.0"})

			installs := recordInstalls(t)

			// Belt and braces for the revert check this test exists for.
			// shellcmd.Command runs the install through a shell - sh, here -
			// and exec.Command looks that up on PATH, so a PATH holding one
			// empty directory leaves the shell itself unresolvable. A build
			// that puts the real command back at the call site then fails
			// immediately and locally, rather than installing this fixture's
			// dependencies for real. Run and confirmed on Linux: the captured
			// output carries `! Install failed: exec: "sh": executable file not
			// found in $PATH`. Nothing runs when the seam is in place, so this
			// only ever matters to the experiment.
			t.Setenv("PATH", t.TempDir())

			var err error
			out := captureStdout(t, func() { err = RunRetreat(true, tt.runInstall) })

			if err != nil {
				t.Fatalf("RunRetreat(force=true, install=%v) = %v, want nil; output was:\n%s", tt.runInstall, err, out)
			}
			if got := installs(); len(got) != tt.wantInstalls {
				t.Errorf("retreat ran %d install(s) %v with install=%v, want %d; output was:\n%s",
					len(got), got, tt.runInstall, tt.wantInstalls, out)
			}
		})
	}
}
