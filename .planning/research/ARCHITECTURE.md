# Architecture Research

**Domain:** Production-grade Go CLI applications
**Researched:** 2026-01-17
**Confidence:** HIGH

## Standard Architecture

### System Overview

```
┌─────────────────────────────────────────────────────────────┐
│                      CLI Layer (Cobra)                       │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐        │
│  │  add    │  │ publish │  │  watch  │  │  status │        │
│  └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘        │
│       │            │            │            │              │
├───────┴────────────┴────────────┴────────────┴──────────────┤
│                   Application Services                       │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐    │
│  │ Package  │  │  Link    │  │  Watch   │  │   GC     │    │
│  │ Service  │  │ Service  │  │ Service  │  │ Service  │    │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘    │
│       │             │             │             │           │
├───────┴─────────────┴─────────────┴─────────────┴───────────┤
│                   Domain Repositories                        │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐    │
│  │ Package  │  │ Project  │  │   Link   │  │   File   │    │
│  │   Repo   │  │   Repo   │  │   Repo   │  │   Repo   │    │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘    │
│       └─────────────┴─────────────┴──────────────┘          │
├─────────────────────────────────────────────────────────────┤
│                   Infrastructure Layer                       │
│  ┌────────────┐  ┌─────────────┐  ┌──────────────┐         │
│  │  BoltDB    │  │  Filesystem │  │  Platform    │         │
│  │  (SQLite)  │  │   (store)   │  │  (OS calls)  │         │
│  └────────────┘  └─────────────┘  └──────────────┘         │
└─────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility | Typical Implementation |
|-----------|----------------|------------------------|
| CLI Commands | Parse input, coordinate services, format output | Cobra command handlers with injected services |
| Application Services | Business logic, orchestration | Structs with interface dependencies |
| Domain Repositories | Data access abstraction | Interface over database/filesystem |
| Infrastructure | Platform-specific implementations | BoltDB, filesystem operations, OS calls |

## Recommended Project Structure

```
lnpm/
├── cmd/
│   └── lnpm/
│       └── main.go              # Minimal - wire dependencies, call CLI
├── internal/
│   ├── app/
│   │   ├── services/            # Application services (business logic)
│   │   │   ├── package.go       # Package publishing, versioning
│   │   │   ├── link.go          # Package linking orchestration
│   │   │   ├── watch.go         # File watching, auto-sync
│   │   │   └── gc.go            # Garbage collection logic
│   │   └── container.go         # Dependency injection container
│   ├── domain/
│   │   ├── package.go           # Package entity, business rules
│   │   ├── project.go           # Project entity
│   │   ├── link.go              # Link entity
│   │   └── repository.go        # Repository interfaces
│   ├── infra/
│   │   ├── db/
│   │   │   ├── boltdb.go        # BoltDB implementation
│   │   │   └── repository.go    # Repository implementations
│   │   ├── store/
│   │   │   ├── store.go         # File store operations
│   │   │   ├── reflink_*.go     # Platform-specific reflink
│   │   │   └── device_*.go      # Platform-specific device checks
│   │   └── platform/
│   │       ├── filesystem.go    # Filesystem interface
│   │       └── os_*.go          # Platform-specific OS operations
│   └── cli/
│       ├── root.go              # Root command setup
│       ├── add.go               # Command handlers (thin)
│       ├── publish.go
│       └── watch.go
├── pkg/                         # Public API (if needed)
└── tests/
    ├── integration/             # Integration tests
    └── fixtures/                # Test data
```

### Structure Rationale

- **cmd/:** Minimal main - only wires dependencies and calls CLI. Enables testing without main.
- **internal/app/:** Application layer - services implement business logic, depend on domain interfaces.
- **internal/domain/:** Domain entities and repository interfaces. No dependencies on infra.
- **internal/infra/:** Infrastructure implementations. Depends on domain interfaces, not imported by domain.
- **internal/cli/:** Command handlers are thin - parse input, call services, format output.
- **tests/:** Integration tests in separate package avoid circular dependencies, test full stack.

## Architectural Patterns

### Pattern 1: Dependency Injection via Constructor

**What:** Pass dependencies as constructor parameters, store in struct fields.

**When to use:** Default approach for all components. Enables testing, explicit dependencies.

**Trade-offs:**
- ✅ Pro: Testable (inject mocks), explicit dependencies, compile-time safety
- ✅ Pro: No global state, parallel-test friendly
- ❌ Con: More verbose than singletons
- ❌ Con: Need container for complex graphs

**Example:**
```go
// Domain interface (defined by consumer)
type PackageRepository interface {
    GetByName(ctx context.Context, name string) (*Package, error)
    Insert(ctx context.Context, pkg *Package) error
}

// Application service
type PackageService struct {
    repo   PackageRepository
    store  FileStore
    hasher ContentHasher
}

// Constructor injection
func NewPackageService(repo PackageRepository, store FileStore, hasher ContentHasher) *PackageService {
    return &PackageService{
        repo:   repo,
        store:  store,
        hasher: hasher,
    }
}

// Business logic method
func (s *PackageService) Publish(ctx context.Context, sourcePath string) (*Package, error) {
    // Validation, hashing, storing files, database insert
    hash, err := s.hasher.Hash(sourcePath)
    if err != nil {
        return nil, fmt.Errorf("hash content: %w", err)
    }

    pkg := &Package{Name: name, ContentHash: hash}
    if err := s.repo.Insert(ctx, pkg); err != nil {
        return nil, fmt.Errorf("insert package: %w", err)
    }

    return pkg, nil
}
```

### Pattern 2: Interface Segregation (Accept Interfaces, Return Structs)

**What:** Functions accept narrow interfaces, return concrete structs. Define interfaces on consumer side, not provider.

**When to use:** Always. Enables mocking exactly what you need, avoids interface pollution.

**Trade-offs:**
- ✅ Pro: Narrow, focused interfaces easy to mock
- ✅ Pro: Consumers define interfaces for their needs
- ✅ Pro: Avoids dependency on provider's interface definition
- ❌ Con: More interface definitions (but localized)

**Example:**
```go
// BAD: Provider defines interface (forces consumers to depend on full interface)
// store/store.go
type Store interface {
    Store(name, hash string, files []*FileInfo) error
    Remove(name, hash string) error
    Exists(name, hash string) bool
    GetFiles(name, hash string) ([]*FileInfo, error)
    List() ([]string, error)
    ListVersions(name string) ([]string, error)
}

// GOOD: Consumer defines narrow interface for its needs
// services/package.go
type fileStorer interface {
    Store(name, hash string, files []*FileInfo) error
}

type PackageService struct {
    store fileStorer  // Only needs Store method
}

// Mock is simple (one method)
type mockStore struct {
    storeFunc func(name, hash string, files []*FileInfo) error
}

func (m *mockStore) Store(name, hash string, files []*FileInfo) error {
    return m.storeFunc(name, hash, files)
}
```

### Pattern 3: Functional Options for Configuration

**What:** Pass functional options to constructor for optional configuration.

**When to use:** When component has optional settings (not required dependencies).

**Trade-offs:**
- ✅ Pro: Clean API, backward compatible (add options without breaking)
- ✅ Pro: Self-documenting, type-safe
- ✅ Pro: Default values easy to provide
- ❌ Con: More complex than simple struct config
- ❌ Con: Required dependencies poorly suited (use constructor parameters)

**Example:**
```go
// Option function type
type ServiceOption func(*PackageService)

// Option constructors
func WithConcurrency(n int) ServiceOption {
    return func(s *PackageService) {
        s.concurrency = n
    }
}

func WithProgressCallback(cb ProgressCallback) ServiceOption {
    return func(s *PackageService) {
        s.onProgress = cb
    }
}

// Constructor with options
func NewPackageService(repo PackageRepository, opts ...ServiceOption) *PackageService {
    s := &PackageService{
        repo:        repo,         // Required dependency
        concurrency: runtime.NumCPU(), // Default
    }

    for _, opt := range opts {
        opt(s)
    }

    return s
}

// Usage
svc := NewPackageService(
    repo,
    WithConcurrency(4),
    WithProgressCallback(logProgress),
)
```

### Pattern 4: Error Wrapping with Context

**What:** Wrap errors at each layer with `fmt.Errorf("%w", err)` to preserve stack trace and add context.

**When to use:** Always when propagating errors across layers.

**Trade-offs:**
- ✅ Pro: Preserves error chain, enables `errors.Is`/`errors.As`
- ✅ Pro: Adds context at each layer ("what was I doing when this failed?")
- ✅ Pro: No external dependencies (stdlib)
- ❌ Con: Verbose (every error needs wrapping)

**Example:**
```go
// Domain layer
var ErrPackageNotFound = errors.New("package not found")

func (r *BoltRepository) GetByName(ctx context.Context, name string) (*Package, error) {
    var pkg *Package
    err := r.db.View(func(tx *bolt.Tx) error {
        data := tx.Bucket(bucketPackages).Get([]byte(name))
        if data == nil {
            return ErrPackageNotFound
        }
        return json.Unmarshal(data, &pkg)
    })
    if err != nil {
        return nil, fmt.Errorf("query package %q: %w", name, err)
    }
    return pkg, nil
}

// Service layer
func (s *PackageService) GetPackage(ctx context.Context, name string) (*Package, error) {
    pkg, err := s.repo.GetByName(ctx, name)
    if err != nil {
        return nil, fmt.Errorf("get package from repository: %w", err)
    }
    return pkg, nil
}

// CLI layer
func runAdd(cmd *cobra.Command, args []string) error {
    pkg, err := svc.GetPackage(cmd.Context(), args[0])
    if err != nil {
        if errors.Is(err, ErrPackageNotFound) {
            return fmt.Errorf("%s not in store - run 'lnpm publish' first", args[0])
        }
        return fmt.Errorf("add package: %w", err)
    }
    fmt.Printf("Added %s@%s\n", pkg.Name, pkg.Version)
    return nil
}
```

### Pattern 5: Context Propagation for Cancellation

**What:** Pass `context.Context` as first parameter to all I/O operations. Use for timeouts, cancellation, request-scoped values.

**When to use:** All database queries, filesystem operations, long-running tasks.

**Trade-offs:**
- ✅ Pro: Enables cancellation (Ctrl+C, timeouts)
- ✅ Pro: Prevents resource leaks (goroutines, file handles)
- ✅ Pro: Standard pattern (expected in Go)
- ❌ Con: Verbose (every signature needs ctx)

**Example:**
```go
// Repository interface
type PackageRepository interface {
    GetByName(ctx context.Context, name string) (*Package, error)
    Insert(ctx context.Context, pkg *Package) error
}

// Service uses context
func (s *PackageService) Publish(ctx context.Context, sourcePath string) (*Package, error) {
    // Check context before expensive operation
    if err := ctx.Err(); err != nil {
        return nil, err
    }

    files, err := s.collectFiles(ctx, sourcePath)
    if err != nil {
        return nil, fmt.Errorf("collect files: %w", err)
    }

    // Pass context to repository
    pkg := &Package{Name: name}
    if err := s.repo.Insert(ctx, pkg); err != nil {
        return nil, fmt.Errorf("insert: %w", err)
    }

    return pkg, nil
}

// CLI creates context
func runPublish(cmd *cobra.Command, args []string) error {
    ctx := cmd.Context() // Cobra provides context with signal handling

    pkg, err := svc.Publish(ctx, ".")
    if err != nil {
        return err
    }

    fmt.Printf("Published %s@%s\n", pkg.Name, pkg.Version)
    return nil
}
```

### Pattern 6: Testing with Isolated Environments

**What:** Each test gets isolated database and filesystem using `t.TempDir()` and `t.Setenv()`.

**When to use:** All integration tests that touch database or filesystem.

**Trade-offs:**
- ✅ Pro: Tests can run in parallel (`t.Parallel()`)
- ✅ Pro: No test pollution, deterministic
- ✅ Pro: No cleanup needed (Go handles it)
- ❌ Con: Slower than unit tests with mocks
- ❌ Con: More setup per test

**Example:**
```go
func TestPackageService_Publish(t *testing.T) {
    t.Parallel() // Safe because each test is isolated

    // Setup isolated environment
    storeDir := t.TempDir()
    dbPath := filepath.Join(t.TempDir(), "test.db")
    t.Setenv("LNPM_STORE", storeDir)

    // Create real dependencies (or mocks)
    db, err := bolt.Open(dbPath, 0600, nil)
    require.NoError(t, err)
    t.Cleanup(func() { db.Close() })

    repo := NewBoltRepository(db)
    store := NewFileStore(storeDir)
    svc := NewPackageService(repo, store)

    // Test
    pkg, err := svc.Publish(context.Background(), "testdata/package")
    require.NoError(t, err)
    assert.Equal(t, "test-pkg", pkg.Name)

    // Verify side effects
    stored, err := repo.GetByName(context.Background(), "test-pkg")
    require.NoError(t, err)
    assert.Equal(t, pkg.ID, stored.ID)
}
```

### Pattern 7: Deterministic Concurrency Testing (Go 1.25+ synctest)

**What:** Use `testing/synctest` to test concurrent code deterministically with virtual time.

**When to use:** Testing goroutines, channels, time.Sleep, file watchers. Available Go 1.25+.

**Trade-offs:**
- ✅ Pro: Deterministic concurrent tests (no flakes)
- ✅ Pro: Fast (virtual time, no real sleeps)
- ✅ Pro: Explore all interleavings
- ❌ Con: Requires Go 1.25+
- ❌ Con: Different behavior than production (virtual environment)

**Example:**
```go
import "testing/synctest"

func TestWatchService_AutoSync(t *testing.T) {
    synctest.Run(func() {
        // Setup
        svc := NewWatchService(mockRepo, mockStore)

        // Start watcher in goroutine
        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()

        errCh := make(chan error)
        go func() {
            errCh <- svc.Watch(ctx, "testdata/package")
        }()

        // Simulate file change (virtual time)
        time.Sleep(100 * time.Millisecond) // Instant in synctest
        createFile("testdata/package/new.js")

        // Wait for sync (virtual time)
        time.Sleep(1 * time.Second) // Instant in synctest

        // Verify sync happened
        assert.True(t, mockStore.SyncCalled)

        // Cleanup
        cancel()
        err := <-errCh
        assert.NoError(t, err)
    })
}
```

## Data Flow

### Request Flow (Publish Command)

```
User runs: lnpm publish
    ↓
[CLI Handler] Parse args, get context
    ↓
[PackageService.Publish(ctx, path)]
    ↓
├─→ [ContentHasher] Hash directory contents
├─→ [FileStore] Copy/link files to store
├─→ [PackageRepository] Insert package record
└─→ [FileRepository] Insert file entries
    ↓
[CLI Handler] Format output, return
```

### State Management (Database)

```
[Service Layer]
    ↓ (business logic validates, transforms)
[Repository Interface] (domain boundary)
    ↓ (implementation-specific details)
[BoltDB Transaction]
    ↓ (serialize to JSON)
[BoltDB Buckets] (key-value storage)
```

### Concurrency Flow (Watch Command)

```
[Watch Service] Start file watcher
    ↓
[fsnotify] OS file events → Go channel
    ↓
[Debouncer] Goroutine: aggregate events (1 second window)
    ↓
[PackageService.Publish(ctx, path)] Re-publish on change
    ↓
[PushService.PushToProjects(ctx, pkg)] Update all linked projects
    ↓
[Link Service] Parallel goroutines per project
    ↓ ↓ ↓
[Project 1] [Project 2] [Project 3] (concurrent updates)
```

### Key Data Flows

1. **Singleton elimination:** Database accessed via injected `PackageRepository` interface, not `db.GetDB()` singleton. Repository instance created at startup, passed to services.

2. **Platform abstraction:** Platform-specific code (reflink, device checks) behind `FileSystem` interface. Test with fake filesystem, production uses OS calls.

3. **Error context:** Errors wrapped at each layer with operation context. CLI can distinguish user errors (package not found) from system errors (database corruption).

## Scaling Considerations

| Scale | Architecture Adjustments |
|-------|--------------------------|
| 1-10 packages | Monolithic CLI, BoltDB, filesystem store (current) |
| 10-1000 packages | Same architecture, optimize database indexes, add caching layer |
| 1000+ packages | Consider separate daemon for watch mode, shared cache, potentially switch to SQLite for better concurrency |

### Scaling Priorities

1. **First bottleneck: Parallel tests with singleton database**
   - Current: Global singleton prevents parallel tests
   - Fix: Dependency injection, isolated database per test (`t.TempDir()`)
   - Impact: Tests run 4-8x faster, no flakes

2. **Second bottleneck: Watch mode file I/O**
   - Current: Sequential file operations on publish/push
   - Fix: Already parallelized in current code (good!)
   - Further optimization: Debounce file events, batch database writes

3. **Third bottleneck: Concurrent database access (many projects)**
   - Current: BoltDB single-writer (fine for CLI, problematic for daemon)
   - Fix: If needed, migrate to SQLite (WAL mode) or embed separate databases per project

## Anti-Patterns

### Anti-Pattern 1: Singleton Database

**What people do:** Use `db.GetDB()` singleton accessed from all code.

**Why it's wrong:**
- Global state prevents parallel tests (race conditions)
- Hard to test (can't inject mock)
- Hidden dependency (not explicit in function signature)
- Initialization order problems (when is it safe to call?)

**Do this instead:**
```go
// BAD
func RunAdd(packageName string) error {
    db, err := db.GetDB() // Singleton, global state
    pkg, err := db.GetPackageByName(packageName)
}

// GOOD
type AddCommand struct {
    repo PackageRepository // Injected dependency
}

func (c *AddCommand) Run(ctx context.Context, packageName string) error {
    pkg, err := c.repo.GetByName(ctx, packageName) // No singleton
}
```

### Anti-Pattern 2: God Service (One Service Does Everything)

**What people do:** Create single `LnpmService` with 20 methods handling all operations.

**Why it's wrong:**
- Hard to test (need to mock everything)
- Hard to reason about (too many responsibilities)
- Difficult to parallelize (coarse-grained locks)
- Violates Single Responsibility Principle

**Do this instead:**
```go
// BAD
type LnpmService struct {
    db *DB
    store *Store
}

func (s *LnpmService) Publish(...) error { ... }
func (s *LnpmService) Add(...) error { ... }
func (s *LnpmService) Push(...) error { ... }
func (s *LnpmService) Watch(...) error { ... }
func (s *LnpmService) GarbageCollect(...) error { ... }
// ... 15 more methods

// GOOD - Separate services, each focused
type PackageService struct { repo PackageRepository; store FileStore }
func (s *PackageService) Publish(ctx, path) (*Package, error)

type LinkService struct { linker Linker; repo LinkRepository }
func (s *LinkService) LinkToProject(ctx, pkg, project) error

type WatchService struct { pkgSvc *PackageService; linkSvc *LinkService }
func (s *WatchService) Watch(ctx, path) error
```

### Anti-Pattern 3: Swallowing Errors

**What people do:** Ignore errors or replace with generic message.

**Why it's wrong:**
- Loses context for debugging
- Can't distinguish error types (user error vs system error)
- Breaks `errors.Is`/`errors.As` checking

**Do this instead:**
```go
// BAD
files, err := s.collectFiles(sourcePath)
if err != nil {
    return nil, errors.New("failed to collect files") // Lost original error!
}

// GOOD
files, err := s.collectFiles(sourcePath)
if err != nil {
    return nil, fmt.Errorf("collect files from %s: %w", sourcePath, err)
}

// Even better - sentinel errors for user-facing conditions
var ErrNoPackageJSON = errors.New("no package.json found")

func (s *PackageService) Publish(ctx context.Context, path string) error {
    if !fileExists(filepath.Join(path, "package.json")) {
        return fmt.Errorf("%w in %s", ErrNoPackageJSON, path)
    }
}

// CLI can check specific errors
if errors.Is(err, ErrNoPackageJSON) {
    fmt.Println("Error: Not a Node.js package (no package.json)")
    fmt.Println("Run this command in a directory with package.json")
    return nil // User error, not exit code 1
}
```

### Anti-Pattern 4: Using `t.Parallel()` Without Isolation

**What people do:** Add `t.Parallel()` to tests that share database or modify environment.

**Why it's wrong:**
- Race conditions (tests modify shared state)
- Flaky tests (timing-dependent failures)
- Hard to debug (non-deterministic)

**Do this instead:**
```go
// BAD
func TestPublish(t *testing.T) {
    t.Parallel() // WRONG - uses global db.GetDB()

    db, _ := db.GetDB() // Shared across all parallel tests!
    pkg, err := PublishPackage(db, ".")
}

// GOOD
func TestPublish(t *testing.T) {
    t.Parallel() // Safe - isolated environment

    // Each test gets own database
    dbPath := filepath.Join(t.TempDir(), "test.db")
    db, err := bolt.Open(dbPath, 0600, nil)
    require.NoError(t, err)
    t.Cleanup(func() { db.Close() })

    // Each test gets own store
    storeDir := t.TempDir()
    t.Setenv("LNPM_STORE", storeDir)

    repo := NewBoltRepository(db)
    svc := NewPackageService(repo, store)
    pkg, err := svc.Publish(context.Background(), "testdata/package")
}
```

### Anti-Pattern 5: Mixing Layers (CLI → Database Direct)

**What people do:** CLI command handlers call database directly, skipping service layer.

**Why it's wrong:**
- Business logic scattered across CLI and database layers
- Can't reuse logic (tied to CLI)
- Hard to test business logic (need CLI context)
- Violates separation of concerns

**Do this instead:**
```go
// BAD - CLI knows about database schema
func runAdd(cmd *cobra.Command, args []string) error {
    db, _ := db.GetDB()

    // Business logic in CLI layer (wrong!)
    pkg, err := db.GetPackageByName(args[0])
    files, err := db.GetFilesForPackage(pkg.ID)

    for _, file := range files {
        // Linking logic in CLI (wrong!)
        os.Link(filepath.Join(pkg.StorePath, file.RelPath), ...)
    }
}

// GOOD - CLI delegates to service
func runAdd(cmd *cobra.Command, args []string) error {
    // CLI only: parse input, call service, format output
    err := c.linkService.AddToProject(cmd.Context(), args[0], ".")
    if err != nil {
        return fmt.Errorf("add package: %w", err)
    }
    fmt.Printf("✓ Added %s\n", args[0])
    return nil
}

// Business logic in service layer
func (s *LinkService) AddToProject(ctx context.Context, pkgName, projectPath string) error {
    pkg, err := s.pkgRepo.GetByName(ctx, pkgName)
    if err != nil {
        return fmt.Errorf("get package: %w", err)
    }

    files, err := s.fileRepo.GetForPackage(ctx, pkg.ID)
    if err != nil {
        return fmt.Errorf("get files: %w", err)
    }

    if err := s.linker.Link(pkg, files, projectPath); err != nil {
        return fmt.Errorf("link files: %w", err)
    }

    return nil
}
```

## Refactoring Order

To migrate lnpm from singleton to DI architecture without breaking functionality:

### Phase 1: Define Domain Interfaces (Low Risk)
**Dependencies:** None
**Changes:** Create interfaces for repositories, don't change implementations yet.

```go
// internal/domain/repository.go
type PackageRepository interface {
    GetByName(ctx context.Context, name string) (*Package, error)
    Insert(ctx context.Context, pkg *Package) error
}

type ProjectRepository interface {
    GetByPath(ctx context.Context, path string) (*Project, error)
    Insert(ctx context.Context, proj *Project) error
}
```

**Why first:** Low risk, no behavior change. Existing code keeps working.

### Phase 2: Implement Repositories (Wrap Existing DB)
**Dependencies:** Phase 1
**Changes:** Create repository structs that wrap existing `*db.DB`, implement interfaces.

```go
// internal/infra/db/repository.go
type BoltPackageRepository struct {
    db *db.DB
}

func NewBoltPackageRepository(database *db.DB) *BoltPackageRepository {
    return &BoltPackageRepository{db: database}
}

func (r *BoltPackageRepository) GetByName(ctx context.Context, name string) (*Package, error) {
    // Call existing db.DB.GetPackageByName
    return r.db.GetPackageByName(name)
}
```

**Why second:** Wraps existing implementation, no logic change. Can test repositories independently.

### Phase 3: Create Application Services
**Dependencies:** Phase 2
**Changes:** Extract business logic from CLI into service layer. Services depend on repository interfaces.

```go
// internal/app/services/package.go
type PackageService struct {
    repo  domain.PackageRepository
    store FileStore
}

func NewPackageService(repo domain.PackageRepository, store FileStore) *PackageService {
    return &PackageService{repo: repo, store: store}
}

func (s *PackageService) Publish(ctx context.Context, sourcePath string) (*domain.Package, error) {
    // Business logic moved from CLI
}
```

**Why third:** Business logic separated, but CLI not changed yet. Can test services with mock repositories.

### Phase 4: Dependency Injection Container
**Dependencies:** Phase 3
**Changes:** Create container that wires all dependencies at startup.

```go
// internal/app/container.go
type Container struct {
    PackageService *PackageService
    LinkService    *LinkService
    WatchService   *WatchService
}

func NewContainer() (*Container, error) {
    // Open database (once at startup)
    db, err := db.GetDB()
    if err != nil {
        return nil, err
    }

    // Create repositories
    pkgRepo := infra.NewBoltPackageRepository(db)
    projRepo := infra.NewBoltProjectRepository(db)

    // Create services with injected dependencies
    pkgSvc := services.NewPackageService(pkgRepo, store)
    linkSvc := services.NewLinkService(projRepo, pkgRepo)
    watchSvc := services.NewWatchService(pkgSvc, linkSvc)

    return &Container{
        PackageService: pkgSvc,
        LinkService:    linkSvc,
        WatchService:   watchSvc,
    }, nil
}
```

**Why fourth:** Central wiring point. CLI commands get services from container.

### Phase 5: Refactor CLI to Use Services
**Dependencies:** Phase 4
**Changes:** CLI commands call services instead of database directly.

```go
// cmd/lnpm/main.go
func main() {
    container, err := app.NewContainer()
    if err != nil {
        log.Fatal(err)
    }

    cli.Execute(container) // Pass container to CLI
}

// internal/cli/root.go
func Execute(container *app.Container) error {
    rootCmd.AddCommand(newAddCmd(container.LinkService))
    rootCmd.AddCommand(newPublishCmd(container.PackageService))
    return rootCmd.Execute()
}

// internal/cli/add.go
func newAddCmd(linkSvc *services.LinkService) *cobra.Command {
    return &cobra.Command{
        Use: "add <package>",
        RunE: func(cmd *cobra.Command, args []string) error {
            return linkSvc.AddToProject(cmd.Context(), args[0], ".")
        },
    }
}
```

**Why fifth:** After services exist, CLI becomes thin. Can test business logic via service layer.

### Phase 6: Remove Singleton Pattern
**Dependencies:** Phase 5
**Changes:** Remove `db.GetDB()` singleton, make database instance passed to repositories.

```go
// internal/infra/db/db.go (before)
var instance *DB
var once sync.Once

func GetDB() (*DB, error) {
    once.Do(func() {
        instance, _ = initDB()
    })
    return instance, nil
}

// internal/infra/db/db.go (after)
func Open(path string) (*DB, error) {
    db, err := bolt.Open(path, 0600, nil)
    if err != nil {
        return nil, err
    }
    return &DB{db: db}, nil
}
```

**Why last:** Only safe after all code uses injected dependencies. Breaking change (but isolated to container).

### Phase 7: Enable Parallel Tests
**Dependencies:** Phase 6
**Changes:** Update test helpers to create isolated environments, add `t.Parallel()`.

```go
// tests/helpers_test.go (after Phase 6)
func setupTest(t *testing.T) *TestEnvironment {
    t.Helper()
    t.Parallel() // NOW SAFE - no singleton

    // Each test gets isolated database
    dbPath := filepath.Join(t.TempDir(), "test.db")
    db, err := infra.Open(dbPath)
    require.NoError(t, err)
    t.Cleanup(func() { db.Close() })

    // Create repositories and services
    pkgRepo := infra.NewBoltPackageRepository(db)
    pkgSvc := services.NewPackageService(pkgRepo, store)

    return &TestEnvironment{
        PackageService: pkgSvc,
        // ...
    }
}
```

**Why last:** Only works after singleton removed. Dramatic test speedup (4-8x).

## Dependency Graph

```
Phase 1: Domain Interfaces
    ↓
Phase 2: Repository Implementations (wraps existing DB)
    ↓
Phase 3: Application Services (use repositories)
    ↓
Phase 4: DI Container (wires everything)
    ↓
Phase 5: CLI Refactor (uses services)
    ↓
Phase 6: Remove Singleton (breaking change, isolated to container)
    ↓
Phase 7: Parallel Tests (speedup)
```

**Critical path:** Each phase depends on previous. Cannot skip phases without risk.

**Low-risk early wins:**
- After Phase 3: Can write service-layer unit tests with mocks
- After Phase 4: Business logic testable without CLI
- After Phase 7: Tests run 4-8x faster

## Testing Strategy

### Unit Tests (Fast, Isolated)

**What to test:** Business logic in services, pure functions.

**How:** Mock repository interfaces using simple struct with function fields.

```go
func TestPackageService_Publish(t *testing.T) {
    mockRepo := &mockPackageRepository{
        insertFunc: func(ctx context.Context, pkg *Package) error {
            return nil // Success case
        },
    }

    svc := NewPackageService(mockRepo, mockStore)
    pkg, err := svc.Publish(context.Background(), "testdata")

    require.NoError(t, err)
    assert.Equal(t, "test-pkg", pkg.Name)
}
```

### Integration Tests (Real DB, Real Filesystem)

**What to test:** Full command flow, repository implementations, platform-specific code.

**How:** Isolated environment per test (`t.TempDir()`, `t.Setenv()`), real BoltDB.

```go
func TestEndToEnd_PublishAndAdd(t *testing.T) {
    t.Parallel()

    env := setupTestEnvironment(t) // Creates isolated DB + store

    // Publish package
    pkg, err := env.PackageService.Publish(context.Background(), "fixtures/pkg-a")
    require.NoError(t, err)

    // Add to project
    err = env.LinkService.AddToProject(context.Background(), pkg.Name, "fixtures/project")
    require.NoError(t, err)

    // Verify filesystem
    assert.FileExists(t, "fixtures/project/node_modules/pkg-a/index.js")

    // Verify database
    links, err := env.DB.GetLinksForProject(...)
    assert.Len(t, links, 1)
}
```

### Concurrency Tests (synctest for Determinism)

**What to test:** Watch mode, parallel operations, race conditions.

**How:** Use `testing/synctest` for virtual time, `go test -race` to detect races.

```go
func TestWatchService_DebounceChanges(t *testing.T) {
    synctest.Run(func() {
        svc := NewWatchService(mockPkgSvc, mockLinkSvc)

        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()

        go svc.Watch(ctx, "testdata")

        // Rapid file changes
        for i := 0; i < 10; i++ {
            writeFile("testdata/index.js", fmt.Sprintf("v%d", i))
            time.Sleep(50 * time.Millisecond) // Virtual time
        }

        // Wait for debounce window
        time.Sleep(2 * time.Second) // Virtual time

        // Should only publish once (debounced)
        assert.Equal(t, 1, mockPkgSvc.PublishCallCount)
    })
}
```

## Sources

### Dependency Injection
- [Dependency Injection | Learn Go with tests](https://quii.gitbook.io/learn-go-with-tests/go-fundamentals/dependency-injection)
- [Dependency Injection in Go: Patterns & Best Practices](https://www.glukhov.org/post/2025/12/dependency-injection-in-go/)
- [Testing Cobra CLI Apps in Go: A DI Approach](https://jonesrussell.github.io/blog/golang/testing/2024/07/24/a-nod-to-golang-testing-cobra-cli-applications-with-dependency-injection.html)
- [Database Connection Management: DI vs. Singleton](https://leapcell.io/blog/database-connection-management-in-go-web-apps-a-dive-into-dependency-injection-vs-singleton)

### Architecture Patterns
- [Writing Go CLIs With Just Enough Architecture](https://blog.carlana.net/post/2020/go-cli-how-to-and-advice/)
- [Go Project Structure: Practices & Patterns](https://dev.to/rosgluk/go-project-structure-practices-patterns-22l5)
- [Architectural Patterns in Go: MVC, Hexagonal, CQRS](https://norbix.dev/posts/architectural-patterns/)
- [Clean Architecture in Go](https://threedots.tech/post/introducing-clean-architecture/)

### Testing
- [Concurrency + Testing = synctest (FOSDEM 2026)](https://fosdem.org/2026/schedule/event/BDH7G7-go-testing-synctest/)
- [Go Concurrent Test Patterns](https://hoani.net/guides/software/golang/testing-concurrency)
- [Parallel Table-Driven Tests in Go](https://www.glukhov.org/post/2025/12/parallel-table-driven-tests-in-go/)
- [Fast, Parallel Database Tests](https://kevin.burke.dev/kevin/fast-parallel-database-tests/)

### Interfaces & Mocking
- [How Go Interfaces Help Build Clean, Testable Systems](https://dev.to/shrsv/how-go-interfaces-help-build-clean-testable-systems-3163)
- [Accept Interfaces, Return Structs](https://dev.to/shrsv/designing-go-apis-the-standard-library-way-accept-interfaces-return-structs-410k)
- [Software Architecture in Go: Testability](https://mariocarrion.com/2021/11/12/golang-software-architecture-testability.html)

### Error Handling
- [A practical guide to error handling in Go](https://www.datadoghq.com/blog/go-error-handling/)
- [Working with Errors in Go 1.13](https://go.dev/blog/go1.13-errors)
- [Error Handling in Go: Patterns and Best Practices](https://dev.to/nguyn_ph_22a40697040669/error-handling-in-go-patterns-and-best-practices-38co)

### Concurrency
- [Building a Real-Time File Monitor with Goroutines](https://alamrafiul.com/posts/golang-realtime-file-monitor/)
- [Mastering Go Concurrency Patterns](https://dev.to/jones_charles_ad50858dbc0/mastering-go-concurrency-patterns-pipelines-broadcasting-and-cancellation-1j36)
- [Concurrency | Learn Go with tests](https://quii.gitbook.io/learn-go-with-tests/go-fundamentals/concurrency)

### Functional Options
- [The Functional Options Pattern in Go](https://www.yellowduck.be/posts/the-functional-options-pattern-in-go)
- [10 years of functional options](https://www.bytesizego.com/blog/10-years-functional-options-golang)
- [Functional options for friendly APIs](https://dave.cheney.net/2014/10/17/functional-options-for-friendly-apis)

---
*Architecture research for: lnpm quality milestone*
*Researched: 2026-01-17*
