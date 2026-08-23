package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/ui"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
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

	// install is false below, so remove starts nothing - this is belt and
	// braces for the revert check the install tests further down exist for. A
	// PATH holding one empty directory leaves the shell itself unresolvable -
	// shellcmd.Command runs through sh, and exec.Command looks sh up on PATH -
	// so a build that installs unconditionally fails immediately and locally
	// instead of installing anything. Run and confirmed: under that build the
	// captured output carries `! Install failed: exec: "sh": executable file
	// not found in $PATH`.
	t.Setenv("PATH", t.TempDir())

	// The assertion below builds its expected marker with ui.IconOK(), which is
	// evaluated after captureStdout has put the real os.Stdout back. Inside the
	// capture stdout is a pipe, so the captured text always holds the ASCII
	// fallback; outside it, a test binary run straight in a terminal has a TTY
	// and ui.IconOK() would answer with the glyph instead, failing the test on
	// nothing. NO_COLOR is checked before stdout is, so it pins both sides to
	// the same answer however the test is run.
	t.Setenv("NO_COLOR", "1")

	var err error
	out := captureStdout(t, func() { err = RunRemove("my-package", false, true, false) })

	if !strings.Contains(out, "my-package") || !strings.Contains(out, "will not parse") {
		t.Errorf("remove said nothing about the link record it could not delete, output was:\n%s", out)
	}
	// Reached the delete rather than refusing earlier: the package really was
	// unlinked and package.json really was restored.
	//
	// The marker is matched, because the warning above prints "Removed
	// my-package, but its link record is still in the store" and so contains
	// the bare phrase. Asserting on the phrase alone could not fail once the
	// first check passed - it would be satisfied by the very line that check
	// was looking for. The success line is "  <ok> Removed my-package", and
	// the warning's is "  <warn> Removed my-package, ...", so the icon is what
	// tells them apart.
	if !strings.Contains(out, ui.IconOK()+" Removed my-package") {
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

// The tests below are about one thing: which invocations of `lnpm remove` start
// a package-manager install. Before #336 every one of them did, so a removal ran
// every dependency's install scripts whether the user asked for that or not.
//
// None of them may start a real install, so all but one go through
// runProjectInstallFn, swapped out by install_test.go's recordInstalls. An
// assertion on stdout alone would not do: runProjectInstall prints "Running
// ..." before the command runs, so a test reading only the output cannot tell a
// suppressed install from an announced one.
//
// The exception is TestRunRemoveReportsAFailedInstall, which is about what the
// real helper does when the install fails and so cannot replace it. Its header
// says how it stays local instead.
//
// recordInstalls records the directory and nothing else, because that is all
// the seam carries - runProjectInstall derives the command from the directory
// itself. Which command a given project gets is answered by
// TestRunRemoveTipNamesTheProjectsInstallCommand below and by
// TestPrintPeerDependencyTipNamesTheProjectsInstallCommand, not here.

// newRemoveProject lays out a project holding one linked package, chdirs into
// it, and returns the working directory as RunRemove will see it.
//
// It returns os.Getwd()'s answer rather than the temp path it built, because
// the two are not the same string everywhere: macOS resolves /var to
// /private/var and Windows expands 8.3 short names, so a test comparing
// RunRemove's cwd against the fixture's own path fails on both.
func newRemoveProject(t *testing.T, packageName string) string {
	t.Helper()

	newDoctorStoreConfig(t)
	db.ResetForTesting()
	t.Cleanup(db.ResetForTesting)

	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(project, ".lnpm", packageName), 0755); err != nil {
		t.Fatalf("create .lnpm: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, "node_modules"), 0755); err != nil {
		t.Fatalf("create node_modules: %v", err)
	}

	writeFile(t, filepath.Join(project, "package.json"),
		`{"name":"remove-project","version":"1.0.0","dependencies":{"`+packageName+`":"file:.lnpm/`+packageName+`"}}`)

	lock := &lockfile.LockFile{Version: 1, Packages: map[string]lockfile.Package{
		packageName: {Version: "1.0.0"},
	}}
	if err := lock.Save(project); err != nil {
		t.Fatalf("save lock file: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd after chdir: %v", err)
	}
	return cwd
}

// addLockEntry adds one more package to the project's lock file, without the
// .lnpm directory newRemoveProject lays down for the package it is given.
func addLockEntry(t *testing.T, project, packageName string) {
	t.Helper()

	lock, err := lockfile.Load(project)
	if err != nil {
		t.Fatalf("load lock file: %v", err)
	}
	lock.Packages[packageName] = lockfile.Package{Version: "1.0.0"}
	if err := lock.Save(project); err != nil {
		t.Fatalf("save lock file: %v", err)
	}
}

// peerDepTip is the guidance a command prints when it did not run an install
// itself, as newRemoveProject's fixture gets it. Spelled out here rather than
// taken from the helper that prints it, so a change to the wording has to be
// made in the test too.
//
// The fixture lays down a lnpm.lock and no package-manager lock file - no
// package-lock.json, pnpm-lock.yaml, yarn.lock or bun.lock* - and those four
// names are the only ones config.DetectPackageManager stats, so it answers npm
// by default and config.GetInstallCommand adds --legacy-peer-deps for
// npm/cli#2199. That flag is why this constant is not the bare 'npm install'
// #384 removed: a remove that spelled the old line out itself would not pass
// here on the fixture's own package manager happening to be npm. Measured on
// 2026-08-27 by putting that literal back at remove's tip site, with go vet
// ./... read as 0 first: the two tests below that assert the tip was printed
// turn red - TestRunRemoveRunsNoInstallWithoutTheFlag and
// TestRunRemoveAdvisesAfterAPartialRemoval - and the two that assert it was
// not stay green, as they must, since the revert changes the wording rather
// than adding a tip where none belonged.
const peerDepTip = "Run 'npm install --legacy-peer-deps' if you need to resolve peer dependencies"

// TestRunRemoveRunsNoInstallWithoutTheFlag is #336's regression: remove used to
// run the package manager on every invocation, executing every dependency's
// install scripts as a side effect of a removal.
//
// Don't add t.Parallel() here: recordInstalls swaps a process-wide var.
func TestRunRemoveRunsNoInstallWithoutTheFlag(t *testing.T) {
	newRemoveProject(t, "remove-pkg")
	ran := recordInstalls(t)

	var err error
	out := captureStdout(t, func() { err = RunRemove("remove-pkg", false, false, false) })

	if err != nil {
		t.Fatalf("RunRemove() = %v, want nil; output was:\n%s", err, out)
	}
	if got := ran(); len(got) != 0 {
		t.Errorf("remove ran an install in %v without --install, want no install at all", got)
	}
	if !strings.Contains(out, peerDepTip) {
		t.Errorf("remove did not print the peer-dependency tip, output was:\n%s", out)
	}
}

// TestRunRemoveRunsTheInstallWithTheFlag covers the other half: --install still
// runs the project's install, in the project directory.
//
// Don't add t.Parallel() here: recordInstalls swaps a process-wide var.
func TestRunRemoveRunsTheInstallWithTheFlag(t *testing.T) {
	cwd := newRemoveProject(t, "remove-pkg")
	ran := recordInstalls(t)

	var err error
	out := captureStdout(t, func() { err = RunRemove("remove-pkg", false, false, true) })

	if err != nil {
		t.Fatalf("RunRemove() = %v, want nil; output was:\n%s", err, out)
	}
	got := ran()
	if len(got) != 1 {
		t.Fatalf("remove ran %d install(s) %v with --install, want exactly one; output was:\n%s", len(got), got, out)
	}
	if got[0] != cwd {
		t.Errorf("remove ran the install in %q, want the project directory %q", got[0], cwd)
	}
	if strings.Contains(out, peerDepTip) {
		t.Errorf("remove printed the peer-dependency tip after running the install, output was:\n%s", out)
	}
}

// TestRunRemoveReportsAFailedInstall pins how a failing install is reported: a
// warning, and a nil return, because the packages were removed either way.
//
// This is the one test in this section that does not call recordInstalls, and
// the reason is the trap that got an earlier draft of it deleted. The warning
// is printed by runProjectInstall itself, at install.go's cmd.Run() branch -
// and runProjectInstallFn is the variable holding that very function, so a fake
// installed over the seam replaces the code under test. RunRemove is handed
// nothing back either: runProjectInstallFn returns no value, so there is no
// second route by which RunRemove could observe the failure. Swap the seam here
// and the test cannot fail for its own reason at all. Don't.
//
// What keeps it local instead is the trick TestRunRemoveReportsALinkItCouldNotDelete
// uses at the top of this file: a PATH holding one empty directory leaves the
// shell itself unresolvable - shellcmd.Command runs through sh, and exec.Command
// looks sh up on PATH - so the real helper runs, fails to start anything at all,
// and reports that. No package manager is reached, so no dependency's install
// scripts run.
//
// Measured on 2026-08-27, go vet ./... read as 0 first so a build failure could
// not pass for a red test: swallowing the warning at install.go's cmd.Run()
// branch, as `_, _ = err, ui.IconWarn()` so the ui import stays used and the
// package still builds, turns this test red and nothing else in the repo -
// internal/cli printed FAIL with a duration rather than [build failed], and
// every other package printed ok. So this is the only thing pinning that
// warning anywhere, at either level.
//
// NO_COLOR is set for the reason the test at the top of this file gives: the
// assertion matches text captured through a pipe, and ui.IconWarn() answers
// differently depending on whether stdout is a TTY.
func TestRunRemoveReportsAFailedInstall(t *testing.T) {
	newRemoveProject(t, "remove-pkg")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("NO_COLOR", "1")

	var err error
	out := captureStdout(t, func() { err = RunRemove("remove-pkg", false, false, true) })

	if err != nil {
		t.Fatalf("RunRemove() = %v after a failed install, want nil - the removal itself succeeded; output was:\n%s", err, out)
	}
	if !strings.Contains(out, ui.IconWarn()+" Install failed:") {
		t.Errorf("remove did not report the failed install, output was:\n%s", out)
	}
	// The tip belongs to the other branch. Reporting a failed install and
	// advising an install the user did ask for are two different sentences, and
	// only one of them was printed here.
	if strings.Contains(out, peerDepTip) {
		t.Errorf("remove printed the peer-dependency tip though --install was passed, output was:\n%s", out)
	}
}

// TestRunRemoveTipNamesTheProjectsInstallCommand pins remove's call site of the
// #384 tip, for the reason tests/add_test.go's two give: the helper's own unit
// test drives printPeerDependencyTip directly and so cannot tell whether any
// command still spells the command out itself.
//
// The project is pnpm's, so a tip still saying npm sends the user to a command
// that rewrites the wrong lock file - and 'pnpm install' is a string the
// hardcoded line #384 removed could not produce under any project, which is why
// the fixture is deliberately not npm's.
//
// The install seam is recorded rather than left alone even though install is
// false, because a build that installs unconditionally would otherwise run a
// real pnpm install against this fixture.
//
// Don't add t.Parallel() here: recordInstalls swaps a process-wide var.
func TestRunRemoveTipNamesTheProjectsInstallCommand(t *testing.T) {
	cwd := newRemoveProject(t, "remove-pkg")
	writeFile(t, filepath.Join(cwd, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n")
	recordInstalls(t)

	var err error
	out := captureStdout(t, func() { err = RunRemove("remove-pkg", false, false, false) })

	if err != nil {
		t.Fatalf("RunRemove() = %v, want nil; output was:\n%s", err, out)
	}
	const want = "Run 'pnpm install' if you need to resolve peer dependencies"
	if !strings.Contains(out, want) {
		t.Errorf("remove's tip = want it to contain %q, output was:\n%s", want, out)
	}
}

// TestRunRemoveThatRemovedNothingNeitherInstallsNorAdvises matches the gate the
// sibling commands use: add runs its install and prints its tip only when
// something was actually added, restore only when something was restored. A run
// where every package failed has nothing for an install to restore and nothing
// for the tip to advise on.
//
// The failure is forced by making .lnpm a regular file, which the linker refuses
// to work through - it is the cheapest way to fail every package after the lock
// file has already been read.
//
// Don't add t.Parallel() here: recordInstalls swaps a process-wide var.
func TestRunRemoveThatRemovedNothingNeitherInstallsNorAdvises(t *testing.T) {
	for _, install := range []bool{false, true} {
		name := "without the flag"
		if install {
			name = "with the flag"
		}

		t.Run(name, func(t *testing.T) {
			cwd := newRemoveProject(t, "remove-pkg")
			if err := os.RemoveAll(filepath.Join(cwd, ".lnpm")); err != nil {
				t.Fatalf("clear .lnpm: %v", err)
			}
			writeFile(t, filepath.Join(cwd, ".lnpm"), "not a directory")
			ran := recordInstalls(t)

			var err error
			out := captureStdout(t, func() { err = RunRemove("remove-pkg", false, false, install) })

			if err == nil {
				t.Fatalf("RunRemove() = nil, want an error saying the package failed to remove; output was:\n%s", out)
			}
			if got := ran(); len(got) != 0 {
				t.Errorf("remove ran an install in %v after removing nothing, want no install at all", got)
			}
			if strings.Contains(out, peerDepTip) {
				t.Errorf("remove printed the peer-dependency tip after removing nothing, output was:\n%s", out)
			}
		})
	}
}

// TestRunRemoveAdvisesAfterAPartialRemoval fixes which question the gate above
// asks. "Did anything survive" and "did everything succeed" agree on a run where
// every package failed, so the test above passes under either - and the second
// is wrong, silently dropping the advice from a run that did remove something.
//
// The escaping lock key is refused by the linker's name validation, so one
// package fails and one is removed.
//
// Don't add t.Parallel() here: recordInstalls swaps a process-wide var.
func TestRunRemoveAdvisesAfterAPartialRemoval(t *testing.T) {
	cwd := newRemoveProject(t, "remove-pkg")
	addLockEntry(t, cwd, "../escapee")
	ran := recordInstalls(t)

	var err error
	out := captureStdout(t, func() { err = RunRemove("", true, true, false) })

	if err == nil {
		t.Fatalf("RunRemove() = nil, want an error naming the package it could not remove; output was:\n%s", out)
	}
	if got := ran(); len(got) != 0 {
		t.Errorf("remove ran an install in %v without --install, want no install at all", got)
	}
	if !strings.Contains(out, peerDepTip) {
		t.Errorf("remove withheld the peer-dependency tip though it removed a package, output was:\n%s", out)
	}
}

// TestRemoveCommandPassesTheInstallFlagThrough drives the cobra command rather
// than RunRemove, because declaring the flag and acting on it are two separate
// edits: a RunE that never reads it leaves `lnpm remove --install` accepted and
// silently ignored, and every test that calls RunRemove directly still passes.
//
// Don't add t.Parallel() here: recordInstalls swaps a process-wide var.
func TestRemoveCommandPassesTheInstallFlagThrough(t *testing.T) {
	newRemoveProject(t, "remove-pkg")
	ran := recordInstalls(t)

	// The commands are package-level values, so the parsed flag and the argument
	// list both outlive this test unless they are put back. It goes through the
	// root command because a subcommand's Execute() delegates to its parent
	// anyway, so args set on removeCmd alone are never the ones parsed.
	//
	// Changed is reset alongside the value because pflag sets it on parse and
	// never clears it, and cobra exposes it as Flags().Changed("install"). No
	// other test in this package drives rootCmd today, so only the value would
	// be missed - but the next one to do so would inherit a flag claiming the
	// user had passed it.
	t.Cleanup(func() {
		flag := removeCmd.Flags().Lookup("install")
		_ = flag.Value.Set("false")
		flag.Changed = false
		rootCmd.SetArgs(nil)
	})
	rootCmd.SetArgs([]string{"remove", "remove-pkg", "--install"})

	var err error
	out := captureStdout(t, func() { err = rootCmd.Execute() })

	if err != nil {
		t.Fatalf("lnpm remove --install = %v, want nil; output was:\n%s", err, out)
	}
	if got := ran(); len(got) != 1 {
		t.Errorf("lnpm remove --install ran %d install(s) %v, want exactly one; output was:\n%s", len(got), got, out)
	}
}

// TestInstallFlagIsSpelledTheSameWayEverywhere holds the sibling commands to one
// spelling. #336 is about the asymmetry between them, so the flag remove gained
// has to read the way the others do, not merely exist.
//
// It is not add's literal string: add and retreat already differ, each naming
// its own command ("after adding", "after retreat"). The shared shape is the
// sentence around that verb, and the flag defaulting to off.
func TestInstallFlagIsSpelledTheSameWayEverywhere(t *testing.T) {
	shape := regexp.MustCompile(`^Run npm install after \w+ \(default: no\)$`)

	for _, cmd := range []*cobra.Command{addCmd, retreatCmd, removeCmd} {
		flag := cmd.Flags().Lookup("install")
		if flag == nil {
			t.Errorf("%s has no --install flag", cmd.Name())
			continue
		}
		if !shape.MatchString(flag.Usage) {
			t.Errorf("%s --install help is %q, want the shape %q the sibling commands share", cmd.Name(), flag.Usage, shape)
		}
		if flag.DefValue != "false" {
			t.Errorf("%s --install defaults to %q, want it off", cmd.Name(), flag.DefValue)
		}
	}

	if got := removeCmd.Flags().Lookup("install"); got != nil && got.Usage != "Run npm install after removing (default: no)" {
		t.Errorf("remove --install help is %q, want it to name removing", got.Usage)
	}
}
