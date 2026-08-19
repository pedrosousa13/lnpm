package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/store"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// RunPull re-links packages already linked in this project to whatever the
// store now holds for them. With no names it refreshes every package in
// lnpm.lock.
//
// Unlike add, pull never touches package.json: the reference there already
// points at .lnpm/<pkg>, and only the contents behind it and the lock entry
// change when the source package is republished.
func RunPull(packageNames []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	lock, err := lockfile.Load(cwd)
	if err != nil {
		return fmt.Errorf("failed to load lock file: %w", err)
	}

	// Named packages must already be linked here - pull refreshes links, it does
	// not create them. Check them all before any linking so a typo cannot leave
	// the project half-pulled.
	names := packageNames
	if len(names) == 0 {
		names = lock.List()
		if len(names) == 0 {
			fmt.Println("No linked packages to pull")
			return nil
		}
		// lock.List() ranges over a map, so sort to keep the report stable
		// between runs.
		sort.Strings(names)
	} else {
		for _, name := range names {
			if !lock.Has(name) {
				return fmt.Errorf("package %s is not linked in this project", name)
			}
		}
	}

	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to access store: %w", err)
	}

	linker := link.New(cwd)

	var failed []error
	refreshed := 0
	skipped := 0
	lockChanged := false
	lastVersion := ""

	for _, name := range names {
		entry, _ := lock.Get(name)

		pkg, err := database.GetPackageByName(name)
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: failed to look up package: %w", name, err))
			continue
		}
		if pkg == nil {
			failed = append(failed, fmt.Errorf("%s: not found in store - did you run 'lnpm publish' in the package directory", name))
			continue
		}

		fmt.Printf("Pulling %s... ", name)

		if entry.Version == pkg.Version && entry.Hash == pkg.ContentHash {
			fmt.Printf("already up to date (%s)\n", pkg.Version)
			skipped++
			continue
		}

		// Every path out of here closes the "Pulling <name>... " line, so a
		// failure names itself where it happened instead of leaving the line
		// dangling until the end-of-run report.
		files, err := s.GetFiles(pkg.Name, pkg.ContentHash)
		if err != nil {
			fmt.Printf("failed to get package files: %v\n", err)
			failed = append(failed, fmt.Errorf("%s: failed to get package files: %w", name, err))
			continue
		}

		// The link type Link reports is deliberately dropped, and no db link row
		// is written: publishing updates the existing package row in place, so
		// the row add recorded still points at the package pull just linked, and
		// no command surfaces the stored link type.
		if _, err := linker.Link(pkg.Name, pkg.StorePath, files); err != nil {
			fmt.Printf("failed to link package: %v\n", err)
			failed = append(failed, fmt.Errorf("%s: failed to link package: %w", name, err))
			continue
		}

		// Keep the original specifier: it lives only in the lock file once add
		// has rewritten package.json, and remove/retreat need it to restore the
		// dependency.
		lock.Add(name, lockfile.Package{
			Version:         pkg.Version,
			Hash:            pkg.ContentHash,
			Source:          pkg.SourcePath,
			Linked:          time.Now(),
			OriginalVersion: entry.OriginalVersion,
		})
		lockChanged = true

		fmt.Printf("updated %s -> %s\n", entry.Version, pkg.Version)
		refreshed++
		lastVersion = pkg.Version
	}

	// A failed save must not swallow the per-package failures: both are
	// reported, and both are returned.
	var saveErr error
	if lockChanged {
		if err := lock.Save(cwd); err != nil {
			saveErr = fmt.Errorf("failed to save lock file: %w", err)
		}
	}

	// The success line comes first so a partially failed run ends on the warning
	// block it exits non-zero for.
	if refreshed == 1 && len(packageNames) == 1 {
		fmt.Printf("%s Pulled %s@%s\n", iconOK(), names[0], lastVersion)
	} else if refreshed > 0 {
		fmt.Printf("%s Pulled %d package(s)\n", iconOK(), refreshed)
	} else if skipped > 0 && len(failed) == 0 {
		fmt.Printf("%s Already up to date\n", iconOK())
	}

	if len(failed) > 0 {
		fmt.Printf("\n%s Some packages failed:\n", iconWarn())
		for _, err := range failed {
			fmt.Printf("  - %v\n", err)
		}
	}
	if saveErr != nil {
		fmt.Printf("\n%s %v\n", iconWarn(), saveErr)
	}

	var pullErr error
	if len(failed) > 0 {
		pullErr = fmt.Errorf("%d of %d package(s) failed to pull", len(failed), len(names))
	}
	return errors.Join(pullErr, saveErr)
}
