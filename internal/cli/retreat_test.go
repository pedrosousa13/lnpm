package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// The tests here cover what retreat does with a package name it did not choose.
// lnpm.lock is a checked-in artifact, so its keys are attacker-controlled in any
// repository the developer did not write, and retreat joins them straight into
// paths it deletes.
//
// The load-bearing assertion in each hostile case is that the file outside the
// project is still there, with its contents. A test that only looked for a
// warning would pass against a "fix" that printed one after the delete.

const victimContents = "PRIVATE KEY\n"

// newRetreatProject lays out a project with a sibling directory holding a file
// retreat has no business touching, chdirs into the project, and returns both
// paths. The victim is a sibling because filepath.Join cleans as it joins:
// Join(project, "node_modules", "../../nm-victim/id_rsa") collapses to
// project/../nm-victim/id_rsa, one level above the project.
func newRetreatProject(t *testing.T) (project, victim string) {
	t.Helper()

	newDoctorStoreConfig(t)
	db.ResetForTesting()
	t.Cleanup(db.ResetForTesting)

	root := t.TempDir()
	project = filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(project, ".lnpm"), 0755); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, "node_modules"), 0755); err != nil {
		t.Fatalf("create node_modules: %v", err)
	}

	victimDir := filepath.Join(root, "nm-victim")
	if err := os.MkdirAll(victimDir, 0755); err != nil {
		t.Fatalf("create victim directory: %v", err)
	}
	victim = filepath.Join(victimDir, "id_rsa")
	writeFile(t, victim, victimContents)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	return project, victim
}

// writeRetreatLock writes package.json and lnpm.lock describing the given
// entries, each as a live lnpm link. An empty original version means the
// dependency was added by lnpm and retreat should drop it from package.json.
func writeRetreatLock(t *testing.T, project string, originalVersions map[string]string) {
	t.Helper()

	deps := make([]string, 0, len(originalVersions))
	lock := &lockfile.LockFile{Version: 1, Packages: map[string]lockfile.Package{}}
	for name, original := range originalVersions {
		deps = append(deps, `"`+name+`":"file:.lnpm/`+name+`"`)
		lock.Packages[name] = lockfile.Package{Version: "1.0.0", OriginalVersion: original}
	}

	writeFile(t, filepath.Join(project, "package.json"),
		`{"name":"victim-project","version":"1.0.0","dependencies":{`+strings.Join(deps, ",")+`}}`)
	if err := lock.Save(project); err != nil {
		t.Fatalf("save lock file: %v", err)
	}
}

// requireVictimIntact is the assertion every hostile case turns on: the file
// outside the project is still there and still holds what it held.
func requireVictimIntact(t *testing.T, victim string) {
	t.Helper()

	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("retreat deleted %s, a file outside the project: %v", victim, err)
	}
	if string(data) != victimContents {
		t.Errorf("retreat changed %s, a file outside the project: contents are %q, want %q",
			victim, string(data), victimContents)
	}
}

// TestRunRetreatRefusesATraversingLockEntry is the issue's own reproduction: a
// lock file whose only key climbs out of node_modules.
func TestRunRetreatRefusesATraversingLockEntry(t *testing.T) {
	project, victim := newRetreatProject(t)
	const hostile = "../../nm-victim/id_rsa"
	writeRetreatLock(t, project, map[string]string{hostile: ""})

	var err error
	out := captureStdout(t, func() { err = RunRetreat(true, false) })

	requireVictimIntact(t, victim)

	if err == nil {
		t.Errorf("RunRetreat() = nil after refusing an entry, want an error saying the retreat was incomplete; output was:\n%s", out)
	}
	if !strings.Contains(out, hostile) {
		t.Errorf("retreat did not name the refused entry %q, output was:\n%s", hostile, out)
	}
	// ": %v" is how every other error in this package trails, so the refusal
	// reads the same way as the rest of the command's output.
	if !strings.Contains(out, "not a valid package name: ") {
		t.Errorf("retreat did not say why the entry was refused, output was:\n%s", out)
	}
	if strings.Contains(out, "Retreat complete!") {
		t.Errorf("retreat claimed to be complete after refusing an entry, output was:\n%s", out)
	}

	// rootCmd silences the usage dump but not the error, so cobra prints
	// err.Error() straight after everything above. Repeating the summary
	// verbatim there would read as a stutter.
	if strings.Contains(out, err.Error()) {
		t.Errorf("the returned error repeats the summary line verbatim; error was %q, output was:\n%s", err, out)
	}

	// The refusal has to cover everything the entry drives, not only the delete.
	pkgJSON := readFileString(t, filepath.Join(project, "package.json"))
	if !strings.Contains(pkgJSON, hostile) {
		t.Errorf("retreat edited package.json for a refused entry, package.json is now:\n%s", pkgJSON)
	}
}

// TestRunRetreatSaysWhatARefusedEntryLeftBehind covers the advice, which has to
// survive the rest of the retreat running to completion around it. .lnpm/ is
// gone and lnpm.lock has been renamed by the time the user reads any of this,
// so telling them to go and look at lnpm.lock would name a file that is no
// longer there - and the file: reference the refusal declined to touch is still
// in package.json with nothing pointing it out.
func TestRunRetreatSaysWhatARefusedEntryLeftBehind(t *testing.T) {
	project, _ := newRetreatProject(t)
	const hostile = "../../nm-victim/id_rsa"
	writeRetreatLock(t, project, map[string]string{hostile: "", "good-pkg": "^1.0.0"})

	out := captureStdout(t, func() { _ = RunRetreat(true, false) })

	// What the advice has to be true about.
	if _, err := os.Stat(filepath.Join(project, "lnpm.lock")); !os.IsNotExist(err) {
		t.Fatalf("lnpm.lock is still in place (%v); this test is about the case where it was stashed", err)
	}
	if _, err := os.Stat(filepath.Join(project, lockfile.RetreatFileName)); err != nil {
		t.Fatalf("%s was not written: %v", lockfile.RetreatFileName, err)
	}

	// The advice is read after the summary, so that is where it has to be, and
	// scoping the assertions to it stops the earlier "saved as lnpm.lock.retreat"
	// progress line from satisfying them by accident.
	_, advice, ok := strings.Cut(out, "Retreat incomplete")
	if !ok {
		t.Fatalf("retreat did not report an incomplete retreat, output was:\n%s", out)
	}
	if !strings.Contains(advice, lockfile.RetreatFileName) {
		t.Errorf("the advice did not point at %s, where the refused entry actually went, advice was:\n%s",
			lockfile.RetreatFileName, advice)
	}
	if !strings.Contains(advice, "by hand") {
		t.Errorf("the advice did not say the leftover package.json dependency has to be removed by hand, advice was:\n%s", advice)
	}
	if !strings.Contains(advice, hostile) {
		t.Errorf("the advice did not name the dependency left in package.json, advice was:\n%s", advice)
	}
	if strings.Contains(advice, "good-pkg") {
		t.Errorf("the advice named good-pkg, which was retreated normally, advice was:\n%s", advice)
	}
}

// TestRunRetreatCleansValidEntriesBesideARefusedOne is the test that proves the
// refusal skips one entry rather than aborting the retreat: a developer with a
// tampered lock file must still be able to get out of lnpm.
func TestRunRetreatCleansValidEntriesBesideARefusedOne(t *testing.T) {
	project, victim := newRetreatProject(t)
	const hostile = "../../nm-victim/id_rsa"
	writeRetreatLock(t, project, map[string]string{hostile: "", "good-pkg": "^1.0.0"})

	goodLink := filepath.Join(project, "node_modules", "good-pkg")
	writeFile(t, goodLink, "module.exports = {}\n")

	var err error
	out := captureStdout(t, func() { err = RunRetreat(true, false) })

	requireVictimIntact(t, victim)

	if _, statErr := os.Lstat(goodLink); !os.IsNotExist(statErr) {
		t.Errorf("retreat left node_modules/good-pkg behind (%v); a refused entry must not stop the valid ones", statErr)
	}
	pkgJSON := readFileString(t, filepath.Join(project, "package.json"))
	if !strings.Contains(pkgJSON, `"good-pkg":"^1.0.0"`) {
		t.Errorf("retreat did not restore good-pkg's original version, package.json is now:\n%s", pkgJSON)
	}
	if _, statErr := os.Stat(filepath.Join(project, ".lnpm")); !os.IsNotExist(statErr) {
		t.Errorf(".lnpm/ was not removed (%v); a refused entry must not abort the retreat", statErr)
	}
	if err == nil {
		t.Errorf("RunRetreat() = nil after refusing an entry, want an error; output was:\n%s", out)
	}
}

// TestRunRetreatRefusesOtherUnsafeLockEntries covers the shapes that are not "..
// climbing out" but are equally not package names. filepath.Join swallows a
// leading separator, so an absolute key does not itself escape - it is refused
// because it is not a name lnpm could ever have linked, and letting it through
// would mean deleting whatever node_modules/<the whole path> happened to be.
func TestRunRetreatRefusesOtherUnsafeLockEntries(t *testing.T) {
	for _, tc := range []struct {
		desc string
		name string
	}{
		{"absolute path", "/etc/ssh/ssh_host_rsa_key"},
		{"parent directory", ".."},
		{"traversal under a scope", "@scope/.."},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			project, victim := newRetreatProject(t)
			writeRetreatLock(t, project, map[string]string{tc.name: ""})

			var err error
			out := captureStdout(t, func() { err = RunRetreat(true, false) })

			requireVictimIntact(t, victim)

			if err == nil {
				t.Errorf("RunRetreat() = nil for the %s key %q, want an error; output was:\n%s", tc.desc, tc.name, out)
			}
			if !strings.Contains(out, "not a valid package name") {
				t.Errorf("retreat did not refuse the %s key %q, output was:\n%s", tc.desc, tc.name, out)
			}
			pkgJSON := readFileString(t, filepath.Join(project, "package.json"))
			if !strings.Contains(pkgJSON, tc.name) {
				t.Errorf("retreat edited package.json for the refused %s key, package.json is now:\n%s", tc.desc, pkgJSON)
			}
		})
	}
}

// TestRunRetreatWithOnlyValidEntriesIsUnchanged pins the other side: the guard
// must be invisible to every project that is not under attack.
func TestRunRetreatWithOnlyValidEntriesIsUnchanged(t *testing.T) {
	project, _ := newRetreatProject(t)
	writeRetreatLock(t, project, map[string]string{"good-pkg": "^1.0.0", "@org/scoped": ""})

	var err error
	out := captureStdout(t, func() { err = RunRetreat(true, false) })

	if err != nil {
		t.Errorf("RunRetreat() = %v for a lock file of valid names, want nil", err)
	}
	if !strings.Contains(out, "Retreat complete!") {
		t.Errorf("retreat did not report completion, output was:\n%s", out)
	}
	if strings.Contains(out, "not a valid package name") || strings.Contains(out, "refused") {
		t.Errorf("retreat warned about a lock file of valid names, output was:\n%s", out)
	}
}

// TestRunRetreatTakesADotPrefixedEntryLinkedBeforeItWasInvalid covers the
// removal-side waiver at the retreat loop. #325 made a leading dot invalid, but
// a project linked before it can hold .lnpm/.hidden-pkg and a lock entry naming
// it, and retreat is the documented way out of lnpm — so it has to take that
// entry rather than refuse it.
//
// Refusing would be worse than pointless here: the RemoveAll of .lnpm at the end
// of the retreat takes the package's files whatever the loop decided, so a
// refusal leaves a node_modules entry and a package.json "file:.lnpm/.hidden-pkg"
// reference both pointing at a directory that is now gone.
func TestRunRetreatTakesADotPrefixedEntryLinkedBeforeItWasInvalid(t *testing.T) {
	project, _ := newRetreatProject(t)
	const dotted = ".hidden-pkg"
	writeRetreatLock(t, project, map[string]string{dotted: "^1.0.0"})

	// What the loop deletes per entry. The .lnpm tree is not asserted on: it is
	// removed wholesale at the end of the retreat either way, so it cannot tell
	// a taken entry from a refused one.
	nodeModulesEntry := filepath.Join(project, "node_modules", dotted)
	writeFile(t, nodeModulesEntry, "module.exports = {}\n")

	var err error
	out := captureStdout(t, func() { err = RunRetreat(true, false) })

	if err != nil {
		t.Errorf("RunRetreat() = %v for a dot-named lock entry, want nil", err)
	}
	if strings.Contains(out, "not a valid package name") {
		t.Errorf("retreat refused the dot-named entry %q, output was:\n%s", dotted, out)
	}
	if _, statErr := os.Lstat(nodeModulesEntry); !os.IsNotExist(statErr) {
		t.Errorf("retreat left node_modules/%s behind (%v); the entry was not taken", dotted, statErr)
	}
	pkgJSON := readFileString(t, filepath.Join(project, "package.json"))
	if !strings.Contains(pkgJSON, `".hidden-pkg":"^1.0.0"`) {
		t.Errorf("retreat did not restore the dot-named entry's original version, package.json is now:\n%s", pkgJSON)
	}
	if !strings.Contains(out, "Retreat complete!") {
		t.Errorf("retreat did not report completion, output was:\n%s", out)
	}
}

// TestRunRetreatPreviewShowsADotPrefixedEntryAsRetreatable is the preview's own
// assertion, and it is separate on purpose: the preview and the loop agreeing is
// the stated reason that call site was waived, so each site has to be pinned
// where reverting only that one turns this red.
func TestRunRetreatPreviewShowsADotPrefixedEntryAsRetreatable(t *testing.T) {
	project, _ := newRetreatProject(t)
	const dotted = ".hidden-pkg"
	writeRetreatLock(t, project, map[string]string{dotted: "^1.0.0"})

	var err error
	out := captureStdout(t, func() { err = RunRetreat(false, false) })

	if err != nil {
		t.Errorf("RunRetreat(force=false) = %v, want nil: a preview changes nothing", err)
	}
	if strings.Contains(out, "will be skipped") {
		t.Errorf("the preview said the dot-named entry would be skipped, output was:\n%s", out)
	}
	// The same line every ordinary entry gets. Asserting the whole line, not
	// just the name, keeps a "will be skipped" line that happens to name the
	// package from satisfying this.
	want := dotted + ": file:.lnpm/" + dotted + " → ^1.0.0"
	if !strings.Contains(out, want) {
		t.Errorf("the preview did not describe the dot-named entry as an ordinary retreat;\nwant a line containing %q, output was:\n%s", want, out)
	}
}

// TestRunRetreatPreviewFlagsAnUnsafeLockEntry covers the branch that runs
// without --force. It exists to tell the developer what retreat is about to do,
// so listing a hostile key as though it were an ordinary package is its own
// small lie - and this is the moment the developer is actually reading.
func TestRunRetreatPreviewFlagsAnUnsafeLockEntry(t *testing.T) {
	project, victim := newRetreatProject(t)
	const hostile = "../../nm-victim/id_rsa"
	writeRetreatLock(t, project, map[string]string{hostile: "", "good-pkg": "^1.0.0"})

	var err error
	out := captureStdout(t, func() { err = RunRetreat(false, false) })

	requireVictimIntact(t, victim)

	if err != nil {
		t.Errorf("RunRetreat(force=false) = %v, want nil: a preview changes nothing", err)
	}
	if !strings.Contains(out, "not a valid package name") {
		t.Errorf("the preview listed the hostile entry as an ordinary package, output was:\n%s", out)
	}
	if !strings.Contains(out, "good-pkg") {
		t.Errorf("the preview dropped the valid entry, output was:\n%s", out)
	}
}

// The tests below cover the second thing retreat joins a lock key into a path
// under: the two directories above node_modules/{name}. A name it validated can
// still land outside the project when node_modules - or, for a scoped name, the
// scope directory - is a link the repository committed. That is the linker's
// #339 hole reached through a different command, and the assertion that matters
// is the same one: the file outside the project is still there afterwards.

// symlinkDirAt points linkPath at target, and skips the test when the platform
// will not have it.
//
// os.Symlink rather than the linker's createDirSymlink, which is unexported:
// that one falls back to a junction on Windows, so these tests run there only
// with the symlink privilege and skip without it. The guard is not exercised in
// that case and saying so beats pretending it passed. Every case below reaches
// this helper before it asserts anything, so a skip is never a silent pass.
func symlinkDirAt(t *testing.T, target, linkPath string) {
	t.Helper()

	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("cannot create a directory link at %s: %v", linkPath, err)
	}
}

// allowSymlinkedNodeModules turns the override on in the config newRetreatProject
// already pointed LNPM_CONFIG at, appending rather than rewriting so the store
// path that config carries survives.
func allowSymlinkedNodeModules(t *testing.T) {
	t.Helper()

	path := os.Getenv("LNPM_CONFIG")
	if path == "" {
		t.Fatal("LNPM_CONFIG is unset; newRetreatProject is what sets it")
	}
	writeFile(t, path, readFileString(t, path)+"follow_symlinked_node_modules: true\n")
	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)
}

// symlinkedNodeModulesProject lays out the hostile checkout every case below
// starts from: a project whose node_modules is a link to the sibling directory
// the fixture keeps a file in, and a sentinel file in that directory at exactly
// the path retreat is about to remove.
//
// The sentinel is a plain file at the entry's own path, as the linker's
// TestUnlinkRefusesASymlinkedNodeModules uses, and for that test's reason: the
// call here is os.Remove, which cannot take a non-empty directory, so a sentinel
// inside one would leave the test passing for a reason the guard did not supply.
func symlinkedNodeModulesProject(t *testing.T, entry string) (project, sentinel string) {
	t.Helper()

	project, victim := newRetreatProject(t)
	victimDir := filepath.Dir(victim)
	sentinel = filepath.Join(victimDir, entry)
	writeFile(t, sentinel, victimContents)

	nodeModules := filepath.Join(project, "node_modules")
	if err := os.Remove(nodeModules); err != nil {
		t.Fatalf("clear the real node_modules: %v", err)
	}
	symlinkDirAt(t, victimDir, nodeModules)

	return project, sentinel
}

// requireProjectUntouched is what a refusal has to be worth. The retreat is
// refused before any of this is touched, so every artifact lnpm would have
// removed is still exactly where it was - which is what makes the remedy the
// message advertises something the user can actually take.
//
// .lnpm/ is the load-bearing one. The retreat loop's other refusal, on an
// invalid name, is safe precisely because lnpm never wrote a .lnpm/{name} for
// it; here it did, and RunRetreat's os.RemoveAll takes that directory whatever
// the loop decided. A guard that only skipped the entry would leave the files
// gone, the package.json reference dangling and lnpm.lock stashed.
func requireProjectUntouched(t *testing.T, project string, deps ...string) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(project, ".lnpm")); err != nil {
		t.Errorf(".lnpm/ Stat err = %v after a refused retreat, want the directory left alone", err)
	}
	if _, err := os.Stat(filepath.Join(project, "lnpm.lock")); err != nil {
		t.Errorf("lnpm.lock Stat err = %v after a refused retreat, want it left in place", err)
	}
	if _, err := os.Stat(filepath.Join(project, lockfile.RetreatFileName)); !os.IsNotExist(err) {
		t.Errorf("%s exists after a refused retreat (Stat err = %v); the lock file must not be stashed", lockfile.RetreatFileName, err)
	}
	pkgJSON := readFileString(t, filepath.Join(project, "package.json"))
	for _, dep := range deps {
		if !strings.Contains(pkgJSON, dep) {
			t.Errorf("retreat edited package.json for a refused retreat; want it to still hold %s, package.json is now:\n%s", dep, pkgJSON)
		}
	}
}

// requireRefusalNamesTheWayOut is the other half: a refusal the user cannot act
// on is the outcome the override exists to prevent, so the message has to carry
// the path and the key, not just a non-nil error.
func requireRefusalNamesTheWayOut(t *testing.T, err error, path string) {
	t.Helper()

	if err == nil {
		t.Fatal("RunRetreat() = nil through a symlinked node_modules, want a refusal")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("RunRetreat() error = %v, want it to name %s", err, path)
	}
	if !strings.Contains(err.Error(), "follow_symlinked_node_modules") {
		t.Errorf("RunRetreat() error = %v, want it to name the override", err)
	}
}

// TestRunRetreatRefusesASymlinkedNodeModules is this hole's reproduction, run
// against the unguarded build before the guard was written: with node_modules
// committed as a link out of the project, retreat deleted nm-victim/my-package
// and reported "OK Retreat complete!" with a nil error.
//
// The refusal takes the whole retreat rather than the entry, and the assertions
// are split accordingly: the sentinel outside the project survives, and so does
// everything inside it. Skipping the entry instead would satisfy the first of
// those and fail the second, because the os.RemoveAll of .lnpm/ below the loop
// runs whatever the loop decided.
func TestRunRetreatRefusesASymlinkedNodeModules(t *testing.T) {
	project, sentinel := symlinkedNodeModulesProject(t, "my-package")
	writeRetreatLock(t, project, map[string]string{"my-package": "^1.0.0"})

	var err error
	out := captureStdout(t, func() { err = RunRetreat(true, false) })

	requireVictimIntact(t, sentinel)
	requireProjectUntouched(t, project, `"my-package":"file:.lnpm/my-package"`)
	requireRefusalNamesTheWayOut(t, err, filepath.Join(project, "node_modules"))

	// Nothing was claimed either. The check runs above the first print, so a
	// refused retreat does not announce one.
	if out != "" {
		t.Errorf("a refused retreat printed %q, want nothing said before it refused", out)
	}
}

// TestRunRetreatRefusesASymlinkedNodeModulesScope covers the second ancestor a
// scoped name adds: a real node_modules holding a committed @org link redirects
// every scoped package one level down. Against the unguarded build this fixture
// deleted nm-victim/scoped.
//
// The unscoped package beside it is the point of the fixture. The scope check is
// per entry, so a check inside the loop would refuse the scoped entry only after
// good-pkg had already been removed - a half-retreated project. The preflight
// answers for both before either is touched, so good-pkg is still linked here.
func TestRunRetreatRefusesASymlinkedNodeModulesScope(t *testing.T) {
	project, victim := newRetreatProject(t)
	victimDir := filepath.Dir(victim)
	sentinel := filepath.Join(victimDir, "scoped")
	writeFile(t, sentinel, victimContents)

	scopeDir := filepath.Join(project, "node_modules", "@org")
	symlinkDirAt(t, victimDir, scopeDir)

	writeRetreatLock(t, project, map[string]string{"@org/scoped": "", "good-pkg": "^1.0.0"})
	goodLink := filepath.Join(project, "node_modules", "good-pkg")
	writeFile(t, goodLink, "module.exports = {}\n")

	var err error
	out := captureStdout(t, func() { err = RunRetreat(true, false) })

	requireVictimIntact(t, sentinel)
	requireProjectUntouched(t, project,
		`"@org/scoped":"file:.lnpm/@org/scoped"`, `"good-pkg":"file:.lnpm/good-pkg"`)
	requireRefusalNamesTheWayOut(t, err, scopeDir)

	if _, statErr := os.Lstat(goodLink); statErr != nil {
		t.Errorf("node_modules/good-pkg Lstat err = %v; a refused retreat must not remove the entries it could have", statErr)
	}
	if out != "" {
		t.Errorf("a refused retreat printed %q, want nothing said before it refused", out)
	}
}

// TestRunRetreatRefusedForASymlinkedNodeModulesStillRetreatsWithTheOverride is
// what the refusal is worth, asserted rather than argued. The message tells the
// user to set the override and re-run; this runs exactly that, and the retreat
// completes. It can only pass if the refusal left .lnpm/ and lnpm.lock alone,
// which is the whole reason it refuses the retreat instead of the entry.
func TestRunRetreatRefusedForASymlinkedNodeModulesStillRetreatsWithTheOverride(t *testing.T) {
	project, sentinel := symlinkedNodeModulesProject(t, "my-package")
	writeRetreatLock(t, project, map[string]string{"my-package": "^1.0.0"})

	var err error
	captureStdout(t, func() { err = RunRetreat(true, false) })
	if err == nil {
		t.Fatal("RunRetreat() = nil through a symlinked node_modules, want the refusal this test recovers from")
	}

	// The remedy the refusal names, taken literally.
	allowSymlinkedNodeModules(t)

	out := captureStdout(t, func() { err = RunRetreat(true, false) })

	if err != nil {
		t.Errorf("RunRetreat() = %v after setting the override, want the retreat the refusal promised; output was:\n%s", err, out)
	}
	if !strings.Contains(out, "Retreat complete!") {
		t.Errorf("retreat did not report completion after setting the override, output was:\n%s", out)
	}
	pkgJSON := readFileString(t, filepath.Join(project, "package.json"))
	if !strings.Contains(pkgJSON, `"my-package":"^1.0.0"`) {
		t.Errorf("retreat did not restore my-package's original version, package.json is now:\n%s", pkgJSON)
	}
	// The override is what the second run followed the link on, so the sentinel
	// is gone this time. That is the behaviour it restores, not a regression.
	if _, statErr := os.Lstat(sentinel); !os.IsNotExist(statErr) {
		t.Errorf("nm-victim/my-package survived the override run (%v); the override has to restore the removal through the link", statErr)
	}
}

// TestRunRetreatFollowsASymlinkedNodeModulesWithTheOverride is the other side of
// the maintainer's decision, carried over from the linker: relocating
// node_modules is a setup people run, so with the override set retreat must do
// exactly what it did before the guard existed - remove the entry wherever the
// relocation put it.
//
// Like its linker counterparts this passes against the unguarded build too, and
// has to: it pins what the override gives back, not what the guard stops.
func TestRunRetreatFollowsASymlinkedNodeModulesWithTheOverride(t *testing.T) {
	project, victim := newRetreatProject(t)
	allowSymlinkedNodeModules(t)

	relocated := filepath.Join(filepath.Dir(victim), "..", "relocated")
	if err := os.MkdirAll(relocated, 0755); err != nil {
		t.Fatalf("create the relocation target: %v", err)
	}
	entry := filepath.Join(relocated, "my-package")
	writeFile(t, entry, "module.exports = {}\n")

	nodeModules := filepath.Join(project, "node_modules")
	if err := os.Remove(nodeModules); err != nil {
		t.Fatalf("clear the real node_modules: %v", err)
	}
	symlinkDirAt(t, relocated, nodeModules)

	writeRetreatLock(t, project, map[string]string{"my-package": "^1.0.0"})

	var err error
	out := captureStdout(t, func() { err = RunRetreat(true, false) })

	if err != nil {
		t.Errorf("RunRetreat() = %v with the override set, want it to proceed; output was:\n%s", err, out)
	}
	if !strings.Contains(out, "Retreat complete!") {
		t.Errorf("retreat did not report completion with the override set, output was:\n%s", out)
	}
	if _, statErr := os.Lstat(entry); !os.IsNotExist(statErr) {
		t.Errorf("relocated/my-package is still there (%v); the override has to restore the removal through the link", statErr)
	}
	pkgJSON := readFileString(t, filepath.Join(project, "package.json"))
	if !strings.Contains(pkgJSON, `"my-package":"^1.0.0"`) {
		t.Errorf("retreat did not restore my-package's original version, package.json is now:\n%s", pkgJSON)
	}
}

// TestRunRetreatPreviewFlagsASymlinkedNodeModules holds the preview to the
// invariant the name check states in retreat.go: the preview and the action
// agree on what --force is going to do. --force refuses outright and removes
// nothing, so a preview that listed the packages it would have removed would
// describe a retreat that is not going to happen.
func TestRunRetreatPreviewFlagsASymlinkedNodeModules(t *testing.T) {
	project, sentinel := symlinkedNodeModulesProject(t, "my-package")
	writeRetreatLock(t, project, map[string]string{"my-package": "^1.0.0"})

	var err error
	out := captureStdout(t, func() { err = RunRetreat(false, false) })

	requireVictimIntact(t, sentinel)
	requireProjectUntouched(t, project, `"my-package":"file:.lnpm/my-package"`)

	if err != nil {
		t.Errorf("RunRetreat(force=false) = %v, want nil: a preview changes nothing", err)
	}
	if !strings.Contains(out, "will refuse and remove nothing") {
		t.Errorf("the preview did not say --force is going to refuse, output was:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(project, "node_modules")) {
		t.Errorf("the preview did not name the project's node_modules, output was:\n%s", out)
	}
	if !strings.Contains(out, "follow_symlinked_node_modules") {
		t.Errorf("the preview did not name the override, output was:\n%s", out)
	}
	// The listing is what it replaced, not something it printed alongside.
	if strings.Contains(out, "Changes that will be made") {
		t.Errorf("the preview listed changes --force is not going to make, output was:\n%s", out)
	}
}

// The tests below cover the third thing that can send retreat somewhere it
// should not go: the store's own record of this project. retreat reads it to
// find the link rows to delete, and #391 made that read report damage instead of
// handing back whatever decoded. A record retreat cannot read is a record it
// cannot clean up from, so the retreat is refused whole - the same all-or-nothing
// shape, and for the same reason, as the node_modules preflight above.

// damageProjectRecord registers the current directory as a project and then
// writes bytes over its record, which damage builds from the record as the store
// actually holds it.
//
// The lookup is checked before the damage goes in so that a fixture whose
// directory the store does not answer for says which of those two things went
// wrong. Without it the nil project is dereferenced for its ID on the way into
// linkKey, and the fixture dies on a nil pointer naming neither the directory nor
// what the store did with it.
func damageProjectRecord(t *testing.T, damage func(stored *db.Project) []byte) {
	t.Helper()

	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	proj := &db.Project{Path: cwd, Name: "victim-project", PackageManager: "npm"}
	if err := database.InsertProject(proj); err != nil {
		t.Fatalf("register the project: %v", err)
	}
	stored, err := database.GetProjectByPath(cwd)
	if err != nil || stored == nil {
		t.Fatalf("the store does not answer for %s (project %v, err %v); this fixture would never reach the damaged record", cwd, stored, err)
	}

	damageDatabase(t, "projects", linkKey(stored.ID), damage(stored))
}

// retypeStoredField re-marshals the record lnpm wrote and replaces one field's
// value with a number, which is what the wrong-type damage shape looks like on a
// record the store really holds.
//
// Going through the record rather than hand-writing a JSON document is the
// point. The wrong-type case is only worth running if the damaged record still
// decodes a real project ID, and a hand-written document would establish that
// about itself rather than about anything lnpm writes. Marshalling the struct
// damages the record as the store actually holds it.
//
// The two checks below are what say the fixture still drives that shape: the
// record must fail to decode, and the ID must survive the failure.
//
// The ID survives for a reason particular to this helper rather than a general
// one. Re-marshalling puts the fields in the order Project declares them, and id
// is the first, so nothing this writes can land ahead of it - and fields ahead of
// a mismatch are kept whichever way the decoder handles it, whether it records
// the error and carries on or stops at the field outright. GetProjectByPath's doc
// comment carries the measurement for both. A hand-written document would have
// had neither guarantee.
//
// The second check is insurance rather than a guard against anything reachable
// today: it fails if the ID ever stops surviving, whatever change made that true,
// which would leave the wrong-type test asserting nothing the syntax-error test
// does not already cover.
func retypeStoredField(t *testing.T, stored *db.Project, field, was string) []byte {
	t.Helper()

	record, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal the stored project: %v", err)
	}
	old := fmt.Sprintf("%q:%q", field, was)
	if !bytes.Contains(record, []byte(old)) {
		t.Fatalf("the stored record does not hold %s; it is %s", old, record)
	}
	damaged := bytes.Replace(record, []byte(old), fmt.Appendf(nil, "%q:123", field), 1)

	var decoded db.Project
	if err := json.Unmarshal(damaged, &decoded); err == nil {
		t.Fatalf("the damaged record still decodes cleanly: %s", damaged)
	}
	if decoded.ID != stored.ID {
		t.Fatalf("decoding the damaged record left ID %d, not the real %d; this fixture no longer drives the shape it exists for: %s",
			decoded.ID, stored.ID, damaged)
	}
	return damaged
}

// requireRefusedForADamagedRecord is what every case here turns on: a non-nil
// error that names the damage, raised by the read that found it, and a project
// nothing has been taken out of.
//
// The untouched half is the load-bearing one. RunRetreat's os.RemoveAll of
// .lnpm/ and its stashLockForRestore both run below the removal loop and neither
// is conditional on it, so a guard that only skipped the database work - or one
// that aborted from inside the loop - would still end with the package's files
// deleted, package.json still carrying file:.lnpm/my-package pointing at
// nothing, and lnpm.lock moved aside so a re-run reports no links at all. That is
// the state the leading-dot waiver in the loop calls worse than pointless.
//
// The prefix assertion is what makes this pin RunRetreat's own check rather than
// its neighbour's. Two reads in RunRetreat ask the store about this same path,
// and both refuse damage, so an error-only assertion passes whichever one raised
// it - which left the check on the first read unpinned, since restoring its
// discard simply moved the refusal to linksOfProject. The two are distinguishable
// in the message: linksOfProject wraps what it returns, so its refusal cannot
// begin with the lookup's own words while RunRetreat's, which wraps only a
// trailing hint, does.
func requireRefusedForADamagedRecord(t *testing.T, err error, out, project string) {
	t.Helper()

	if err == nil {
		t.Fatalf("RunRetreat() = nil for a project record the store cannot read, want a refusal; output was:\n%s", out)
	}
	if !strings.Contains(err.Error(), "will not parse") {
		t.Errorf("RunRetreat() error = %v, want it to say the record will not parse", err)
	}
	// Resolved, because the error names the path the lookup normalized rather
	// than the one the fixture handed it. GetProjectByPath runs its argument
	// through normalizePath, which is filepath.EvalSymlinks, and on Windows that
	// expands an 8.3 short name: a temp directory that arrives as
	// C:\Users\RUNNER~1\... is named in the error as C:\Users\runneradmin\... .
	// seedLinkedProject records the same asymmetry for the paths gc prints.
	wantDir := resolvePath(project)
	if !strings.Contains(err.Error(), wantDir) {
		t.Errorf("RunRetreat() error = %v, want it to name the project directory %s", err, wantDir)
	}
	const lookupPrefix = "the record of the project at "
	if !strings.HasPrefix(err.Error(), lookupPrefix) {
		t.Errorf("RunRetreat() error = %q, want it to begin %q; anything ahead of that means some later reader refused and RunRetreat's own check on the project record did not", err, lookupPrefix)
	}
	// A refusal has to say which side of the removals it happened on, as the
	// node_modules preflight's does. Without it the user cannot tell a project
	// that was left alone from one that is half retreated.
	if !strings.Contains(err.Error(), "nothing was removed") {
		t.Errorf("RunRetreat() error = %v, want it to say nothing was removed", err)
	}
	requireProjectUntouched(t, project, `"my-package":"file:.lnpm/my-package"`)
	// Nothing was claimed either. The read runs above the first print, as the
	// node_modules preflight does, so a refused retreat does not announce one.
	if out != "" {
		t.Errorf("a refused retreat printed %q, want nothing said before it refused", out)
	}
}

// TestRunRetreatRefusesAProjectRecordThatWillNotParse drives the syntax-error
// shape. json.Unmarshal validates a document before decoding any of it, so this
// one decodes nothing: pre-#391 the lookup handed back a project with every
// field zero, which is the harmless end of the damage - ID 0 names no row the
// store ever assigned. The shape below is the one that cost something.
func TestRunRetreatRefusesAProjectRecordThatWillNotParse(t *testing.T) {
	project, _ := newRetreatProject(t)
	writeRetreatLock(t, project, map[string]string{"my-package": "^1.0.0"})
	damageProjectRecord(t, func(*db.Project) []byte { return []byte("{ not a project") })

	var err error
	out := captureStdout(t, func() { err = RunRetreat(true, false) })

	requireRefusedForADamagedRecord(t, err, out, project)
}

// TestRunRetreatRefusesAProjectRecordWhoseValueHasTheWrongType drives the shape
// that costs more here, and which the test above does not reach.
//
// A document that parses but holds a value of the wrong type loses only the
// field that will not decode, so a real project ID survives it -
// retypeStoredField asserts that rather than taking it on trust. Pre-#391 the
// lookup therefore handed back a half-built project carrying an ID that names
// live link rows, out of a record it had just failed to read. Nothing in retreat
// consumed it: linksOfProject re-reads this same path in the same block and
// refused first, so the removal loop and its DeleteLink were never reached. What
// the shape cost was the margin - the ID that would have been handed to a caller
// that did not check, where the other shape's zero could only ever have named
// nothing.
func TestRunRetreatRefusesAProjectRecordWhoseValueHasTheWrongType(t *testing.T) {
	project, _ := newRetreatProject(t)
	writeRetreatLock(t, project, map[string]string{"my-package": "^1.0.0"})
	// package_manager takes a string, so this parses and then fails to decode,
	// leaving every other field - id among them - decoded.
	damageProjectRecord(t, func(stored *db.Project) []byte {
		return retypeStoredField(t, stored, "package_manager", "npm")
	})

	var err error
	out := captureStdout(t, func() { err = RunRetreat(true, false) })

	requireRefusedForADamagedRecord(t, err, out, project)
}

// TestRunRetreatStillRetreatsADirectoryTheStoreDoesNotKnow pins the case the two
// above have to stay distinguishable from. A project with no record is the
// ordinary state of a lock file written by an add whose database write failed,
// and it is not damage: there are no rows to clean up, so retreat does the rest
// of its work and completes. Reading a missing record as a reason to refuse
// would take retreat - the documented way out of lnpm - away from exactly the
// projects whose database bookkeeping is already broken.
func TestRunRetreatStillRetreatsADirectoryTheStoreDoesNotKnow(t *testing.T) {
	project, _ := newRetreatProject(t)
	writeRetreatLock(t, project, map[string]string{"my-package": "^1.0.0"})

	var err error
	out := captureStdout(t, func() { err = RunRetreat(true, false) })

	if err != nil {
		t.Errorf("RunRetreat() = %v for a directory the store holds no record of, want nil; output was:\n%s", err, out)
	}
	if !strings.Contains(out, "Retreat complete!") {
		t.Errorf("retreat did not report completion, output was:\n%s", out)
	}
	pkgJSON := readFileString(t, filepath.Join(project, "package.json"))
	if !strings.Contains(pkgJSON, `"my-package":"^1.0.0"`) {
		t.Errorf("retreat did not restore my-package's original version, package.json is now:\n%s", pkgJSON)
	}
}

// TestLinksOfProjectRefusesARecordThatWillNotParse pins the fail-closed half of
// the read retreat shares with pull and remove. It sits with the damaged-record
// cases above, whose fixture shape it repeats, rather than in a new file for one
// test - this package has no tag_test.go to put it in.
//
// #329 is the reason this is asserted rather than assumed: making a read strict
// is only half the work, and the half that went wrong there was a caller that
// warned about the new error and carried on. linksOfProject already returns it,
// and returning nil links with it is what stops a caller reading the failure as
// an empty set - an answer that means "this project holds no links", and so
// leaves every link in place while the command reports success.
func TestLinksOfProjectRefusesARecordThatWillNotParse(t *testing.T) {
	newDoctorStoreConfig(t)
	db.ResetForTesting()
	t.Cleanup(db.ResetForTesting)

	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	project := t.TempDir()
	proj := &db.Project{Path: project, Name: "consumer", PackageManager: "npm"}
	if err := database.InsertProject(proj); err != nil {
		t.Fatalf("register the project: %v", err)
	}

	// Closes lnpm's handle, so the read below has to reopen it.
	damageDatabase(t, "projects", linkKey(proj.ID), []byte("{ not a project"))
	database, err = db.GetDB()
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}

	held, err := linksOfProject(database, project)
	if err == nil {
		t.Fatal("linksOfProject() returned no error for a project record the store cannot read")
	}
	if held != nil {
		t.Errorf("linksOfProject() returned %v alongside its error; a caller reading the links instead of the error cannot tell a failed read from a project that holds none", held)
	}
}

// writeFile writes content to path, failing the test if it cannot. It lives
// here rather than beside its first caller in output_tty_linux_test.go, which
// carries a linux build tag the tests above do not.
func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readFileString reads path, failing the test if it cannot.
func readFileString(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
