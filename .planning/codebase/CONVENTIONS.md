# Coding Conventions

**Analysis Date:** 2026-01-17

## Naming Patterns

**Files:**
- Go source: `lowercase.go` (e.g., `config.go`, `workspace.go`)
- Tests: `*_test.go` (e.g., `add_test.go`, `pack_test.go`)
- Platform-specific: `feature_os.go` (e.g., `reflink_darwin.go`, `device_unix.go`)
- Permission tests: `*_permissions_test.go`

**Functions:**
- Exported: `PascalCase` (e.g., `RunAdd`, `GetDB`, `LoadConfig`)
- Unexported: `camelCase` (e.g., `parsePackageSpec`, `copyFile`, `getStorePath`)
- Test functions: `Test*` (e.g., `TestAddBasicPackage`)

**Variables:**
- Local: `camelCase` (e.g., `pkgDir`, `storePath`, `linkType`)
- Package-level: `camelCase` for unexported, `PascalCase` for exported
- Constants: `PascalCase` for exported (e.g., `HardLink`, `Copy`)
- Package buckets: `bucketCamelCase` prefix (e.g., `bucketPackages`, `bucketProjects`)

**Types:**
- Structs: `PascalCase` (e.g., `Package`, `Project`, `Link`, `TestEnvironment`)
- Interfaces: `PascalCase` with -er suffix where applicable
- Enums: `PascalCase` type with `PascalCase` constants

## Code Style

**Formatting:**
- Tool: `go fmt` (standard Go formatting)
- Additional: `goimports` (optional, for import ordering)
- Command: `make fmt`

**Linting:**
- Tool: `golangci-lint` (preferred) or `go vet` (fallback)
- Command: `make lint` or `make lint-staged` for pre-commit
- Config: `.golangci.yml` at the repo root defines the enabled linter set
- CI: Uses `golangci/golangci-lint-action@v9` with the version pinned to `v2.12.2`; lints the full repo on every PR
- Git hooks: Available via `make hooks-enable` at `.githooks/`

## Import Organization

**Order:**
1. Standard library imports
2. External dependencies
3. Internal packages

**Example:**
```go
import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"

    "github.com/cespare/xxhash/v2"
    bolt "go.etcd.io/bbolt"

    "github.com/pedrosousa13/lnpm/internal/config"
    "github.com/pedrosousa13/lnpm/internal/db"
)
```

**Path Aliases:**
- None configured; standard Go import paths
- Aliasing used only for name conflicts (e.g., `bolt "go.etcd.io/bbolt"`)

## Error Handling

**Patterns:**
- Always wrap errors with context: `fmt.Errorf("failed to X: %w", err)`
- Return early on errors
- Defer cleanup with explicit error ignoring: `defer func() { _ = file.Close() }()`
- Check all error returns (enforced by recent fixes)

**Example:**
```go
if err := os.MkdirAll(path, 0755); err != nil {
    return fmt.Errorf("failed to create directory: %w", err)
}
```

**Deferred Errors:**
- Explicitly ignore with `_ =` when cleanup errors are non-critical
- Use anonymous function wrapper when error handling is needed in defer

## Logging

**Framework:** Custom debug package at `internal/debug/debug.go`

**Patterns:**
- Debug logging: `debug.Logf("pack: scanning %s", packageDir)`
- User output: `fmt.Printf()` for CLI feedback
- Prefix format: `"module: message"` (e.g., `"store: storing %s hash=%s"`)
- Enabled via: `debug.Init(debugFlag)` in main

**When to Log:**
- Debug: Internal operations, file counts, method selection (reflink vs hardlink)
- User: Progress updates, warnings, success messages with symbols (✓ ⚠ ℹ 💡)

## Comments

**When to Comment:**
- Exported functions, types, and constants (godoc convention)
- Complex algorithms or non-obvious logic
- Platform-specific behavior
- Intentional deviations or workarounds

**JSDoc/TSDoc:**
- Not applicable (Go codebase)
- Use Go doc comments: `// Package describes...` or `// Function does...`

**Example:**
```go
// shortHash returns the first 8 characters of a hash for display
func shortHash(hash string) string {
```

## Function Design

**Size:** Generally focused functions; largest functions are ~250 lines with clear sections

**Parameters:**
- Use named types for clarity (e.g., `LinkType`, `PackageManager`)
- Boolean flags for optional behavior (e.g., `dev bool`, `pure bool`, `force bool`)
- Context passed explicitly where needed, not as first parameter

**Return Values:**
- Errors always last: `func RunAdd(...) error`
- Multiple returns: `(value, error)` pattern
- Named returns avoided except for clarity in defer scenarios

## Module Design

**Exports:**
- Public API: Exported functions in package (e.g., `cli.RunAdd`, `store.New`)
- Internal helpers: Unexported functions
- Constructors: `New()` pattern for types with setup (e.g., `store.New()`)

**Barrel Files:**
- Not applicable (Go uses package-level exports)
- Each package has focused responsibility (e.g., `internal/cli`, `internal/store`, `internal/pack`)

## Concurrency

**Patterns:**
- Worker pools with channels: `sync.WaitGroup` + `chan`
- Atomic counters: `sync/atomic` for progress tracking
- Parallel file operations: Goroutines with `numWorkers := min(runtime.NumCPU(), 8)`
- Error channels: Buffered `errChan := make(chan error, 1)` with select for non-blocking check

**Synchronization:**
- Mutex: `sync.RWMutex` for database access
- Once: `sync.Once` for singleton initialization (config, database)

## Testing Patterns

**Test Isolation:**
- Each test gets isolated temp directory: `t.TempDir()`
- Environment variables: `t.Setenv("LNPM_STORE", storeDir)`
- Cleanup: `t.Cleanup(func() { ... })` for automatic teardown

**Concurrency:**
- Use `t.Helper()` in assertion functions
- Custom `RunConcurrently()` helper for concurrent test execution
- Avoid `t.Parallel()` where tests control their own concurrency

---

*Convention analysis: 2026-01-17*
