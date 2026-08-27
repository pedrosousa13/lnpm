package cli

import (
	"fmt"
	"path/filepath"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/ui"
)

// RunForget executes the forget command.
func RunForget(path string, yes bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve %s: %w", path, err)
	}

	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	proj, err := database.GetProjectByPath(abs)
	if err != nil {
		return fmt.Errorf("failed to read the project at %s: %w", abs, err)
	}

	if proj == nil {
		return fmt.Errorf("lnpm has no record of a project at %s", abs)
	}

	// Refuse a project that is still there. classifyProjectDir answers projectLive
	// both when the directory stats and when the stat failed for a reason that
	// establishes nothing - a permission or I/O error - and both belong on this
	// side of the line: forget is irreversible, and "I could not tell" is not a
	// reason to drop a record. The other two states are the ones this command is
	// for. projectUnreachable is the drive that is gone for good, which is the
	// whole of #382; projectGone is a project genuinely deleted, where gc would
	// have collected the versions on its own but the record is still there to
	// remove.
	if state, _ := classifyProjectDir(proj.Path, proj.Device); state == projectLive {
		return fmt.Errorf("%s is still there, so there is nothing to forget; run 'lnpm retreat' inside it to unlink lnpm from a project that is still on disk", proj.Path)
	}

	links, err := database.GetLinksForProject(proj.ID)
	if err != nil {
		return fmt.Errorf("failed to read the links of %s: %w", proj.Path, err)
	}

	if !confirm(fmt.Sprintf("Permanently remove the record of %s and its %d link(s)?", proj.Path, len(links)), yes) {
		fmt.Println("Skipped forgetting the project.")
		return nil
	}

	if err := database.DeleteProject(proj.ID); err != nil {
		return err
	}

	fmt.Printf("%s Forgot %s and its %d link(s)\n", ui.IconOK(), proj.Path, len(links))
	// Nothing left the store, so the user is one command short of the space they
	// came for. gc is named rather than run: removing store entries is the
	// destructive half, and it stays behind gc's own confirmation instead of
	// being smuggled into a command whose whole promise is that it only drops a
	// record.
	fmt.Println("  Run 'lnpm gc' to collect the version(s) nothing else consumes.")
	return nil
}
