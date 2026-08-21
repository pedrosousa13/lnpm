package cli

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/hooks"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/store"
)

// pushTag works out which channel a push publishes to when the user has not
// said.
//
// Push has no channel of its own to go on. It re-publishes the working tree, and
// nothing records which channel that tree was last published to. What is
// recorded is which tags name the build already in the store, and for an
// unchanged working tree that answers the question exactly: a push over the
// build tagged beta is a push to beta. That is the case worth getting right -
// without it a bare push in a package whose current build is a pre-release moves
// the default tag onto it and releases something nobody asked to release.
//
// Content the store does not hold is named by no tag and falls back to the
// default one, so editing a pre-release and pushing it does still move the
// default tag. Nothing in the store can be read to infer otherwise, and
// inferring it from the last publish would mean inferring it from state lnpm
// does not keep; --tag is what says it instead.
//
// Content several channels name is refused rather than resolved by picking one.
// Which channel a build belongs to is not a thing to guess when the guess
// releases software.
func pushTag(database *db.DB, name, hash string) (string, error) {
	tags, err := database.TagsForPackage(name)
	if err != nil {
		return "", fmt.Errorf("failed to read the tags of %s: %w", name, err)
	}

	var naming []string
	for _, tag := range tagsNamingList(tags, hash) {
		if tag == db.DefaultTag {
			// Already what the default tag names, so this push moves nothing.
			return db.DefaultTag, nil
		}
		naming = append(naming, tag)
	}

	switch len(naming) {
	case 0:
		return db.DefaultTag, nil
	case 1:
		return naming[0], nil
	default:
		return "", fmt.Errorf("the build of %s in your working tree is named by the tags %s, so which channel this push is for is ambiguous: re-run with --tag",
			name, strings.Join(naming, ", "))
	}
}

// RunPush executes the push command, publishing to the channel the build
// already in the store carries.
func RunPush(skipHooks bool) error {
	return RunPushTagged(skipHooks, "")
}

// RunPushTagged is RunPush, publishing to tag. An empty tag means work the
// channel out from the store, which is what a bare `lnpm push` does; see
// pushTag.
func RunPushTagged(skipHooks bool, tag string) error {
	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Get database
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Read package.json to get package name
	pkgJSON, err := pack.ReadPackageJSON(cwd)
	if err != nil {
		return fmt.Errorf("failed to read package.json: %w", err)
	}

	// Get existing package from database
	pkg, err := database.GetPackageByName(pkgJSON.Name)
	if err != nil {
		return fmt.Errorf("failed to look up package: %w", err)
	}

	// If not published yet, delegate to publish
	if pkg == nil {
		fmt.Printf("Package %s not published yet, publishing...\n", pkgJSON.Name)

		// Run prepare scripts before packing
		if err := hooks.RunPrepare(cwd, skipHooks); err != nil {
			return fmt.Errorf("prepare hook failed: %w", err)
		}

		_, files, err := pack.Pack(cwd)
		if err != nil {
			return fmt.Errorf("failed to pack: %w", err)
		}
		cleanup, err := pack.RewriteWorkspaceDeps(cwd, files)
		defer cleanup()
		if err != nil {
			// Deliberately unwrapped: RewriteWorkspaceDeps' errors are already
			// self-contained, so wrapping them here would only stutter.
			return err
		}
		// Nothing is in the store to read a channel off, so an unset --tag can
		// only mean the default one.
		if tag == "" {
			tag = db.DefaultTag
		}
		return finishPublish(cwd, pkgJSON, files, database, false, tag)
	}

	// Always run prepare scripts before packing
	if err := hooks.RunPrepare(cwd, skipHooks); err != nil {
		return fmt.Errorf("prepare hook failed: %w", err)
	}

	// Pack files
	_, files, err := pack.Pack(cwd)
	if err != nil {
		return fmt.Errorf("failed to pack: %w", err)
	}

	// Resolve any workspace: dependency specifiers before hashing, so a push
	// stores the same resolved package.json a publish would.
	cleanup, err := pack.RewriteWorkspaceDeps(cwd, files)
	defer cleanup()
	if err != nil {
		// Deliberately unwrapped, as above.
		return err
	}

	// Calculate content hash
	newHash := pack.HashFiles(files)

	if tag == "" {
		tag, err = pushTag(database, pkgJSON.Name, newHash)
		if err != nil {
			return err
		}
	}

	fmt.Printf("Pushing %s@%s%s...\n", pkgJSON.Name, pkgJSON.Version, tagSuffix(tag))

	// Get store
	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to access store: %w", err)
	}

	// Check if we need to store (hash changed or doesn't exist)
	var storePath string
	if pkg.ContentHash != newHash || !s.Exists(pkgJSON.Name, newHash) {
		// Store the updated package
		storePath, err = s.Store(pkgJSON.Name, newHash, files, cwd)
		if err != nil {
			return fmt.Errorf("failed to store package: %w", err)
		}
	} else {
		// Reuse existing store path
		storePath = s.PackagePath(pkgJSON.Name, newHash)
	}

	// Calculate total size
	var totalSize int64
	for _, f := range files {
		totalSize += f.Size
	}

	// Update package in database
	pkg.Version = pkgJSON.Version
	pkg.ContentHash = newHash
	pkg.SourcePath = cwd
	pkg.StorePath = storePath
	pkg.FilesCount = len(files)
	pkg.TotalSize = totalSize

	// The package row and its file manifest go in together, for the reason
	// finishPublish gives. The manifest is rewritten whether or not the content
	// hash moved: skipping it when nothing changed would save one bucket write
	// and make "the file rows always describe the package row" conditional,
	// which is the property a relink's file-level decisions rest on.
	fileEntries := make([]*db.FileEntry, len(files))
	for i, f := range files {
		fileEntries[i] = &db.FileEntry{
			RelativePath: f.RelPath,
			ContentHash:  f.ContentHash,
			Size:         f.Size,
			Mode:         f.Mode,
			ModTime:      f.ModTime,
		}
	}
	if err := database.InsertPackageWithFilesTagged(pkg, fileEntries, tag); err != nil {
		return fmt.Errorf("failed to update package: %w", err)
	}

	// Get linked projects
	projects, err := database.GetProjectsForPackage(pkg.ID)
	if err != nil {
		return fmt.Errorf("failed to get linked projects: %w", err)
	}

	if len(projects) == 0 {
		fmt.Printf("%s Updated %s@%s in store\n", iconOK(), pkgJSON.Name, pkgJSON.Version)
		fmt.Println("  No linked projects to update")
		return nil
	}

	// Push to all linked projects
	fmt.Printf("Updating %d linked projects...\n", len(projects))

	// Convert files to FileInfo once (avoid walking store per project)
	fileData := make([]pack.FileEntryData, len(files))
	for i, f := range files {
		fileData[i] = pack.FileEntryData{
			RelPath: f.RelPath,
			Size:    f.Size,
			Mode:    f.Mode,
			Hash:    f.ContentHash,
		}
	}
	storeFiles := pack.FileInfoFromStore(storePath, fileData)

	// Link to all projects in parallel
	type result struct {
		path    string
		skipped bool
		link    link.Result
		err     error
	}
	results := make(chan result, len(projects))
	var wg sync.WaitGroup

	for _, proj := range projects {
		wg.Add(1)
		go func(p *db.Project) {
			defer wg.Done()
			linker := link.New(p.Path)
			// A project that added this package with --link already resolves to
			// the source directory being pushed. Relinking it from the store
			// would replace its live link with a snapshot copy and silently end
			// the live updates it was added for.
			if linker.IsLiveLinked(pkg.Name) {
				results <- result{path: p.Path, skipped: true}
				return
			}
			linkRes, err := linker.Link(pkg.Name, storePath, storeFiles)
			results <- result{path: p.Path, link: linkRes, err: err}
		}(proj)
	}

	// Wait for all links to complete
	wg.Wait()
	close(results)

	// Print results in order received (not deterministic order)
	successCount := 0
	skippedCount := 0
	failedCount := 0
	for res := range results {
		switch {
		case res.err != nil:
			fmt.Printf("  %s %s: %v\n", iconFail(), res.path, res.err)
			failedCount++
		case res.skipped:
			fmt.Printf("  %s %s: skipped (live link to source)\n", iconOK(), res.path)
			skippedCount++
		default:
			fmt.Printf("  %s %s (%d changed, %d unchanged)\n", iconOK(), res.path, res.link.Changed, res.link.Unchanged)
			successCount++
		}
	}

	// The denominator is every project considered, matching the count announced
	// above. Live-linked projects are reported as skipped instead of being taken
	// out of the total: removing them made the two lines contradict each other,
	// and an all-live push report "Pushed to 0/0 projects".
	fmt.Printf("\nPushed to %d/%d projects", successCount, len(projects))
	if skippedCount > 0 {
		fmt.Printf(" (%d skipped: live link to source)", skippedCount)
	}
	fmt.Println()

	if failedCount > 0 {
		return fmt.Errorf("push failed for %d of %d project(s)", failedCount, len(projects))
	}

	return nil
}
