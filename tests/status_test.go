// RunStatus and RunList are exercised from this package, not from package cli,
// so they do not appear in `go test ./internal/cli/ -cover` — that measures the
// internal/cli test binary alone. To see their coverage:
//
//	go test ./tests/ -coverpkg=./internal/cli/ -coverprofile=cover.out
//
// which reports RunStatus and RunList around 90%, against 0% before these tests
// existed.
package tests

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// fieldValue returns the text printed after an indented "Label: value" line,
// so a test can assert on the value rather than merely on the label.
func fieldValue(t *testing.T, out, label string) string {
	t.Helper()

	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, label); ok {
			return strings.TrimSpace(after)
		}
	}
	t.Fatalf("output has no %q line, output was:\n%s", label, out)
	return ""
}

// isShortHash reports whether s looks like the abbreviated store hash RunList
// prints: non-empty and hexadecimal.
func isShortHash(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.Is(unicode.Hex_Digit, r) {
			return false
		}
	}
	return true
}

// statusSection returns the body of one RunStatus section: everything printed
// after the named header up to the blank line that separates it from the next
// one. Slicing the output this way lets a test say "the package appears under
// Active Links" rather than "the package appears somewhere", so a status that
// dropped a whole section would still fail even though the name is printed
// elsewhere in the report.
func statusSection(t *testing.T, out, name string) string {
	t.Helper()

	start := strings.Index(out, name+"\n")
	if start < 0 {
		t.Fatalf("status output has no %q section, output was:\n%s", name, out)
	}
	body := out[start+len(name)+1:]
	if end := strings.Index(body, "\n\n"); end >= 0 {
		return body[:end]
	}
	return body
}

// TestStatusEmptyStore pins what status reports when nothing has been
// published: both the packages and the links sections render their "(none)"
// placeholder, and the current-project section is omitted entirely because the
// cwd has no lockfile.
func TestStatusEmptyStore(t *testing.T) {
	env := setupTest(t)
	env.newProject("empty-status-project") // a cwd with no lnpm.lock

	out := captureStdout(t, func() {
		if err := cli.RunStatus(); err != nil {
			t.Errorf("RunStatus() error = %v", err)
		}
	})

	if got := statusSection(t, out, "Published Packages"); !strings.Contains(got, "(none)") {
		t.Errorf("Published Packages section should be empty, got:\n%s", got)
	}
	if got := statusSection(t, out, "Active Links"); !strings.Contains(got, "(none)") {
		t.Errorf("Active Links section should be empty, got:\n%s", got)
	}
	if strings.Contains(out, "Current Project") {
		t.Errorf("status reported a Current Project without a lockfile, output was:\n%s", out)
	}
}

// TestStatusWithPackagesAndLinks pins the populated report. The cwd is left
// inside the linked project, so all three sections run: the package is listed
// in the store, the project is listed as an active link with its package
// manager and the package it uses, and the current project's lockfile entry is
// printed with its version.
func TestStatusWithPackagesAndLinks(t *testing.T) {
	env := setupTest(t)
	_, projectDir := env.publishAndAdd("status-pkg")

	out := captureStdout(t, func() {
		if err := cli.RunStatus(); err != nil {
			t.Errorf("RunStatus() error = %v", err)
		}
	})

	published := statusSection(t, out, "Published Packages")
	if !strings.Contains(published, "status-pkg") || !strings.Contains(published, "1.0.0") {
		t.Errorf("Published Packages section is missing status-pkg@1.0.0, got:\n%s", published)
	}

	// The project column is truncated to 40 columns, so the assertion keys on
	// the package manager and the packages column, which are not.
	links := statusSection(t, out, "Active Links")
	if strings.Contains(links, "(none)") {
		t.Errorf("Active Links section reported no links despite an active link, got:\n%s", links)
	}
	if !strings.Contains(links, "npm") || !strings.Contains(links, "status-pkg") {
		t.Errorf("Active Links section is missing the npm project using status-pkg, got:\n%s", links)
	}

	// The linked package is printed from the lockfile, with the version the
	// lockfile recorded rather than the one in the store listing.
	current := statusSection(t, out, "Current Project")
	if !strings.Contains(current, "status-pkg@1.0.0") {
		t.Errorf("Current Project section is missing status-pkg@1.0.0, got:\n%s", current)
	}
	// Asserted separator-free: the printed cwd may be a resolved form of
	// projectDir (macOS resolves /var through a symlink), so only the leaf is
	// stable across platforms.
	if leaf := filepath.Base(projectDir); !strings.Contains(current, leaf) {
		t.Errorf("Current Project section does not name the project %q, got:\n%s", leaf, current)
	}
}

// TestListStore pins `lnpm list --store` in both states: it names every
// published package with its version, and reports the empty store rather than
// printing an empty list.
func TestListStore(t *testing.T) {
	t.Run("empty store", func(t *testing.T) {
		setupTest(t)

		out := captureStdout(t, func() {
			if err := cli.RunList(true, "", false); err != nil {
				t.Errorf("RunList(store) error = %v", err)
			}
		})

		if !strings.Contains(out, "No packages in store") {
			t.Errorf("RunList(store) on an empty store printed:\n%s", out)
		}
	})

	t.Run("populated store", func(t *testing.T) {
		env := setupTest(t)
		env.simplePkg("list-store-a")
		env.simplePkg("list-store-b")

		out := captureStdout(t, func() {
			if err := cli.RunList(true, "", false); err != nil {
				t.Errorf("RunList(store) error = %v", err)
			}
		})

		if !strings.Contains(out, "Packages in store:") {
			t.Errorf("RunList(store) did not print the store listing header, output was:\n%s", out)
		}
		for _, want := range []string{"list-store-a@1.0.0", "list-store-b@1.0.0"} {
			if !strings.Contains(out, want) {
				t.Errorf("RunList(store) did not list %q, output was:\n%s", want, out)
			}
		}
	})
}

// TestListProjectsForPackage pins the package+--projects mode: the linked
// project is named with its package manager, a package nobody links reports
// that rather than an empty list, and an unknown package is an error.
func TestListProjectsForPackage(t *testing.T) {
	t.Run("linked package", func(t *testing.T) {
		env := setupTest(t)
		_, projectDir := env.publishAndAdd("list-projects-pkg")

		out := captureStdout(t, func() {
			if err := cli.RunList(false, "list-projects-pkg", true); err != nil {
				t.Errorf("RunList(projects) error = %v", err)
			}
		})

		if !strings.Contains(out, "Projects using list-projects-pkg:") {
			t.Errorf("RunList(projects) did not print the projects header, output was:\n%s", out)
		}
		if leaf := filepath.Base(projectDir); !strings.Contains(out, leaf) {
			t.Errorf("RunList(projects) did not name the project %q, output was:\n%s", leaf, out)
		}
		if !strings.Contains(out, "(npm)") {
			t.Errorf("RunList(projects) did not report the project's package manager, output was:\n%s", out)
		}
	})

	t.Run("package with no projects", func(t *testing.T) {
		env := setupTest(t)
		env.simplePkg("unlinked-pkg")

		out := captureStdout(t, func() {
			if err := cli.RunList(false, "unlinked-pkg", true); err != nil {
				t.Errorf("RunList(projects) error = %v", err)
			}
		})

		if !strings.Contains(out, "No projects using unlinked-pkg") {
			t.Errorf("RunList(projects) on an unlinked package printed:\n%s", out)
		}
	})

	t.Run("unknown package", func(t *testing.T) {
		setupTest(t)

		err := cli.RunList(false, "no-such-pkg", true)
		if err == nil {
			t.Fatal("RunList(projects) on an unknown package returned nil, want an error")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("RunList(projects) error = %v, want it to say the package was not found", err)
		}
		if !strings.Contains(err.Error(), "no-such-pkg") {
			t.Errorf("RunList(projects) error = %v, want it to name no-such-pkg", err)
		}
	})

	t.Run("package name without --projects", func(t *testing.T) {
		env := setupTest(t)
		env.simplePkg("needs-projects-flag")

		err := cli.RunList(false, "needs-projects-flag", false)
		if err == nil {
			t.Fatal("RunList with a package name but no --projects returned nil, want an error")
		}
		if !strings.Contains(err.Error(), "--projects") {
			t.Errorf("RunList error = %v, want it to point at --projects", err)
		}
	})
}

// TestListCurrentProject pins the no-flags mode, which reads the cwd's
// lockfile: inside a linked project every recorded field is printed, and a
// directory without a lockfile reports no linked packages rather than failing
// (lockfile.Load treats a missing file as an empty lockfile).
func TestListCurrentProject(t *testing.T) {
	t.Run("linked project", func(t *testing.T) {
		env := setupTest(t)
		env.publishAndAdd("list-current-pkg") // leaves the cwd in the project

		out := captureStdout(t, func() {
			if err := cli.RunList(false, "", false); err != nil {
				t.Errorf("RunList(current) error = %v", err)
			}
		})

		if !strings.Contains(out, "Linked packages:") {
			t.Errorf("RunList(current) did not print the linked packages header, output was:\n%s", out)
		}
		if !strings.Contains(out, "list-current-pkg@1.0.0") {
			t.Errorf("RunList(current) did not list list-current-pkg@1.0.0, output was:\n%s", out)
		}
		// Assert the field *values*, not just the labels: printing "Source:"
		// with an empty value is exactly the kind of regression a
		// label-presence check waves through.
		for _, field := range []struct{ label, want string }{
			// The source is the package directory publishAndAdd created, so its
			// last element is the package name. Compared on the base name only,
			// which keeps the assertion free of path separators for Windows and
			// of macOS's /var -> /private/var symlink resolution.
			{"Source:", "list-current-pkg"},
			// Nothing else has elapsed, so formatTimeAgo's first branch applies.
			{"Linked:", "just now"},
		} {
			value := fieldValue(t, out, field.label)
			if !strings.Contains(value, field.want) {
				t.Errorf("RunList(current) printed %s %q, want it to contain %q, output was:\n%s",
					field.label, value, field.want, out)
			}
		}
		if hash := fieldValue(t, out, "Hash:"); !isShortHash(hash) {
			t.Errorf("RunList(current) printed Hash: %q, want a short hex hash, output was:\n%s", hash, out)
		}
	})

	t.Run("no lockfile", func(t *testing.T) {
		env := setupTest(t)
		env.newProject("unlinked-project")

		out := captureStdout(t, func() {
			if err := cli.RunList(false, "", false); err != nil {
				t.Errorf("RunList(current) without a lockfile error = %v", err)
			}
		})

		if !strings.Contains(out, "No linked packages in current project") {
			t.Errorf("RunList(current) without a lockfile printed:\n%s", out)
		}
	})
}
