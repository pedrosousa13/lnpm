# Architecture

**Analysis Date:** 2026-01-17

## Pattern Overview

**Overall:** Domain-Driven CLI Application with Repository Pattern

**Key Characteristics:**
- CLI interface orchestrates domain operations (publish, add, link, watch)
- Embedded key-value database (bbolt) for state tracking
- File-based store for package content
- Intelligent linking strategy: reflink → hardlink → parallel copy
- Singleton database pattern for lifecycle management

## Layers

**CLI Layer:**
- Purpose: Command interface and orchestration
- Location: `internal/cli/`
- Contains: Cobra commands, user interaction, workflow coordination
- Depends on: All internal packages (db, store, link, pack, watch)
- Used by: `cmd/lnpm/main.go` entry point

**Domain Logic Layer:**
- Purpose: Core package management operations
- Location: `internal/pack/`, `internal/link/`, `internal/store/`, `internal/watch/`
- Contains: Business logic for packing, linking, storing, watching packages
- Depends on: Database layer, config, debug utilities
- Used by: CLI layer

**Data Access Layer:**
- Purpose: State persistence and retrieval
- Location: `internal/db/`
- Contains: bbolt database operations, CRUD for packages/projects/links
- Depends on: bbolt library
- Used by: CLI and domain layers

**Workspace Detection Layer:**
- Purpose: Monorepo workspace handling
- Location: `internal/workspace/`
- Contains: Detection and parsing of npm/yarn/pnpm/bun workspaces
- Depends on: YAML parser (gopkg.in/yaml.v3)
- Used by: Publish command for `--all` flag

**Utilities Layer:**
- Purpose: Cross-cutting concerns
- Location: `internal/config/`, `internal/debug/`, `internal/gitignore/`, `internal/pkgjson/`, `internal/update/`
- Contains: Configuration, logging, .gitignore management, package.json editing, version checking
- Depends on: Nothing (leaf modules)
- Used by: All layers

**Public API Layer:**
- Purpose: Reusable packages for external consumption
- Location: `pkg/lockfile/`
- Contains: Lock file parsing and management
- Depends on: YAML parser
- Used by: CLI layer

## Data Flow

**Publish Flow:**

1. `lnpm publish` → CLI command parses flags
2. `pack.Pack()` scans package directory, filters files, calculates content hashes
3. `store.Store()` copies/reflinks files into a temp directory and renames it onto `~/.lnpm/store/{name}/{hash}/`, never destroying an entry already committed for that hash
4. Database records Package, FileEntry records
5. If `--push` flag: retrieve linked projects, re-link files

**Add Flow:**

1. `lnpm add <pkg>` → CLI command parses package spec
2. Database lookup: find package by name (or hash if version specified)
3. `store.GetFiles()` retrieves file list from store
4. `link.Link()` creates reflinks/hardlinks to `.lnpm/{pkg}/`, symlink to `node_modules/{pkg}/`
5. Update lnpm.lock with package metadata, including the original package.json specifier, which must reach the lock before step 6 overwrites it or a failure in between loses it
6. Update package.json with `file:.lnpm/{pkg}`
7. Database records Project and Link

**Watch Flow:**

1. `lnpm watch` → CLI command starts watcher
2. fsnotify watches package directory recursively
3. On file change: debounce events (100ms default)
4. `pack.PackIncremental()` re-hashes only changed files (cache hit optimization)
5. `store.Store()` updates store with new hash
6. Database lookup: find all linked projects
7. `link.Link()` re-links to each project

**State Management:**
- bbolt database is singleton (sync.Once initialization)
- Database handles: packages, projects, links, file manifests, metadata (ID counter)
- Lock file (lnpm.lock) persists project-local state in YAML

## Key Abstractions

**Package:**
- Purpose: Represents a published package version
- Examples: `internal/db/db.go` (db.Package), `internal/pack/pack.go` (pack.PackageJSON)
- Pattern: Dual representation - PackageJSON for source, db.Package for persisted state

**Store:**
- Purpose: Content-addressable file storage
- Examples: `internal/store/store.go`
- Pattern: Hash-based addressing (`~/.lnpm/store/{name}/{hash}/`)

**Linker:**
- Purpose: Manages file linking strategies and project connections
- Examples: `internal/link/link.go`
- Pattern: Strategy pattern - reflink → hardlink → copy priority fallback

**Watcher:**
- Purpose: File system monitoring with debouncing and incremental hashing
- Examples: `internal/watch/watch.go`
- Pattern: Event loop with channel-based concurrency, cached file state

**FileInfo:**
- Purpose: File metadata with content hash
- Examples: `internal/pack/pack.go` (pack.FileInfo)
- Pattern: Used throughout packing, storing, linking pipeline

## Entry Points

**Main Entry:**
- Location: `cmd/lnpm/main.go`
- Triggers: User invokes `lnpm` command
- Responsibilities: Sets version, delegates to CLI executor

**CLI Root:**
- Location: `internal/cli/root.go`
- Triggers: main() calls cli.Execute()
- Responsibilities: Cobra command tree, debug flag handling, async version check

**Command Handlers:**
- Publish: `internal/cli/publish.go` → RunPublish()
- Add: `internal/cli/add.go` → RunAdd()
- Push: `internal/cli/push.go` (delegates to publish.finishPublish)
- Watch: `internal/cli/watch.go`
- Remove: `internal/cli/remove.go`
- Status: `internal/cli/status.go`
- Doctor: `internal/cli/doctor.go`
- GC: `internal/cli/gc.go`

## Error Handling

**Strategy:** Explicit error returns, wrapped with context

**Patterns:**
- All public functions return `error` as last return value
- Errors wrapped with `fmt.Errorf("context: %w", err)` for stack traces
- Database operations return errors, caller decides logging/display
- File operations use deferred cleanup: `defer func() { _ = file.Close() }()`
- CLI layer catches errors, formats for user display

## Cross-Cutting Concerns

**Logging:**
- `internal/debug/debug.go` provides debug.Log(), debug.Logf()
- Controlled by `--debug` flag or `LNPM_DEBUG=1` env var
- Output to stderr with timestamps

**Validation:**
- package.json must have name and version fields
- Database lookup validates package existence before operations
- Store.Exists() checks before linking

**Authentication:**
- Not applicable (local tool)

**Configuration:**
- Global: `~/.lnpm/config.yaml` (store path, link mode, debounce, gitignore management)
- Per-project: package.json `lnpm` field, lnpm.lock
- Environment: `LNPM_STORE` env var overrides store location

**Concurrency:**
- Database: mutex-protected singleton (db.mu sync.RWMutex)
- File hashing: Worker pool (ants library, NumCPU*2 workers)
- File copying: Parallel workers (runtime.NumCPU() workers, max 8)
- Watch: Channel-based event loop, debounce timer

**Platform Abstraction:**
- Reflink syscalls: Platform-specific files (reflink_darwin.go, reflink_linux.go, reflink_other.go)
- Device ID extraction: Platform-specific (device_unix.go, device_windows.go)
- Build tags determine compilation per platform

---

*Architecture analysis: 2026-01-17*
