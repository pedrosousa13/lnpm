package pkgjson

import (
	"strings"
	"testing"
)

// These tests assert on the RAW BYTES the editors produce. A parsed-map
// comparison cannot see the bug they exist for - key order, indentation and
// large integer literals all survive a round trip through map[string]interface{}
// as far as a parsed comparison is concerned, and are all destroyed on disk.

// assertBytes compares raw output against the exact expected document,
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

func setDep(t *testing.T, src, field, name, value string) string {
	t.Helper()
	out, err := SetDep([]byte(src), field, name, value)
	if err != nil {
		t.Fatalf("SetDep: %v", err)
	}
	return string(out)
}

func removeDep(t *testing.T, src, field, name string) string {
	t.Helper()
	out, err := RemoveDep([]byte(src), field, name)
	if err != nil {
		t.Fatalf("RemoveDep: %v", err)
	}
	return string(out)
}

// The headline bug: a document whose keys are not in alphabetical order comes
// back with those keys still in the author's order, its 4-space indentation
// intact, and an integer literal beyond 2^53 untouched.
func TestSetDepOnlyRewritesTheEditedEntry(t *testing.T) {
	src := `{
    "name": "my-app",
    "version": "1.0.0",
    "private": true,
    "buildNumber": 9007199254740993,
    "dependencies": {
        "zod": "^3.22.0",
        "express": "^4.18.0"
    }
}
`
	want := `{
    "name": "my-app",
    "version": "1.0.0",
    "private": true,
    "buildNumber": 9007199254740993,
    "dependencies": {
        "zod": "^3.22.0",
        "express": "^4.18.0",
        "my-lib": "file:.lnpm/my-lib"
    }
}
`
	assertBytes(t, setDep(t, src, "dependencies", "my-lib", "file:.lnpm/my-lib"), want)
}

func TestSetDepPreservesTabIndentation(t *testing.T) {
	src := "{\n\t\"name\": \"my-app\",\n\t\"dependencies\": {\n\t\t\"zod\": \"^3.22.0\"\n\t}\n}\n"
	want := "{\n\t\"name\": \"my-app\",\n\t\"dependencies\": {\n\t\t\"zod\": \"^3.22.0\",\n\t\t\"my-lib\": \"file:.lnpm/my-lib\"\n\t}\n}\n"

	assertBytes(t, setDep(t, src, "dependencies", "my-lib", "file:.lnpm/my-lib"), want)
}

// An entry that already exists keeps its position in the object rather than
// being re-sorted to wherever alphabetical order would put it.
func TestSetDepReplacesExistingEntryInPlace(t *testing.T) {
	src := `{
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
	assertBytes(t, setDep(t, src, "dependencies", "my-lib", "link:.lnpm/my-lib"), want)
}

// Setting an entry that appears twice must leave exactly one behind, at the
// position encoding/json would have read: the last one.
func TestSetDepCollapsesDuplicateEntries(t *testing.T) {
	src := `{
  "dependencies": {
    "my-lib": "^1.0.0",
    "zod": "^3.22.0",
    "my-lib": "file:.lnpm/my-lib"
  }
}
`
	want := `{
  "dependencies": {
    "zod": "^3.22.0",
    "my-lib": "^2.5.0"
  }
}
`
	assertBytes(t, setDep(t, src, "dependencies", "my-lib", "^2.5.0"), want)
}

func TestSetDepCreatesMissingField(t *testing.T) {
	src := `{
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
	assertBytes(t, setDep(t, src, "dependencies", "my-lib", "file:.lnpm/my-lib"), want)
}

func TestSetDepFillsEmptyField(t *testing.T) {
	src := `{
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
	assertBytes(t, setDep(t, src, "dependencies", "my-lib", "file:.lnpm/my-lib"), want)
}

// A document with no keys at all has nothing to copy an indent from, so the new
// field falls back to the 2-space default.
func TestSetDepFillsEmptyDocument(t *testing.T) {
	want := `{
  "dependencies": {
    "my-lib": "file:.lnpm/my-lib"
  }
}
`
	assertBytes(t, setDep(t, "{}\n", "dependencies", "my-lib", "file:.lnpm/my-lib"), want)
}

// A minified document has no lines to match, so the new entry goes inline
// beside its siblings rather than dragging a newline into the file.
func TestSetDepKeepsMinifiedDocumentInline(t *testing.T) {
	src := `{"version":"1.0.0","name":"my-app","dependencies":{"zod":"^3.22.0"}}`
	want := `{"version":"1.0.0","name":"my-app","dependencies":{"zod":"^3.22.0", "my-lib": "file:.lnpm/my-lib"}}`

	assertBytes(t, setDep(t, src, "dependencies", "my-lib", "file:.lnpm/my-lib"), want)
}

// CI runs Windows, where package.json files routinely have CRLF line endings.
// The inserted line must use the document's own ending, not a bare LF.
func TestSetDepPreservesCRLFLineEndings(t *testing.T) {
	src := "{\r\n  \"name\": \"my-app\",\r\n  \"dependencies\": {\r\n    \"zod\": \"^3.22.0\"\r\n  }\r\n}\r\n"
	want := "{\r\n  \"name\": \"my-app\",\r\n  \"dependencies\": {\r\n    \"zod\": \"^3.22.0\",\r\n    \"my-lib\": \"file:.lnpm/my-lib\"\r\n  }\r\n}\r\n"

	assertBytes(t, setDep(t, src, "dependencies", "my-lib", "file:.lnpm/my-lib"), want)
}

// A field holding something other than an object is replaced wholesale, as the
// map-based code this replaced used to.
func TestSetDepReplacesNonObjectField(t *testing.T) {
	src := `{
  "name": "my-app",
  "dependencies": null
}
`
	want := `{
  "name": "my-app",
  "dependencies": {
    "my-lib": "file:.lnpm/my-lib"
  }
}
`
	assertBytes(t, setDep(t, src, "dependencies", "my-lib", "file:.lnpm/my-lib"), want)
}

// add and remove rely on an unparseable document failing the edit, so that they
// abort instead of silently losing the file.
func TestSetDepRejectsInvalidJSON(t *testing.T) {
	if _, err := SetDep([]byte("{not valid json"), "dependencies", "my-lib", "^1.0.0"); err == nil {
		t.Fatal("expected an error for unparseable package.json, got nil")
	}
}

func TestRemoveDepOnlyRewritesTheDeletedEntry(t *testing.T) {
	src := `{
    "name": "my-app",
    "buildNumber": 9007199254740993,
    "dependencies": {
        "zod": "^3.22.0",
        "my-lib": "file:.lnpm/my-lib",
        "express": "^4.18.0"
    }
}
`
	want := `{
    "name": "my-app",
    "buildNumber": 9007199254740993,
    "dependencies": {
        "zod": "^3.22.0",
        "express": "^4.18.0"
    }
}
`
	assertBytes(t, removeDep(t, src, "dependencies", "my-lib"), want)
}

// Removing the last entry of the list must not leave a dangling comma behind.
func TestRemoveDepRemovesLastEntryWithoutDanglingComma(t *testing.T) {
	src := `{
    "dependencies": {
        "zod": "^3.22.0",
        "my-lib": "file:.lnpm/my-lib"
    }
}
`
	want := `{
    "dependencies": {
        "zod": "^3.22.0"
    }
}
`
	assertBytes(t, removeDep(t, src, "dependencies", "my-lib"), want)
}

// Removing the only entry leaves a valid empty object.
func TestRemoveDepLeavesEmptyObject(t *testing.T) {
	src := `{
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
	assertBytes(t, removeDep(t, src, "dependencies", "my-lib"), want)
}

// Duplicate keys are legal JSON, and encoding/json resolves them last-wins. A
// removal that took out only one copy would leave the dependency live.
func TestRemoveDepRemovesEveryDuplicateEntry(t *testing.T) {
	src := `{"dependencies":{"my-lib":"file:.lnpm/my-lib","zod":"^3.22.0","my-lib":"file:.lnpm/my-lib"}}`
	want := `{"dependencies":{"zod":"^3.22.0"}}`

	assertBytes(t, removeDep(t, src, "dependencies", "my-lib"), want)
}

func TestRemoveDepIsNoOpWhenEntryAbsent(t *testing.T) {
	src := "{\n\t\"name\": \"my-app\",\n\t\"dependencies\": {\n\t\t\"zod\": \"^3.22.0\"\n\t}\n}\n"

	assertBytes(t, removeDep(t, src, "dependencies", "my-lib"), src)
}

func TestRemoveDepIsNoOpWhenFieldAbsent(t *testing.T) {
	src := "{\n  \"name\": \"my-app\"\n}\n"

	assertBytes(t, removeDep(t, src, "dependencies", "my-lib"), src)
}

func TestRemoveDepRejectsInvalidJSON(t *testing.T) {
	if _, err := RemoveDep([]byte("{not valid json"), "dependencies", "my-lib"); err == nil {
		t.Fatal("expected an error for unparseable package.json, got nil")
	}
}

func TestHasDep(t *testing.T) {
	src := []byte(`{"dependencies":{"zod":"^3.22.0"},"devDependencies":{"vitest":"^1.0.0"},"peerDependencies":null}`)

	cases := []struct {
		field, name string
		want        bool
	}{
		{"dependencies", "zod", true},
		{"dependencies", "vitest", false},
		{"devDependencies", "vitest", true},
		{"optionalDependencies", "zod", false},
		{"peerDependencies", "zod", false},
	}
	for _, c := range cases {
		got, err := HasDep(src, c.field, c.name)
		if err != nil {
			t.Fatalf("HasDep(%s, %s): %v", c.field, c.name, err)
		}
		if got != c.want {
			t.Errorf("HasDep(%s, %s) = %v, want %v", c.field, c.name, got, c.want)
		}
	}
}

// A duplicated key is present, whichever copy is found first.
func TestHasDepFindsDuplicatedEntry(t *testing.T) {
	src := []byte(`{"dependencies":{"my-lib":"^1.0.0","my-lib":"^2.0.0"}}`)

	got, err := HasDep(src, "dependencies", "my-lib")
	if err != nil {
		t.Fatalf("HasDep: %v", err)
	}
	if !got {
		t.Fatal("HasDep = false, want true")
	}
}

func TestHasDepRejectsInvalidJSON(t *testing.T) {
	if _, err := HasDep([]byte("{not valid json"), "dependencies", "my-lib"); err == nil {
		t.Fatal("expected an error for unparseable package.json, got nil")
	}
}

// lnpm has always left package.json with a trailing newline, and the newline it
// adds is the one the document already uses.
func TestEnsureTrailingNewline(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"missing", "{}", "{}\n"},
		{"present", "{}\n", "{}\n"},
		{"missing with CRLF document", "{\r\n  \"a\": 1\r\n}", "{\r\n  \"a\": 1\r\n}\r\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertBytes(t, string(EnsureTrailingNewline([]byte(c.src))), c.want)
		})
	}
}
