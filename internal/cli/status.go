package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/ui"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// RunStatus executes the status command
func RunStatus() error {
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Get all packages in store
	packages, err := database.ListPackages()
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}

	// Tags of each name, read once per name however many of its versions the
	// store holds. The table lists one row per retained version, so without the
	// tags two rows of one name say nothing about which of them a plain add
	// resolves to - and a name whose tags cannot be read would print rows that
	// claim it has none.
	tagsByName, err := tagsOfPackages(database, packages)
	if err != nil {
		return err
	}

	// Print packages section
	fmt.Println(ui.Header("📦", "Published Packages"))
	if len(packages) == 0 {
		fmt.Println("  (none)")
	} else {
		// Print header
		// The rule is as wide as the columns and their separators: 25+14+10+20+20
		// plus the four single spaces between them. The tag column is wide enough
		// for three tags - "latest, beta, next" is 18 - because a version that
		// several channels name is the one a reader most needs spelled out.
		fmt.Printf("  %-25s %-14s %-10s %-20s %-20s\n", "NAME", "VERSION", "HASH", "TAGS", "PUBLISHED")
		fmt.Printf("  %s\n", ui.HRule(93))

		for _, pkg := range packages {
			fmt.Printf("  %-25s %-14s %-10s %-20s %-20s\n",
				truncate(pkg.Name, 25),
				truncate(pkg.Version, 14),
				shortHash(pkg.ContentHash),
				truncate(strings.Join(tagsNamingList(tagsByName[pkg.Name], pkg.ContentHash), ", "), 20),
				formatTimeAgo(pkg.UpdatedAt),
			)
		}
	}
	fmt.Println()

	// Get all links and group by project
	type projectInfo struct {
		path     string
		pm       string
		packages []string
	}
	projectMap := make(map[string]*projectInfo)

	for _, pkg := range packages {
		projects, err := database.GetProjectsForPackage(pkg.ID)
		if err != nil {
			return fmt.Errorf("failed to get the projects on %s@%s: %w", pkg.Name, pkg.Version, err)
		}
		for _, proj := range projects {
			if _, ok := projectMap[proj.Path]; !ok {
				projectMap[proj.Path] = &projectInfo{
					path: proj.Path,
					pm:   proj.PackageManager,
				}
			}
			projectMap[proj.Path].packages = append(projectMap[proj.Path].packages, pkg.Name)
		}
	}

	// Print links section
	fmt.Println(ui.Header("🔗", "Active Links"))
	if len(projectMap) == 0 {
		fmt.Println("  (none)")
	} else {
		fmt.Printf("  %-40s %-8s %-30s\n", "PROJECT", "PM", "PACKAGES")
		fmt.Printf("  %s\n", ui.HRule(80))

		for _, proj := range projectMap {
			fmt.Printf("  %-40s %-8s %-30s\n",
				truncate(proj.path, 40),
				proj.pm,
				truncate(strings.Join(proj.packages, ", "), 30),
			)
		}
	}
	fmt.Println()

	// Check current directory for local status
	cwd, err := os.Getwd()
	if err != nil {
		return nil // Non-fatal
	}

	lock, err := lockfile.Load(cwd)
	if err == nil && len(lock.List()) > 0 {
		fmt.Println(ui.Header("📍", "Current Project"))
		fmt.Printf("  %s\n", cwd)
		fmt.Printf("  Linked packages:\n")
		// The pin is shown here and not in the tables above, and it is read from
		// the lock file rather than from the link row. Both follow from what
		// this block is: the one part of status that is about the project the
		// user is standing in, which is the only project whose lock file status
		// has. A display may read the pin from there - the database row is the
		// authority, but nothing here acts on it - while the tables above cover
		// every project in the store and would need a link lookup per project to
		// say the same thing. See ADR-0006.
		//
		// It is worth saying at all because the state was unnameable before it:
		// `lnpm list <pkg> --versions` shows a project sitting on a superseded
		// build, but that is the symptom, and it does not distinguish a project
		// that chose to be there from one that has simply not pulled.
		for _, name := range lock.List() {
			entry, _ := lock.Get(name)
			pin := ""
			if entry.Pinned {
				pin = ", pinned"
			}
			fmt.Printf("    • %s@%s (hash: %s%s)\n", name, entry.Version, shortHash(entry.Hash), pin)
		}
	}

	return nil
}

// RunList executes the list command
func RunList(showStore bool, packageName string, showProjects bool) error {
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if showStore {
		// List all packages in store
		packages, err := database.ListPackages()
		if err != nil {
			return fmt.Errorf("failed to list packages: %w", err)
		}

		if len(packages) == 0 {
			fmt.Println("No packages in store")
			return nil
		}

		tagsByName, err := tagsOfPackages(database, packages)
		if err != nil {
			return err
		}

		fmt.Println("Packages in store:")
		for _, pkg := range packages {
			fmt.Printf("  %s@%s (%s)%s\n", pkg.Name, pkg.Version, shortHash(pkg.ContentHash),
				tagsNaming(tagsByName[pkg.Name], pkg.ContentHash))
		}
		return nil
	}

	if packageName != "" && !showProjects {
		return fmt.Errorf("use --projects to list which projects use %q, or --versions to list what it can be rolled back to", packageName)
	}

	if packageName != "" && showProjects {
		// Every version of the name, not the one it resolves to. The name index
		// mirrors the default tag, so resolving by name would answer with the
		// stable release and leave every project that asked for a channel out of
		// a listing whose whole job is naming who consumes this package.
		packages, err := database.ListPackages()
		if err != nil {
			return fmt.Errorf("failed to list packages: %w", err)
		}
		var versions []*db.Package
		for _, pkg := range packages {
			if pkg.Name == packageName {
				versions = append(versions, pkg)
			}
		}
		if len(versions) == 0 {
			return fmt.Errorf("package %s not found", packageName)
		}

		tags, err := database.TagsForPackage(packageName)
		if err != nil {
			return fmt.Errorf("failed to read the tags of %s: %w", packageName, err)
		}

		// Each line says which build the project is on, because that is what
		// differs between them now and what a maintainer looking at this list is
		// deciding from.
		var lines []string
		for _, version := range versions {
			projects, err := database.GetProjectsForPackage(version.ID)
			if err != nil {
				return fmt.Errorf("failed to get projects: %w", err)
			}
			for _, proj := range projects {
				lines = append(lines, fmt.Sprintf("  %s (%s) -> %s@%s%s",
					proj.Path, proj.PackageManager, packageName, version.Version,
					tagsNaming(tags, version.ContentHash)))
			}
		}

		if len(lines) == 0 {
			fmt.Printf("No projects using %s\n", packageName)
			return nil
		}

		// Sorted, so a store holding several versions lists the same way on
		// every run rather than in whatever order the records were written.
		sort.Strings(lines)
		fmt.Printf("Projects using %s:\n", packageName)
		for _, line := range lines {
			fmt.Println(line)
		}
		return nil
	}

	// List packages in current project
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	lock, err := lockfile.Load(cwd)
	if err != nil {
		return fmt.Errorf("failed to load lock file: %w", err)
	}

	packages := lock.List()
	if len(packages) == 0 {
		fmt.Println("No linked packages in current project")
		return nil
	}

	fmt.Println("Linked packages:")
	for _, name := range packages {
		entry, _ := lock.Get(name)
		fmt.Printf("  %s@%s\n", name, entry.Version)
		fmt.Printf("    Hash: %s\n", shortHash(entry.Hash))
		fmt.Printf("    Source: %s\n", entry.Source)
		fmt.Printf("    Linked: %s\n", formatTimeAgo(entry.Linked))
	}

	return nil
}

// RunListVersions lists every version of one package the store still holds:
// its content hash, its version, when it was published, the tags naming it and
// the projects on it.
//
// This is a separate listing rather than another shape for `--store` because it
// answers a different question. `--store` and `status` say what is in the store;
// this says what one package can be rolled back to, which is about the rows the
// default tag has moved off - the ones every other listing shows without
// distinguishing.
//
// The set is exactly what gc has not collected, because a version's record and
// its store entry go together. There is no separate "ever published" history to
// consult and nothing here retains anything that gc would otherwise take.
//
// Every read this makes is surfaced rather than skipped: a listing whose whole
// job is telling a user which build to roll back to must not report a version as
// untagged or unconsumed because a read failed, because that is
// indistinguishable on screen from the version being safe to leave behind. The
// history itself goes further and fails on a version record it cannot parse, for
// the reason GetPackageVersions gives.
//
// The consumer lookup holds that line too, since #355: a link row or an index
// entry it cannot read is an error rather than a consumer quietly missing from
// the row. The one shape it still passes over is a link naming a project record
// that is gone, which is an orphaned link rather than damage - doctor reports it
// and `gc --fix-links` removes it.
func RunListVersions(packageName string) error {
	if packageName == "" {
		return fmt.Errorf("--versions needs a package name: try 'lnpm list <package> --versions'")
	}

	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	versions, err := database.GetPackageVersions(packageName)
	if err != nil {
		return fmt.Errorf("failed to read the versions of %s: %w", packageName, err)
	}
	if len(versions) == 0 {
		return fmt.Errorf("package %s not found", packageName)
	}

	tags, err := database.TagsForPackage(packageName)
	if err != nil {
		return fmt.Errorf("failed to read the tags of %s: %w", packageName, err)
	}

	fmt.Printf("%s versions:\n", packageName)
	// The rule is as wide as the columns and their separators: 10+14+20+20 plus
	// the four single spaces between them, plus the last heading, which is the
	// one column left unpadded because a project path is longer than any width
	// worth reserving for it.
	fmt.Printf("  %-10s %-14s %-20s %-20s %s\n", "HASH", "VERSION", "PUBLISHED", "TAGS", "LINKED IN")
	fmt.Printf("  %s\n", ui.HRule(77))

	for _, version := range versions {
		consumers, err := database.GetProjectsForPackage(version.ID)
		if err != nil {
			return fmt.Errorf("failed to get the projects on %s@%s: %w", packageName, version.Version, err)
		}
		// Trimmed because the last two columns are both often empty - most
		// versions carry no tag and have no consumer - and a padded row would
		// then be mostly trailing spaces.
		fmt.Println(strings.TrimRight(fmt.Sprintf("  %-10s %-14s %-20s %-20s %s",
			shortHash(version.ContentHash),
			truncate(version.Version, 14),
			formatTimeAgo(version.UpdatedAt),
			truncate(strings.Join(tagsNamingList(tags, version.ContentHash), ", "), 20),
			consumersNaming(consumers),
		), " "))
	}

	return nil
}

// namedConsumers is how many projects consumersNaming spells out before falling
// back to a count.
const namedConsumers = 3

// consumersNaming renders the projects on a version, and nothing when none are.
//
// Projects are named rather than counted, because this column exists so a
// maintainer deciding whether to roll a version back can see who it would move,
// and a count does not answer that. They are named by path, as `lnpm list
// --projects` and `lnpm status` both name them: Name is a basename, so two
// projects called myapp render identically and the column stops distinguishing
// the thing it exists to distinguish. Paths are left untruncated for the same
// reason - a path cut to a column width loses the segment that tells two of them
// apart.
//
// Sorted, because the links come back in whatever order the index holds them and
// an unordered listing would reshuffle itself between runs over an unchanged
// store. Capped at namedConsumers, because a widely consumed package would
// otherwise put a line of unbounded length on every row; `lnpm list <pkg>
// --projects` is the listing that names them all.
func consumersNaming(projects []*db.Project) string {
	if len(projects) == 0 {
		return ""
	}
	paths := make([]string, 0, len(projects))
	for _, proj := range projects {
		paths = append(paths, proj.Path)
	}
	sort.Strings(paths)

	if len(paths) > namedConsumers {
		omitted := len(paths) - namedConsumers
		paths = append(paths[:namedConsumers:namedConsumers], fmt.Sprintf("and %d more", omitted))
	}
	return strings.Join(paths, ", ")
}

// truncate truncates a string to maxLen runes, appending "..." when shortened.
// It operates on runes so multibyte UTF-8 characters are never split.
func truncate(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(r[:maxLen])
	}
	return string(r[:maxLen-3]) + "..."
}

// formatTimeAgo formats a time as "X ago"
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case d < 7*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}
