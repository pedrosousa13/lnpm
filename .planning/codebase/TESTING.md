# Testing Patterns

**Analysis Date:** 2026-01-17

## Test Framework

**Runner:**
- Go testing (standard library)
- Config: None required (uses built-in `go test`)

**Assertion Library:**
- Standard `testing` package
- Custom helper methods via `TestEnvironment`

**Run Commands:**
```bash
go test ./...                    # Run all tests
go test -v ./...                 # Verbose output
go test -race ./...              # Race detection
go test -coverprofile=coverage.out ./...  # Coverage
make test                        # Makefile shortcut
make test-coverage               # Generate HTML coverage report
```

## Test File Organization

**Location:**
- Integration tests: Separate `tests/` directory at project root
- Unit tests: Co-located with source in `internal/` packages
- Permission tests: Suffixed `*_permissions_test.go`

**Naming:**
- Pattern: `*_test.go`
- Examples: `add_test.go`, `store_permissions_test.go`, `pack_test.go`

**Structure:**
```
lnpm/
├── tests/                    # Integration & E2E tests
│   ├── add_test.go
│   ├── publish_test.go
│   ├── helpers_test.go      # Test utilities
│   └── fixtures/            # Test data
│       ├── npm-workspace/
│       ├── pnpm-workspace/
│       └── turborepo/
└── internal/
    └── pack/
        ├── pack.go
        └── pack_test.go     # Co-located unit tests
```

## Test Structure

**Suite Organization:**
```go
func TestFeatureScenario(t *testing.T) {
    env := setupTest(t)

    // Setup phase
    pkgDir := env.CreateTestPackage("test-pkg", "1.0.0", map[string]string{
        "index.js": "module.exports = 'test';",
    })

    // Execute
    if err := cli.RunPublish(false, "", false); err != nil {
        t.Fatalf("Failed to publish: %v", err)
    }

    // Assert
    env.AssertPackageInDatabase("test-pkg", true)
    env.AssertFilesLinked(projectDir, "test-pkg")
}
```

**Patterns:**
- Setup: Use `setupTest(t)` helper returning `*TestEnvironment`
- Execution: Call production code directly (no mocks for CLI commands)
- Assertions: Custom helpers (`AssertPackageJSON`, `AssertSymlinkExists`, etc.)
- Teardown: Automatic via `t.Cleanup()`

## Mocking

**Framework:** Minimal mocking; prefers real implementations with isolation

**Patterns:**
- Database: Real bbolt instance in temp directory
- Filesystem: Real files in `t.TempDir()`
- Environment: Isolated via `t.Setenv("LNPM_STORE", tempStore)`
- Test doubles: Only for external dependencies (none currently)

**What to Mock:**
- Not applicable; integration-focused testing

**What NOT to Mock:**
- Database (use real instance with temp path)
- Filesystem operations
- Internal packages

## Fixtures and Factories

**Test Data:**
```go
// Factory pattern for test packages
pkgDir := env.CreateTestPackage("pkg-name", "1.0.0", map[string]string{
    "index.js": "module.exports = 'test';",
    "src/utils.js": "exports.util = () => {};",
})

// Copy fixture for workspace tests
workspaceDir := env.CopyFixture("npm-workspace")
```

**Location:**
- Fixtures: `tests/fixtures/` directory
- Factory methods: `tests/helpers_test.go` (`TestEnvironment` methods)

**Available Fixtures:**
- `npm-workspace/`: NPM workspace with multiple packages
- `pnpm-workspace/`: PNPM workspace structure
- `yarn-workspace/`: Yarn workspace setup
- `turborepo/`: Turborepo monorepo structure
- `nx/`: Nx workspace example

## Coverage

**Requirements:** No explicit target enforced

**View Coverage:**
```bash
make test-coverage           # Generates coverage.html
go tool cover -html=coverage.out
```

**CI Coverage:**
- Runs with `-coverprofile=coverage-unit.out` for unit tests
- Separate `coverage-workspace.out` for integration tests
- Merged and uploaded to Codecov

## Test Types

**Unit Tests:**
- Scope: Individual functions and packages
- Location: `internal/*/` co-located with source
- Examples: `pack_test.go` (file filtering), `lockfile_test.go` (JSON parsing)
- Approach: Test pure functions and data transformations

**Integration Tests:**
- Scope: Multi-component workflows (publish → add → link)
- Location: `tests/` directory
- Examples: `add_test.go`, `publish_test.go`, `workspace_test.go`
- Approach: Full command execution with real filesystem and database

**E2E Tests:**
- Scope: Complete user workflows across commands
- Location: `tests/end_to_end_test.go`, `tests/integration_test.go`
- Examples: `TestPublishAllTurborepo` (workspace discovery → publish all)
- Approach: Fixture-based scenarios mimicking real usage

## Common Patterns

**Async Testing:**
```go
// Concurrent execution helper
RunConcurrently(t,
    func() error {
        return cli.RunAdd("pkg-a", false, false)
    },
    func() error {
        return cli.RunAdd("pkg-b", false, false)
    },
)
```

**Error Testing:**
```go
err := cli.RunAdd("nonexistent-package", false, false)
if err == nil {
    t.Fatal("Expected error when adding non-existent package, got nil")
}
```

**Assertion Helpers:**
```go
// Assertion methods on TestEnvironment
env.AssertPackageJSON(projectPath, "pkg-name", "file:.lnpm/pkg-name")
env.AssertSymlinkExists(projectPath, "pkg-name")
env.AssertDatabaseLink("pkg-name", projectPath)
env.AssertLockfileExists(projectPath, true)
env.AssertGitignore(projectPath, ".lnpm/", true)
```

**Database Assertions:**
```go
env.AssertLockfile(projectPath, func(lock *lockfile.LockFile) {
    if !lock.Has("pkg-name") {
        t.Error("Expected pkg-name in lockfile")
    }
    pkg, _ := lock.Get("pkg-name")
    if pkg.Version != "1.0.0" {
        t.Errorf("Expected version 1.0.0, got %s", pkg.Version)
    }
})
```

## Test Isolation

**Per-Test Cleanup:**
- Temp directories: `t.TempDir()` auto-removes on completion
- Environment vars: `t.Setenv()` restores after test
- Database: `db.ResetForTesting()` clears singleton
- Working directory: `t.Cleanup(func() { os.Chdir(originalDir) })`

**Parallel Safety:**
- Integration tests avoid `t.Parallel()` due to shared global state
- Tests that manage their own concurrency document: "Don't use t.Parallel()"
- Unit tests can use `t.Parallel()` when isolated

## CI Configuration

**Jobs:**
- `test`: Unit tests (`./internal/...`) and workspace tests (`./tests/...`)
- `lint`: golangci-lint with `only-new-issues: true`
- `test-windows`: Same test suites on `windows-latest`, without coverage
- `test-macos`: Same test suites on `macos-latest`, without coverage
- `build`: Multi-platform compilation

**Race Detection:**
- Enabled: `go test -race` in CI
- Location: `.github/workflows/ci.yaml`

**Platform Testing:**
- Runners: `ubuntu-latest` (primary, with coverage), `windows-latest`, `macos-latest`
- Go version: `1.26` (defined in `go.mod`, which workflows read via `go-version-file`)

---

*Testing analysis: 2026-01-17*
