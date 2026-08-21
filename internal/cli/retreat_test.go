package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if !strings.Contains(out, "not a valid package name") {
		t.Errorf("retreat did not say why the entry was refused, output was:\n%s", out)
	}
	if strings.Contains(out, "Retreat complete!") {
		t.Errorf("retreat claimed to be complete after refusing an entry, output was:\n%s", out)
	}

	// The refusal has to cover everything the entry drives, not only the delete.
	pkgJSON := readFileString(t, filepath.Join(project, "package.json"))
	if !strings.Contains(pkgJSON, hostile) {
		t.Errorf("retreat edited package.json for a refused entry, package.json is now:\n%s", pkgJSON)
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
