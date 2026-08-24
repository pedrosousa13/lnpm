package cli

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/hooks"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/store"
	"github.com/pedrosousa13/lnpm/internal/validation"
	"github.com/pedrosousa13/lnpm/internal/workspace"
)

// PublishOptions is the full request a publish run is given: every flag the
// command accepts, in one value. It is what RunPublishWith takes, so a new flag
// is a new field rather than a new parameter on every caller.
type PublishOptions struct {
	Push           bool
	All            bool
	SkipHooks      bool
	SkipValidation bool
	// DryRun reports what would be packed and returns without writing.
	DryRun bool
	Tag    string
}

// RunPublish executes the publish command, moving the default tag onto what it
// writes.
//
// It and RunPublishTagged below are both test-only now: cobra builds a
// PublishOptions and calls RunPublishWith directly, so nothing in the command
// path reaches either. They stay because the tests that call them name only
// the fields they care about, which reads better than a struct literal
// repeated across every call site.
func RunPublish(push bool, all bool, skipHooks bool, skipValidation bool) error {
	return RunPublishTagged(push, all, skipHooks, skipValidation, db.DefaultTag)
}

// RunPublishTagged is RunPublish, moving tag onto what it writes instead of the
// default one. An empty tag means the default one.
func RunPublishTagged(push bool, all bool, skipHooks bool, skipValidation bool, tag string) error {
	return RunPublishWith(PublishOptions{
		Push:           push,
		All:            all,
		SkipHooks:      skipHooks,
		SkipValidation: skipValidation,
		Tag:            tag,
	})
}

// RunPublishWith is the publish entry point. RunPublish and RunPublishTagged
// are convenience wrappers over it.
func RunPublishWith(opts PublishOptions) error {
	if opts.Tag == "" {
		opts.Tag = db.DefaultTag
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Handle --all for monorepo publishing
	if opts.All {
		return publishAll(cwd, opts)
	}

	return publishSingle(cwd, opts)
}

// publishAll publishes all packages in a monorepo workspace
func publishAll(cwd string, opts PublishOptions) error {
	ws, err := workspace.Detect(cwd)
	if err != nil {
		return fmt.Errorf("failed to detect workspace: %w", err)
	}

	if ws == nil {
		return fmt.Errorf("no workspace found. --all requires a monorepo with workspaces configured")
	}

	packages, err := ws.ListPackages()
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}

	if len(packages) == 0 {
		return fmt.Errorf("no packages found in workspace")
	}

	// A dry run writes nothing, so this function's own three lines - the header
	// here, and the count and failure message at the end - must not say the
	// workspace was published. Only the wording differs; the run is the same.
	if opts.DryRun {
		fmt.Printf("Dry run over %d packages from %s workspace; nothing will be written...\n\n", len(packages), ws.Type)
	} else {
		fmt.Printf("Publishing %d packages from %s workspace...\n\n", len(packages), ws.Type)
	}

	// Publish packages in parallel with worker pool
	numWorkers := min(runtime.NumCPU(), len(packages))
	if numWorkers < 1 {
		numWorkers = 1
	}

	type publishResult struct {
		pkg *workspace.Package
		err error
	}

	pkgChan := make(chan *workspace.Package, len(packages))
	resultChan := make(chan publishResult, len(packages))
	var wg sync.WaitGroup
	var outputMu sync.Mutex // Synchronize output per package

	// Start worker goroutines
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pkg := range pkgChan {
				// Synchronize output for clean formatting
				outputMu.Lock()
				sep := hrule(3)
				fmt.Printf("%s %s@%s %s\n", sep, pkg.Name, pkg.Version, sep)
				outputMu.Unlock()

				err := publishSingle(pkg.Path, opts)

				outputMu.Lock()
				if err != nil {
					fmt.Printf("%s Failed: %v\n\n", iconFail(), err)
				} else {
					fmt.Println()
				}
				outputMu.Unlock()

				resultChan <- publishResult{pkg: pkg, err: err}
			}
		}()
	}

	// Queue all packages
	for i := range packages {
		pkgChan <- &packages[i]
	}
	close(pkgChan)

	// Wait for all workers to complete
	wg.Wait()
	close(resultChan)

	// Count successes
	successCount := 0
	for res := range resultChan {
		if res.err == nil {
			successCount++
		}
	}

	if opts.DryRun {
		fmt.Printf("Dry run: %d/%d packages packed; nothing was written\n", successCount, len(packages))
	} else {
		fmt.Printf("Published %d/%d packages\n", successCount, len(packages))
	}
	if successCount < len(packages) {
		if opts.DryRun {
			// Not "failed to pack": validation and the pre-pack hooks run
			// before pack does, and a failure in either lands here too.
			return fmt.Errorf("the dry run failed for %d of %d package(s)", len(packages)-successCount, len(packages))
		}
		return fmt.Errorf("%d of %d package(s) failed to publish", len(packages)-successCount, len(packages))
	}
	return nil
}

// publishSingle publishes a single package
func publishSingle(pkgPath string, opts PublishOptions) error {
	// Validate package before proceeding
	if !opts.SkipValidation {
		if err := validation.ValidatePackage(pkgPath); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
	}

	// Run custom pre_publish hook before packing.
	//
	// A dry run runs it too, deliberately: these hooks are often what builds
	// the files being packed, so skipping them would make the dry run report a
	// set the real publish would not. --skip-hooks covers anyone who objects.
	//
	// npm agrees, run rather than assumed: with npm 11.16.0, `npm publish
	// --dry-run` over a package carrying prepack and prepublishOnly ran both,
	// and the file each wrote appeared in the tarball listing. That same run
	// also executed postpublish; lnpm does not - see the dry-run return below.
	cfg := config.Get()
	if !opts.SkipHooks {
		if err := hooks.RunCustom(pkgPath, cfg.Hooks.PrePublish, "pre_publish"); err != nil {
			return fmt.Errorf("pre_publish hook failed: %w", err)
		}
	}

	// Run prepare scripts before packing
	if err := hooks.RunPrepare(pkgPath, opts.SkipHooks); err != nil {
		return fmt.Errorf("prepare hook failed: %w", err)
	}

	// Pack the package
	pkgJSON, files, err := pack.Pack(pkgPath)
	if err != nil {
		return fmt.Errorf("failed to pack: %w", err)
	}

	// Resolve any workspace: dependency specifiers before the content hash is
	// taken, so the hash covers the bytes consumers actually install. The
	// cleanup must outlive the store copy, so it is deferred to the end of the
	// publish rather than released here.
	cleanup, err := pack.RewriteWorkspaceDeps(pkgPath, files)
	defer cleanup()
	if err != nil {
		// Deliberately unwrapped, against the "always wrap" convention: every
		// error out of RewriteWorkspaceDeps already names the specifier, the
		// dependency and the workspace, so a "failed to resolve workspace
		// dependencies:" prefix would only stutter in front of it.
		return err
	}

	// A dry run stops here: everything that decides the packed set has run,
	// nothing that writes has, and the rewritten manifest is already
	// materialised, so the reported set is the one a real publish would store.
	// The defer above removes the temporary file it lives in on the way out.
	//
	// Here rather than immediately before finishPublish because the "already
	// published with same content" short-circuit sits in between and returns -
	// a dry run that fell into it would answer "already published" to the
	// question "what would you ship?".
	//
	// Stopping above db.GetDB is also what keeps the guarantee cheap to state:
	// the database is never opened, the store is never constructed (store.New
	// creates its directory tree as a side effect of being called), and
	// post_publish never runs. npm does run postpublish on a dry run (the same
	// 11.16.0 run cited above); lnpm does not, because post_publish is for
	// reacting to a release that happened.
	if opts.DryRun {
		reportDryRun(pkgJSON, files)
		return nil
	}

	// Check if already published with same hash
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	contentHash := pack.HashFiles(files)
	existing, err := database.GetPackageByHash(pkgJSON.Name, contentHash)
	if err != nil {
		return fmt.Errorf("failed to check existing package: %w", err)
	}

	// Nothing to do only if the content is already stored AND the tag being
	// published already names it. The tag half is not a refinement: this return
	// skips finishPublish, which is the only place a tag is ever moved, so
	// without it `publish --tag beta` over an unchanged working tree would print
	// a reassuring line and leave beta pointing wherever it was - or nowhere.
	tagged, err := database.ResolveTag(pkgJSON.Name, opts.Tag)
	if err != nil {
		return fmt.Errorf("failed to read the %s tag of %s: %w", opts.Tag, pkgJSON.Name, err)
	}

	if existing != nil && !opts.Push && tagged != nil && tagged.ContentHash == contentHash {
		fmt.Printf("%s Package %s@%s already published with same content (hash: %s)\n",
			iconWarn(), pkgJSON.Name, pkgJSON.Version, shortHash(contentHash))
		fmt.Println("Use --push to update linked projects anyway")
		return nil
	}

	if err := finishPublish(pkgPath, pkgJSON, files, database, opts.Push, opts.Tag); err != nil {
		return err
	}

	// Run custom post_publish hook after a successful publish
	if !opts.SkipHooks {
		if err := hooks.RunCustom(pkgPath, cfg.Hooks.PostPublish, "post_publish"); err != nil {
			return fmt.Errorf("post_publish hook failed: %w", err)
		}
	}

	return nil
}

// writePackedPaths renders the packed set into b, one relative path per line,
// each prefixed with indent. Shared by the dry run and by finishPublish so the
// two print the same set the same way and one of them cannot drift.
//
// Sorted by RelPath, and not because the walk leaves them unsorted by accident:
// filepath.Walk visits each directory's entries in lexical order, which puts
// "a/inner.js" before "a.js" because it descends into "a" on reaching it, while
// a sort by path puts "a.js" first ('.' is 0x2E, '/' is 0x2F). Diffing two
// listings is a main reason to have one, so the order is pinned rather than
// inherited.
//
// It writes into a builder so the caller can emit the whole block with one
// Print. That is not an atomicity guarantee - a pipe only writes atomically up
// to PIPE_BUF, 4096 bytes on Linux, and a tty promises nothing at all, and a
// few hundred paths pass 4096 easily. It is one write syscall instead of one
// per path, which makes interleaving under --all's parallel workers far less
// likely, and that is the whole claim.
func writePackedPaths(b *strings.Builder, files []*pack.FileInfo, indent string) {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.RelPath
	}
	sort.Strings(paths)

	for _, p := range paths {
		fmt.Fprintf(b, "%s%s\n", indent, p)
	}
}

// reportDryRun prints what a real publish would have packed.
func reportDryRun(pkgJSON *pack.PackageJSON, files []*pack.FileInfo) {
	var totalSize int64
	for _, f := range files {
		totalSize += f.Size
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Dry run: %s@%s would pack %d files (%s); nothing was written\n",
		pkgJSON.Name, pkgJSON.Version, len(files), formatSize(totalSize))
	writePackedPaths(&b, files, "  ")
	fmt.Print(b.String())
}

// finishPublish completes publishing with pre-packed data (used by push too),
// moving tag onto the version it records.
func finishPublish(pkgPath string, pkgJSON *pack.PackageJSON, files []*pack.FileInfo, database *db.DB, push bool, tag string) error {
	contentHash := pack.HashFiles(files)

	// Store the package
	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to create store: %w", err)
	}

	fmt.Printf("Publishing %s@%s (%d files%s)...\n", pkgJSON.Name, pkgJSON.Version, len(files), tagClause(tag))

	storePath, err := s.Store(pkgJSON.Name, contentHash, files, pkgPath)
	if err != nil {
		return fmt.Errorf("failed to store package: %w", err)
	}

	// Calculate total size
	var totalSize int64
	for _, f := range files {
		totalSize += f.Size
	}

	// Record in database
	pkg := &db.Package{
		Name:        pkgJSON.Name,
		Version:     pkgJSON.Version,
		ContentHash: contentHash,
		SourcePath:  pkgPath,
		StorePath:   storePath,
		FilesCount:  len(files),
		TotalSize:   totalSize,
	}

	// The package row and its file manifest go in together. A relink reads the
	// file rows to decide which files it can leave alone, so a package row that
	// named this generation while the file rows still described the previous one
	// would let a changed file be carried over unchanged.
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
		return fmt.Errorf("failed to record package: %w", err)
	}

	fmt.Printf("%s Published %s@%s%s\n", iconOK(), pkgJSON.Name, pkgJSON.Version, tagSuffix(tag))
	fmt.Printf("  Hash: %s\n", shortHash(contentHash))
	fmt.Printf("  Files: %d\n", len(files))
	fmt.Printf("  Size: %s\n", formatSize(totalSize))
	fmt.Printf("  Store: %s\n", storePath)

	// The packed set itself, not only the count above. A count cannot show that
	// a secret was packed, and this is the one place that can say what was
	// actually stored: `publish --dry-run` re-packs the working tree, so it
	// answers "what would ship now", never "what did ship".
	//
	// Uncapped, deliberately. A dist-heavy package puts thousands of lines on
	// every publish, which is real, but a list truncated at N is worthless for
	// the thing the list is for - the packed secret is as likely to be at
	// position 900 as position 9, and a reader who cannot trust the list to be
	// complete has to go and check by hand anyway.
	//
	// push reaches this only through RunPushTagged's "not published yet"
	// branch, which delegates the first publish here. A steady-state push does
	// its own packing and storing, and prints the same block itself (#372), so
	// the two halves of push list alike; keep the format here and there
	// together.
	var listing strings.Builder
	listing.WriteString("  Packed:\n")
	writePackedPaths(&listing, files, "    ")
	fmt.Print(listing.String())

	// If push requested, push to all linked projects
	if push {
		if err := pushToLinkedProjects(database, pkg, s); err != nil {
			return err
		}
	}

	return nil
}

// pushToLinkedProjects pushes updates to all projects linked to this package.
// It returns an error if any linked project failed to update, matching push's
// own pushToAllProjects.
func pushToLinkedProjects(database *db.DB, pkg *db.Package, s *store.Store) error {
	projects, err := database.GetProjectsForPackage(pkg.ID)
	if err != nil {
		return fmt.Errorf("failed to get linked projects: %w", err)
	}

	if len(projects) == 0 {
		fmt.Println("\nNo linked projects to update")
		return nil
	}

	fmt.Printf("\nPushing to %d linked projects...\n", len(projects))

	// Push to all projects in parallel
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
			linkRes, skipped, err := pushToProject(database, p, pkg, s)
			results <- result{path: p.Path, skipped: skipped, link: linkRes, err: err}
		}(proj)
	}

	// Wait for all pushes to complete
	wg.Wait()
	close(results)

	// Print results
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

// pushToProject pushes a package update to a single project, reporting whether
// the project was skipped rather than relinked and, when it was relinked, how
// much of the package the relink actually had to rewrite.
func pushToProject(database *db.DB, proj *db.Project, pkg *db.Package, s *store.Store) (res link.Result, skipped bool, err error) {
	// A project that added this package with --link already resolves to the
	// source directory just published. Relinking it from the store would replace
	// its live link with a snapshot copy and silently end the live updates it
	// was added for - the same hazard push guards against, reached here by
	// `publish --push`.
	linker := link.New(proj.Path)
	if linker.IsLiveLinked(pkg.Name) {
		return link.Result{}, true, nil
	}

	// Get files from store
	files, err := storeFilesForLink(database, s, pkg)
	if err != nil {
		return link.Result{}, false, err
	}

	// Re-link the package
	res, err = linker.Link(pkg.Name, pkg.StorePath, files)
	return res, false, err
}

// shortHash returns the first 8 characters of a hash
func shortHash(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

// formatSize formats a byte size in human-readable form
func formatSize(size int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case size >= GB:
		return fmt.Sprintf("%.2f GB", float64(size)/GB)
	case size >= MB:
		return fmt.Sprintf("%.2f MB", float64(size)/MB)
	case size >= KB:
		return fmt.Sprintf("%.2f KB", float64(size)/KB)
	default:
		return fmt.Sprintf("%d B", size)
	}
}
