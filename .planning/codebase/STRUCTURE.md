# Codebase Structure

**Analysis Date:** 2026-01-17

## Directory Layout

```
lnpm/
├── cmd/                  # Application entry points
│   └── lnpm/             # Main CLI binary
├── internal/             # Private application code
│   ├── cli/              # CLI commands and orchestration
│   ├── config/           # Configuration management
│   ├── db/               # Database layer (bbolt)
│   ├── debug/            # Debug logging utilities
│   ├── gitignore/        # .gitignore file management
│   ├── link/             # Package linking logic
│   ├── pack/             # File packing and filtering
│   ├── pkgjson/          # Order-preserving package.json editing
│   ├── store/            # Package store operations
│   ├── update/           # Version update checking
│   ├── watch/            # File watching implementation
│   └── workspace/        # Monorepo workspace detection
├── pkg/                  # Public reusable packages
│   └── lockfile/         # Lock file parsing
├── tests/                # Integration tests and fixtures
│   └── fixtures/         # Test workspace examples
├── examples/             # Usage examples
├── .github/              # GitHub Actions workflows
├── .githooks/            # Git hooks
└── .planning/            # GSD planning artifacts
```

## Directory Purposes

**cmd/lnpm:**
- Purpose: Application entry point
- Contains: main.go with version injection
- Key files: `cmd/lnpm/main.go`

**internal/cli:**
- Purpose: CLI command implementations using Cobra
- Contains: Command definitions, user interaction, workflow orchestration
- Key files:
  - `root.go`: Cobra root command, version management, debug flag
  - `publish.go`: Publish command logic
  - `add.go`: Add command logic
  - `push.go`: Push command logic
  - `watch.go`: Watch command logic
  - `remove.go`: Remove/retreat command logic
  - `status.go`: Status command logic
  - `doctor.go`: Health check command
  - `gc.go`: Garbage collection command
  - `completion.go`: Shell completion generation
  - `config.go`: Config management command

**internal/db:**
- Purpose: Database operations and models
- Contains: bbolt wrapper, CRUD operations, data models
- Key files: `db.go` (Package, Project, Link, FileEntry models)

**internal/pack:**
- Purpose: Package file discovery, filtering, and manifest resolution
- Contains: File scanning, .npmignore/.gitignore parsing, content hashing,
  `workspace:` dependency rewriting (depends on `internal/pkgjson` and
  `internal/workspace`)
- Key files:
  - `pack.go`: Main packing logic, incremental hashing
  - `git_filter.go`: Safety filter for .git files
  - `workspacedeps.go`: Resolves `workspace:` specifiers into the published
    package.json, without touching the source one

**internal/store:**
- Purpose: Package store management
- Contains: File storage with reflink/hardlink/copy strategies
- Key files:
  - `store.go`: Store operations, parallel file processing
  - `reflink_darwin.go`: macOS APFS reflink syscall
  - `reflink_linux.go`: Linux Btrfs/XFS reflink ioctl
  - `reflink_other.go`: Fallback for unsupported platforms
  - `device_unix.go`: Unix device ID extraction
  - `device_windows.go`: Windows device ID extraction

**internal/link:**
- Purpose: Package linking to projects
- Contains: Reflink/hardlink/copy logic, symlink creation
- Key files:
  - `link.go`: Linker implementation
  - Platform-specific reflink files (same pattern as store/)

**internal/watch:**
- Purpose: File system watching with fsnotify
- Contains: Event debouncing, incremental sync, cached file hashing
- Key files: `watch.go`

**internal/workspace:**
- Purpose: Monorepo workspace detection
- Contains: Parser for pnpm-workspace.yaml, package.json workspaces
- Key files: `workspace.go`

**internal/config:**
- Purpose: Configuration file handling
- Contains: YAML config parsing, package manager detection
- Key files: `config.go`

**internal/gitignore:**
- Purpose: Automatic .gitignore management
- Contains: Pattern insertion/removal with atomic writes
- Key files: `gitignore.go`

**internal/pkgjson:**
- Purpose: Order-preserving package.json editing
- Contains: Dependency set/remove/lookup that splice the file's own bytes, so key order, indentation, line endings and number literals survive an edit
- Key files: `pkgjson.go`

**internal/debug:**
- Purpose: Debug logging
- Contains: Conditional logging to stderr
- Key files: `debug.go`

**internal/update:**
- Purpose: Version update notifications
- Contains: GitHub release checking (async)
- Key files: `update.go`

**pkg/lockfile:**
- Purpose: Public API for lock file operations
- Contains: lnpm.lock YAML parsing and manipulation
- Key files: `lockfile.go`

**tests/fixtures:**
- Purpose: Test workspace examples
- Contains: Sample npm/yarn/pnpm workspaces for testing, plus a packed npm tarball
- Subdirectories: `npm-workspace/`, `npm-workspace-negation/`, `yarn-workspace/`, `pnpm-workspace/`, `turborepo/`, `nx/`, `tarballs/`
- `tarballs/`: a real `.tgz` for `TestSymlinkSurvivesNpmInstall` to install by path, so the test drives a genuine `npm install` without reaching the registry; see `tarballs/README.md`

## Key File Locations

**Entry Points:**
- `cmd/lnpm/main.go`: Binary entry, sets version via ldflags
- `internal/cli/root.go`: Cobra CLI entry, command tree initialization

**Configuration:**
- `internal/config/config.go`: Global config (~/.lnpm/config.yaml)
- User config: `~/.lnpm/config.yaml` (runtime)
- Lock file: `lnpm.lock` in project directories (YAML)

**Core Logic:**
- `internal/pack/pack.go`: File discovery and hashing
- `internal/store/store.go`: Package storage
- `internal/link/link.go`: Package linking
- `internal/watch/watch.go`: File watching
- `internal/db/db.go`: Database operations

**Testing:**
- Unit tests: `*_test.go` files co-located with implementation
- Permission tests: `*_permissions_test.go` (database, gitignore, link, store)
- Integration fixtures: `tests/fixtures/`

## Naming Conventions

**Files:**
- Lowercase with underscores: `pack.go`, `reflink_darwin.go`
- Test files: `*_test.go`
- Platform-specific: `*_darwin.go`, `*_linux.go`, `*_windows.go`, `*_unix.go`, `*_other.go`

**Directories:**
- Lowercase, single word or compound: `cli/`, `gitignore/`

**Go Packages:**
- Match directory name: `package cli`, `package db`

**Functions:**
- Exported: PascalCase (`Pack`, `Link`, `GetDB`)
- Unexported: camelCase (`hashFile`, `copyFile`, `shortHash`)

**Types:**
- Exported structs: PascalCase (`Package`, `Store`, `Watcher`)
- Unexported types: camelCase (rare in this codebase)

**Constants:**
- Exported: PascalCase (`HardLink`, `Copy`)
- Unexported: camelCase (`bucketPackages`, `lockFileName`)

## Where to Add New Code

**New CLI Command:**
- Primary code: `internal/cli/{command}.go`
- Register in: `internal/cli/root.go` init() function
- Pattern: Create Cobra command, add to rootCmd.AddCommand()

**New Domain Operation:**
- Implementation: `internal/{domain}/{operation}.go`
- Tests: `internal/{domain}/{operation}_test.go`
- Example: New linking strategy → `internal/link/link.go`

**New Database Entity:**
- Model: Add struct to `internal/db/db.go`
- Bucket: Add bucket constant and initialization
- CRUD: Add Insert/Get/Delete methods to DB type

**Utilities:**
- Shared helpers: `internal/{util_name}/{util_name}.go`
- Example: New config option → `internal/config/config.go`

**Platform-Specific Code:**
- Create platform files: `{feature}_darwin.go`, `{feature}_linux.go`, `{feature}_windows.go`
- Use build tags if needed (currently not used, Go compiler handles by filename)
- Common interface in main file: `{feature}.go`

**Public API:**
- New reusable package: `pkg/{package_name}/`
- Exported types only
- Example: Lock file operations in `pkg/lockfile/`

## Special Directories

**.planning/codebase:**
- Purpose: GSD codebase mapping documents
- Generated: Yes (by /gsd:map-codebase)
- Committed: Yes

**.github/workflows:**
- Purpose: CI/CD configuration
- Generated: No
- Committed: Yes

**.githooks:**
- Purpose: Git hooks for development
- Generated: No
- Committed: Yes

**tests/fixtures:**
- Purpose: Test workspace examples
- Generated: No (manually curated), with one exception: `tarballs/lnpm-test-dep-1.0.0.tgz` is produced by `npm pack` from the hand-written sources committed beside it
- Committed: Yes

**Generated test binaries:**
- Pattern: `*.test` (e.g., `db.test`, `link.test`)
- Generated: Yes (during test runs)
- Committed: No (in .gitignore)

**Build artifacts:**
- Binary: `lnpm` (root directory)
- Coverage: `coverage-unit.out`
- Generated: Yes
- Committed: No

## Import Path Patterns

**Internal imports:**
- Pattern: `github.com/pedrosousa13/lnpm/internal/{package}`
- Example: `import "github.com/pedrosousa13/lnpm/internal/db"`

**Public API imports:**
- Pattern: `github.com/pedrosousa13/lnpm/pkg/{package}`
- Example: `import "github.com/pedrosousa13/lnpm/pkg/lockfile"`

**External dependencies:**
- CLI framework: `github.com/spf13/cobra`
- Database: `go.etcd.io/bbolt`
- File watching: `github.com/fsnotify/fsnotify`
- Hashing: `github.com/cespare/xxhash/v2`
- Worker pool: `github.com/panjf2000/ants/v2`
- Glob matching: `github.com/bmatcuk/doublestar/v4`
- YAML parsing: `gopkg.in/yaml.v3`

## Build and Deployment

**Build tool:**
- Makefile with targets: build, test, install, deps

**Binary location:**
- Development: `./lnpm` (root directory)
- Installation: `$GOPATH/bin/lnpm` or system bin directory

**Version injection:**
- Build-time: `go build -ldflags "-X main.Version={version}"`
- Default: "dev"

**Installation script:**
- Location: `install.sh` (root)
- Purpose: Quick install via curl

**Release process:**
- Tool: GoReleaser (`.goreleaser.yaml`)
- Platforms: Linux (amd64, arm64), macOS (amd64, arm64), Windows (amd64)

---

*Structure analysis: 2026-01-17*
