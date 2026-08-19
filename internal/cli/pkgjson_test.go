package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests assert on the RAW BYTES package.json is left with. A parsed-map
// comparison cannot see the bug they exist for - key order, indentation and
// large integer literals all survive a round trip through map[string]interface{}
// as far as a parsed comparison is concerned, and are all destroyed on disk.

// newPkgJSON writes content as a project's package.json and returns the project
// directory and the file path.
func newPkgJSON(t *testing.T, content string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	return dir, path
}

// readPkgJSON reads package.json back verbatim.
func readPkgJSON(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read package.json: %v", err)
	}
	return string(data)
}

// assertBytes compares raw output against the exact expected file content,
// reporting the first differing line so failures are readable.
func assertBytes(t *testing.T, got, want string) {
	t.Helper()
	if got == want {
		return
	}
	gotLines := strings.Split(got, "\n")
	wantLines := strings.Split(want, "\n")
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		g, w := "<missing>", "<missing>"
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Fatalf("package.json differs at line %d:\n  want %q\n  got  %q\n\nfull output:\n%s", i+1, w, g, got)
		}
	}
	t.Fatalf("package.json differs:\nwant %q\ngot  %q", want, got)
}

// diffLines reports the lines added and removed between before and after, using
// a longest-common-subsequence walk so that an insertion does not read as every
// following line having changed.
func diffLines(before, after string) (added, removed []string) {
	a := strings.Split(before, "\n")
	b := strings.Split(after, "\n")

	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			removed = append(removed, a[i])
			i++
		default:
			added = append(added, b[j])
			j++
		}
	}
	removed = append(removed, a[i:]...)
	added = append(added, b[j:]...)
	return added, removed
}

func assertDiff(t *testing.T, before, after string, wantAdded, wantRemoved []string) {
	t.Helper()
	added, removed := diffLines(before, after)
	if !equalStrings(added, wantAdded) || !equalStrings(removed, wantRemoved) {
		t.Fatalf("unexpected diff\n  added:   %q (want %q)\n  removed: %q (want %q)\n\nfull output:\n%s",
			added, wantAdded, removed, wantRemoved, after)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The headline bug: a package.json whose top-level keys are not in alphabetical
// order must come out of an add with those keys still in the author's order, its
// 4-space indentation intact, and an integer literal beyond 2^53 untouched. Only
// the dependency lines may change.
func TestWriteLnpmReferenceOnlyTouchesTheDependencyLines(t *testing.T) {
	before := `{
    "name": "my-app",
    "version": "1.0.0",
    "private": true,
    "buildNumber": 9007199254740993,
    "scripts": {
        "build": "tsc",
        "test": "vitest"
    },
    "dependencies": {
        "zod": "^3.22.0",
        "express": "^4.18.0"
    }
}
`
	_, path := newPkgJSON(t, before)

	if err := writeLnpmReference(path, "my-lib", false, false); err != nil {
		t.Fatalf("writeLnpmReference: %v", err)
	}

	after := readPkgJSON(t, path)
	assertDiff(t, before, after,
		[]string{`        "express": "^4.18.0",`, `        "my-lib": "file:.lnpm/my-lib"`},
		[]string{`        "express": "^4.18.0"`})
}

func TestWriteLnpmReferencePreservesTabIndentation(t *testing.T) {
	before := "{\n\t\"name\": \"my-app\",\n\t\"dependencies\": {\n\t\t\"zod\": \"^3.22.0\"\n\t}\n}\n"
	want := "{\n\t\"name\": \"my-app\",\n\t\"dependencies\": {\n\t\t\"zod\": \"^3.22.0\",\n\t\t\"my-lib\": \"file:.lnpm/my-lib\"\n\t}\n}\n"

	_, path := newPkgJSON(t, before)

	if err := writeLnpmReference(path, "my-lib", false, false); err != nil {
		t.Fatalf("writeLnpmReference: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), want)
}

// An entry that already exists keeps its position in the object rather than
// being re-sorted to wherever alphabetical order would put it.
func TestWriteLnpmReferenceReplacesExistingEntryInPlace(t *testing.T) {
	before := `{
  "dependencies": {
    "zod": "^3.22.0",
    "my-lib": "^1.0.0",
    "express": "^4.18.0"
  }
}
`
	want := `{
  "dependencies": {
    "zod": "^3.22.0",
    "my-lib": "link:.lnpm/my-lib",
    "express": "^4.18.0"
  }
}
`
	_, path := newPkgJSON(t, before)

	if err := writeLnpmReference(path, "my-lib", false, true); err != nil {
		t.Fatalf("writeLnpmReference: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), want)
}

func TestWriteLnpmReferenceCreatesMissingDependenciesField(t *testing.T) {
	before := `{
  "name": "my-app",
  "version": "1.0.0"
}
`
	want := `{
  "name": "my-app",
  "version": "1.0.0",
  "dependencies": {
    "my-lib": "file:.lnpm/my-lib"
  }
}
`
	_, path := newPkgJSON(t, before)

	if err := writeLnpmReference(path, "my-lib", false, false); err != nil {
		t.Fatalf("writeLnpmReference: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), want)
}

func TestWriteLnpmReferenceFillsEmptyDependenciesObject(t *testing.T) {
	before := `{
    "name": "my-app",
    "dependencies": {}
}
`
	want := `{
    "name": "my-app",
    "dependencies": {
        "my-lib": "file:.lnpm/my-lib"
    }
}
`
	_, path := newPkgJSON(t, before)

	if err := writeLnpmReference(path, "my-lib", false, false); err != nil {
		t.Fatalf("writeLnpmReference: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), want)
}

// A package.json with no keys at all has nothing to copy an indent from, so the
// new field falls back to the 2-space default.
func TestWriteLnpmReferenceFillsEmptyDocument(t *testing.T) {
	want := `{
  "dependencies": {
    "my-lib": "file:.lnpm/my-lib"
  }
}
`
	_, path := newPkgJSON(t, "{}\n")

	if err := writeLnpmReference(path, "my-lib", false, false); err != nil {
		t.Fatalf("writeLnpmReference: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), want)
}

// A minified package.json has no lines to match, so the new entry goes inline
// beside its siblings rather than dragging a newline into the file.
func TestWriteLnpmReferenceKeepsMinifiedFileInline(t *testing.T) {
	before := `{"version":"1.0.0","name":"my-app","dependencies":{"zod":"^3.22.0"}}`
	want := `{"version":"1.0.0","name":"my-app","dependencies":{"zod":"^3.22.0", "my-lib": "file:.lnpm/my-lib"}}` + "\n"

	_, path := newPkgJSON(t, before)

	if err := writeLnpmReference(path, "my-lib", false, false); err != nil {
		t.Fatalf("writeLnpmReference: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), want)
}

// --dev on a package already in dependencies moves it to devDependencies. Both
// objects are edited, and nothing else in the file may move.
func TestWriteLnpmReferenceMovesEntryBetweenFields(t *testing.T) {
	before := `{
  "name": "my-app",
  "dependencies": {
    "zod": "^3.22.0",
    "my-lib": "^1.0.0"
  },
  "devDependencies": {
    "vitest": "^1.0.0"
  }
}
`
	want := `{
  "name": "my-app",
  "dependencies": {
    "zod": "^3.22.0"
  },
  "devDependencies": {
    "vitest": "^1.0.0",
    "my-lib": "file:.lnpm/my-lib"
  }
}
`
	_, path := newPkgJSON(t, before)

	if err := writeLnpmReference(path, "my-lib", true, false); err != nil {
		t.Fatalf("writeLnpmReference: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), want)
}

// CI runs Windows, where package.json files routinely have CRLF line endings.
// The inserted line must use the file's own ending, not a bare LF.
func TestWriteLnpmReferencePreservesCRLFLineEndings(t *testing.T) {
	before := "{\r\n  \"name\": \"my-app\",\r\n  \"dependencies\": {\r\n    \"zod\": \"^3.22.0\"\r\n  }\r\n}\r\n"
	want := "{\r\n  \"name\": \"my-app\",\r\n  \"dependencies\": {\r\n    \"zod\": \"^3.22.0\",\r\n    \"my-lib\": \"file:.lnpm/my-lib\"\r\n  }\r\n}\r\n"

	_, path := newPkgJSON(t, before)

	if err := writeLnpmReference(path, "my-lib", false, false); err != nil {
		t.Fatalf("writeLnpmReference: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), want)
}

// lnpm has always left package.json with a trailing newline; a file that
// arrives without one still gains one.
func TestWriteLnpmReferenceAddsTrailingNewlineWhenMissing(t *testing.T) {
	before := "{\n  \"dependencies\": {\n    \"zod\": \"^3.22.0\"\n  }\n}"
	want := "{\n  \"dependencies\": {\n    \"zod\": \"^3.22.0\",\n    \"my-lib\": \"file:.lnpm/my-lib\"\n  }\n}\n"

	_, path := newPkgJSON(t, before)

	if err := writeLnpmReference(path, "my-lib", false, false); err != nil {
		t.Fatalf("writeLnpmReference: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), want)
}

// tests/remove_test.go relies on an unparseable package.json failing the write,
// so that add/remove abort instead of silently losing the file.
func TestWriteLnpmReferenceRejectsInvalidJSON(t *testing.T) {
	_, path := newPkgJSON(t, "{not valid json")

	if err := writeLnpmReference(path, "my-lib", false, false); err == nil {
		t.Fatal("expected an error for unparseable package.json, got nil")
	}
}

func TestRestorePackageJSONOnlyTouchesTheAffectedLine(t *testing.T) {
	before := `{
    "name": "my-app",
    "version": "1.0.0",
    "buildNumber": 9007199254740993,
    "dependencies": {
        "zod": "^3.22.0",
        "my-lib": "file:.lnpm/my-lib",
        "express": "^4.18.0"
    }
}
`
	dir, path := newPkgJSON(t, before)

	if err := restorePackageJSON(dir, "my-lib", "^2.5.0"); err != nil {
		t.Fatalf("restorePackageJSON: %v", err)
	}

	after := readPkgJSON(t, path)
	assertDiff(t, before, after,
		[]string{`        "my-lib": "^2.5.0",`},
		[]string{`        "my-lib": "file:.lnpm/my-lib",`})
}

func TestRestorePackageJSONRestoresInDevDependencies(t *testing.T) {
	before := `{
  "devDependencies": {
    "my-lib": "file:.lnpm/my-lib",
    "vitest": "^1.0.0"
  },
  "dependencies": {
    "zod": "^3.22.0"
  }
}
`
	want := `{
  "devDependencies": {
    "my-lib": "^2.5.0",
    "vitest": "^1.0.0"
  },
  "dependencies": {
    "zod": "^3.22.0"
  }
}
`
	dir, path := newPkgJSON(t, before)

	if err := restorePackageJSON(dir, "my-lib", "^2.5.0"); err != nil {
		t.Fatalf("restorePackageJSON: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), want)
}

func TestRestorePackageJSONLeavesFileAloneWhenPackageAbsent(t *testing.T) {
	before := `{
    "name": "my-app",
    "dependencies": {
        "zod": "^3.22.0"
    }
}
`
	dir, path := newPkgJSON(t, before)

	if err := restorePackageJSON(dir, "my-lib", "^2.5.0"); err != nil {
		t.Fatalf("restorePackageJSON: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), before)
}

func TestRestorePackageJSONRejectsInvalidJSON(t *testing.T) {
	dir, _ := newPkgJSON(t, "{not valid json")

	if err := restorePackageJSON(dir, "my-lib", "^2.5.0"); err == nil {
		t.Fatal("expected an error for unparseable package.json, got nil")
	}
}

func TestRemoveFromPackageJSONOnlyTouchesTheAffectedLine(t *testing.T) {
	before := `{
    "name": "my-app",
    "version": "1.0.0",
    "buildNumber": 9007199254740993,
    "dependencies": {
        "zod": "^3.22.0",
        "my-lib": "file:.lnpm/my-lib",
        "express": "^4.18.0"
    }
}
`
	dir, path := newPkgJSON(t, before)

	if err := removeFromPackageJSON(dir, "my-lib"); err != nil {
		t.Fatalf("removeFromPackageJSON: %v", err)
	}

	after := readPkgJSON(t, path)
	assertDiff(t, before, after, nil, []string{`        "my-lib": "file:.lnpm/my-lib",`})
}

// Removing the last entry of the list must not leave a dangling comma behind.
func TestRemoveFromPackageJSONRemovesLastEntryWithoutDanglingComma(t *testing.T) {
	before := `{
    "version": "1.0.0",
    "name": "my-app",
    "dependencies": {
        "zod": "^3.22.0",
        "my-lib": "file:.lnpm/my-lib"
    }
}
`
	want := `{
    "version": "1.0.0",
    "name": "my-app",
    "dependencies": {
        "zod": "^3.22.0"
    }
}
`
	dir, path := newPkgJSON(t, before)

	if err := removeFromPackageJSON(dir, "my-lib"); err != nil {
		t.Fatalf("removeFromPackageJSON: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), want)
}

// Removing the only entry leaves a valid empty object.
func TestRemoveFromPackageJSONLeavesEmptyObject(t *testing.T) {
	before := `{
  "name": "my-app",
  "dependencies": {
    "my-lib": "file:.lnpm/my-lib"
  }
}
`
	want := `{
  "name": "my-app",
  "dependencies": {}
}
`
	dir, path := newPkgJSON(t, before)

	if err := removeFromPackageJSON(dir, "my-lib"); err != nil {
		t.Fatalf("removeFromPackageJSON: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), want)
}

func TestRemoveFromPackageJSONIsNoOpWhenPackageAbsent(t *testing.T) {
	before := "{\n\t\"name\": \"my-app\",\n\t\"dependencies\": {\n\t\t\"zod\": \"^3.22.0\"\n\t}\n}\n"

	dir, path := newPkgJSON(t, before)

	if err := removeFromPackageJSON(dir, "my-lib"); err != nil {
		t.Fatalf("removeFromPackageJSON: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), before)
}

func TestRemoveFromPackageJSONRemovesFromBothFields(t *testing.T) {
	before := `{
  "devDependencies": {
    "my-lib": "file:.lnpm/my-lib",
    "vitest": "^1.0.0"
  },
  "dependencies": {
    "my-lib": "file:.lnpm/my-lib",
    "zod": "^3.22.0"
  }
}
`
	want := `{
  "devDependencies": {
    "vitest": "^1.0.0"
  },
  "dependencies": {
    "zod": "^3.22.0"
  }
}
`
	dir, path := newPkgJSON(t, before)

	if err := removeFromPackageJSON(dir, "my-lib"); err != nil {
		t.Fatalf("removeFromPackageJSON: %v", err)
	}

	assertBytes(t, readPkgJSON(t, path), want)
}

func TestRemoveFromPackageJSONRejectsInvalidJSON(t *testing.T) {
	dir, _ := newPkgJSON(t, "{not valid json")

	if err := removeFromPackageJSON(dir, "my-lib"); err == nil {
		t.Fatal("expected an error for unparseable package.json, got nil")
	}
}
