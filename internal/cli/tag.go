package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pedrosousa13/lnpm/internal/db"
)

// tagsNaming renders every tag in tags that points at hash, as a trailing
// " [beta, latest]", and nothing when none does.
//
// Sorted, because bolt hands back a map and an unordered listing would reshuffle
// itself between runs of the same command over an unchanged store. The default
// tag is included here, unlike in the running commentary the other helpers
// produce: this listing exists to say which version each channel names, and
// latest is the channel most readers came to find.
func tagsNaming(tags map[string]string, hash string) string {
	var naming []string
	for tag, tagged := range tags {
		if tagged == hash {
			naming = append(naming, tag)
		}
	}
	if len(naming) == 0 {
		return ""
	}
	sort.Strings(naming)
	return " [" + strings.Join(naming, ", ") + "]"
}

// projectLinks maps each package name a project consumes to the link row that
// records it. An absent name is a package this project has no link for.
type projectLinks map[string]*db.Link

// tag returns the channel the project follows for name, reading an absent or
// untagged link as the default one.
func (p projectLinks) tag(name string) string {
	if l, ok := p[name]; ok {
		return l.Tag
	}
	return ""
}

// linksOfProject reads the links the project at path holds, keyed by package
// name.
//
// Looking a package up by name instead is what every command used to do, and it
// stopped being enough once two versions of a name could be in the store at
// once: the name index mirrors the default tag, so a name lookup answers with
// the version tagged latest whatever version the project is actually on. The
// link row is the only record of which one that is, and of which channel the
// project asked for - the lock file says what was linked, not what was
// requested.
//
// A project not in the database - a lock file written by an add whose database
// write failed, say - is not an error. It holds no links, so every caller falls
// back to what it would have done anyway.
func linksOfProject(database *db.DB, path string) (projectLinks, error) {
	proj, err := database.GetProjectByPath(path)
	if err != nil {
		return nil, fmt.Errorf("failed to look up this project: %w", err)
	}
	if proj == nil {
		return nil, nil
	}

	links, err := database.GetLinksForProject(proj.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to read this project's links: %w", err)
	}

	packages, err := database.ListPackages()
	if err != nil {
		return nil, fmt.Errorf("failed to list packages: %w", err)
	}
	nameByID := make(map[int64]string, len(packages))
	for _, pkg := range packages {
		nameByID[pkg.ID] = pkg.Name
	}

	held := make(projectLinks, len(links))
	for _, l := range links {
		if name, ok := nameByID[l.PackageID]; ok {
			held[name] = l
		}
	}
	return held, nil
}

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

// isDefaultTag reports whether tag is the one every lookup by name resolves
// through. An empty tag counts: that is how a link written before tags existed
// reads, and what an unset --tag carries.
func isDefaultTag(tag string) bool {
	return tag == "" || tag == db.DefaultTag
}

// tagNote names a tag for output, and says nothing at all for the default one.
//
// The default tag is left unsaid because every publish moves it: spelling it out
// would add a clause to every line lnpm has ever printed while saying only what
// was already true. A tag is worth naming exactly when someone chose it.
func tagNote(tag string) string {
	if isDefaultTag(tag) {
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
