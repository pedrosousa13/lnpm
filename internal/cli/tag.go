package cli

import (
	"fmt"

	"github.com/pedrosousa13/lnpm/internal/db"
)

// RunTag manages a dist-tag on a package already in the store, without
// republishing it.
//
// Setting points the tag at the version the package resolves to by name - the
// one tagged latest - because that is the version a publish just wrote and the
// only one a user can name without knowing content hashes. Deleting takes the
// tag off and leaves that version in the store, still reachable by name.
//
// The database refuses two things, and both refusals are passed through rather
// than worked around: a tag on a version no record holds, and a delete of the
// default tag, which the name index mirrors.
func RunTag(packageName, tag string, del bool) error {
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if del {
		if err := database.DeleteTag(packageName, tag); err != nil {
			return err
		}
		fmt.Printf("%s Removed tag %s from %s\n", iconOK(), tag, packageName)
		return nil
	}

	pkg, err := database.GetPackageByName(packageName)
	if err != nil {
		return fmt.Errorf("failed to look up package: %w", err)
	}
	if pkg == nil {
		return fmt.Errorf("package %s not found in store. Did you run 'lnpm publish' in the package directory?", packageName)
	}

	if err := database.SetTag(packageName, tag, pkg.ContentHash); err != nil {
		return err
	}

	fmt.Printf("%s Tagged %s@%s as %s\n", iconOK(), pkg.Name, pkg.Version, tag)
	return nil
}

// tagNote names a tag for output, and says nothing at all for the default one.
//
// The default tag is left unsaid because every publish moves it: spelling it out
// would add a clause to every line lnpm has ever printed while saying only what
// was already true. A tag is worth naming exactly when someone chose it.
func tagNote(tag string) string {
	if tag == "" || tag == db.DefaultTag {
		return ""
	}
	return "tag: " + tag
}

// tagSuffix is tagNote as a trailing parenthetical, for a line that has none of
// its own.
func tagSuffix(tag string) string {
	note := tagNote(tag)
	if note == "" {
		return ""
	}
	return " (" + note + ")"
}

// tagClause is tagNote folded into a parenthetical a line already carries, so a
// tagged publish reads "(3 files, tag: beta)" rather than trailing a second pair
// of brackets behind the first.
func tagClause(tag string) string {
	note := tagNote(tag)
	if note == "" {
		return ""
	}
	return ", " + note
}
