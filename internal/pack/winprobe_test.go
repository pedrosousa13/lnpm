package pack

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWindowsNameProbe is a temporary diagnostic for #326. It creates nothing in
// the product; it only asks the filesystem what it does with the names the issue
// names, so the Windows consequence can be measured rather than reasoned about.
// Delete it once the finding is recorded on the issue.
func TestWindowsNameProbe(t *testing.T) {
	t.Logf("GOOS probe running on %s", os.Getenv("RUNNER_OS"))

	// Half one: can a directory carrying a reserved device name be created at
	// all? mimics the .lnpm/{name} join the linker does.
	for _, name := range []string{"CON", "NUL", "AUX", "PRN", "COM1", "LPT1", "con", "con.js", "NUL.txt"} {
		dir := t.TempDir()
		err := os.MkdirAll(filepath.Join(dir, name), 0o755)
		if err != nil {
			t.Logf("PROBE mkdir %-8q -> ERROR %v", name, err)
			continue
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			t.Logf("PROBE mkdir %-8q -> ok, but ReadDir failed: %v", name, readErr)
			continue
		}
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		t.Logf("PROBE mkdir %-8q -> ok, directory now holds %q", name, got)
	}

	// Half two: does a trailing dot or space alias the bare name? Create the
	// bare name first, then ask whether the decorated one already resolves.
	for _, decorated := range []string{"foo.", "foo ", "foo..", "foo. "} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "foo"), 0o755); err != nil {
			t.Fatalf("creating the bare name failed: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, decorated)); err == nil {
			t.Logf("PROBE alias  %-8q -> ALIASES the existing \"foo\"", decorated)
		} else {
			t.Logf("PROBE alias  %-8q -> does not resolve to \"foo\": %v", decorated, err)
		}
	}

	// Half three: create the decorated name on its own and see what lands.
	for _, decorated := range []string{"foo.", "foo ", "foo..", "@ns/pkg."} {
		dir := t.TempDir()
		err := os.MkdirAll(filepath.Join(dir, decorated), 0o755)
		if err != nil {
			t.Logf("PROBE create %-9q -> ERROR %v", decorated, err)
			continue
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			t.Logf("PROBE create %-9q -> ok, but ReadDir failed: %v", decorated, readErr)
			continue
		}
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		t.Logf("PROBE create %-9q -> ok, directory now holds %q", decorated, got)
	}

	// Half four: what does the current validator say about each of these today?
	for _, name := range []string{"CON", "NUL", "com1", "con.js", "foo.", "foo ", "@ns/CON", "@CON/pkg"} {
		t.Logf("PROBE validate %-10q -> %v", name, ValidatePackageName(name))
	}
}
