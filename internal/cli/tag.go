package cli

import (
	"github.com/pedrosousa13/lnpm/internal/db"
)

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
