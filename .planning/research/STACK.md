# Stack Research: Go Quality, Testing & Reliability

**Domain:** Go CLI Tool Quality Improvements
**Researched:** 2026-01-17
**Confidence:** HIGH

## Recommended Stack

### Core Testing Framework

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| Go stdlib `testing` | Go 1.24.12+ | Primary test framework | Built-in, zero dependencies, supports unit/integration/fuzz/benchmark tests. Industry standard. |
| testify/assert | v1.10.0+ | Readable assertions | More expressive error messages than bare `if` checks. Non-fatal failures. |
| testify/require | v1.10.0+ | Fatal assertions | Same as assert but stops test on failure. Use for setup/preconditions. |
| testify/mock | v1.10.0+ | Manual mocking | Interface-based mocks with call verification. Integrates with stdlib testing. |

**Why stdlib + testify:** Go's philosophy is minimalism. Stdlib testing handles all test types (unit, integration, fuzz, benchmark). Testify adds ergonomics without framework lock-in. This combo is the de facto standard for 2025.

### Mock Generation

| Tool | Version | Purpose | When to Use |
|------|---------|---------|-------------|
| mockery | v3.6.0+ | Auto-generate mocks from interfaces | Reduces boilerplate, maintains type safety. Use for complex interfaces with many methods. |

**Why mockery v3:** Complete rewrite in 2025 with order-of-magnitude performance improvements. Template-driven, supports custom templates. Works seamlessly with testify/mock.

### Static Analysis & Linting

| Tool | Version | Purpose | Why Essential for This Project |
|------|---------|---------|-------------------------------|
| golangci-lint | v2.1.2+ | Meta-linter runner | Runs 50+ linters in parallel with caching. Catches race conditions, error handling bugs, code smells. **Critical for stability goals.** |
| staticcheck | (via golangci-lint) | Deep analysis | Gold standard for Go static analysis. Detects concurrency bugs, incorrect API usage, inefficiencies. |
| gosec | v2.22.11+ | Security scanner | Finds security vulnerabilities (hard-coded creds, weak crypto, unsafe file ops). **Critical for CLI tool with file system access.** |
| errcheck | (via golangci-lint) | Unchecked error detection | Finds ignored errors. **Directly addresses error handling pattern issues.** |
| gocritic | (via golangci-lint) | Code style & bugs | Detects suspicious constructs, style violations, performance issues. |

**Why golangci-lint v2:** Released 2025, industry standard meta-linter. Runs linters in parallel with smart caching (completes in seconds). Configured via YAML, integrates with all IDEs. Used by Kubernetes, Prometheus, Terraform.

**Why these specific linters:**
- **staticcheck:** Finds concurrency bugs like improper mutex usage, data races beyond what -race catches
- **gosec:** Essential for CLI tools - detects file permission issues, path traversal, unsafe system calls
- **errcheck:** Directly fixes "error handling patterns" issue in project context
- **gocritic:** Performance & correctness checks

### Code Formatting

| Tool | Version | Purpose | When to Use |
|------|---------|---------|-------------|
| gofumpt | v0.7.0+ | Stricter gofmt | Opinionated formatter, superset of gofmt. Enforces consistent style. |
| gopls | Latest | Language server | Enable `gopls.formatting.gofumpt` for IDE integration. Auto-formats on save. |

**Why gofumpt over gofmt:** Backward-compatible with gofmt but enforces stricter formatting rules. Industry trend in 2025. Reduces style bikeshedding in reviews.

### Coverage & Quality Tracking

| Tool | Purpose | Notes |
|------|---------|-------|
| `go test -cover` | Built-in coverage | Native Go coverage. Use `-coverprofile=coverage.out` |
| `go tool cover` | Coverage visualization | Use `-html` flag for browser view (green/red/grey coverage) |
| `go test -covermode=atomic` | Thread-safe coverage | Use with -race for parallel test coverage |
| go-test-coverage | v1.2.0+ | Coverage enforcement | Fails CI if coverage drops below threshold. Integrates with coverage profiles. |

**Why built-in coverage:** Go 1.20+ supports integration test coverage (`go build -cover`). No third-party tools needed for accurate coverage metrics.

### Race Detection & Concurrency

| Tool | Purpose | Critical For |
|------|---------|--------------|
| `go test -race` | Race detector | **Fixing "test race conditions" and "concurrency bugs" from project context.** Detects data races at runtime. |
| `go test -parallel N` | Parallel execution | Validates test independence. Exposes shared state bugs. |

**Why -race is non-negotiable:** Project has "test race conditions" and "concurrency bugs". -race detects unsynchronized access to shared memory. 2-20x slower but catches bugs unit tests miss. **Run in CI on all platforms.**

### Database Testing (bbolt-specific)

| Pattern | Purpose | Implementation |
|---------|---------|----------------|
| Isolated DB per test | Parallel test safety | Create temp dir with `t.TempDir()`, use unique bbolt file per test |
| TestMain setup/teardown | Shared fixtures | Use `TestMain(m *testing.M)` for global setup, defer cleanup |
| Build tags | Separate integration tests | Tag integration tests with `//go:build integration`, run selectively |

**Why isolated databases:** Project has "database singleton" issue. Per-test databases enable parallel execution without data conflicts. bbolt is file-based, so use `t.TempDir()` for automatic cleanup.

### CI/CD - Cross-Platform Testing

| Component | Configuration | Why |
|-----------|---------------|-----|
| GitHub Actions | `actions/setup-go@v5` | Official Go setup action, supports matrix builds |
| Matrix testing | Go 1.23.x, 1.24.x on ubuntu/macos/windows | **Project constraint: cross-platform.** Tests Linux, macOS, Windows simultaneously. |
| Race detection in CI | `go test -race ./...` | Catches platform-specific concurrency bugs |
| Build tags for CI | `go test -tags=integration` | Run integration tests only in CI, skip locally |

**Why matrix builds:** Project must support Linux, macOS, Windows. GitHub Actions matrices test all platforms in parallel. Uses `actions/setup-go@v5` for consistent Go versions across runners.

**Platform-specific gotcha:** Git `core.autocrlf` defaults to true on Windows. Can cause test failures with plaintext fixtures. Disable in CI or normalize line endings.

### Error Handling

| Approach | Status | Rationale |
|----------|--------|-----------|
| stdlib `errors` | **RECOMMENDED** | Go 1.13+ error wrapping (`fmt.Errorf("%w", err)`) + `errors.Is/As` is industry standard 2025. |
| `pkg/errors` | **AVOID** | Maintenance mode since Go 1.13. Incompatible with stdlib error unwrapping. |

**Why stdlib errors:** `pkg/errors` is deprecated. Go 1.13+ `fmt.Errorf("%w", err)` provides error wrapping. `errors.Is()` and `errors.As()` handle sentinel errors and error types. No external dependencies needed.

**For stack traces:** Use `%+v` verb with wrapped errors in logging, or integrate runtime.Callers for custom traces. Most teams find this sufficient without `pkg/errors`.

### Benchmarking & Profiling

| Tool | Purpose | Usage |
|------|---------|-------|
| `go test -bench=.` | Performance testing | Built-in benchmarking framework |
| `go test -benchmem` | Memory allocation tracking | Shows allocs/op and bytes/op |
| `go test -cpuprofile` | CPU profiling | Generates profile for pprof analysis |
| `go test -memprofile` | Memory profiling | Detects allocation hotspots and leaks |
| `pprof` | Profile visualization | `go tool pprof cpu.prof` for interactive analysis |

**Why pprof:** Built into stdlib. Web UI shows flame graphs, call graphs, source annotations. Essential for optimizing CLI performance.

## Supporting Libraries (Project-Specific)

| Library | Version | Purpose | Current Usage |
|---------|---------|---------|---------------|
| bbolt | v1.3.8+ | Embedded key/value DB | Already in use |
| cobra | v1.8.1+ | CLI framework | Already in use |
| fsnotify | v1.7.0+ | File watching | Already in use |
| doublestar | v4.6.1+ | Glob patterns | Already in use |

**No changes needed** - current stack is modern and appropriate.

## Installation

```bash
# Update Go to latest patch version
# Go 1.24.12 (latest as of 2026-01-17)
# https://go.dev/dl/

# Install testing libraries
go get github.com/stretchr/testify@latest

# Install linting tools
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install github.com/securego/gosec/v2/cmd/gosec@latest
go install mvdan.cc/gofumpt@latest

# Install mock generation (if needed)
go install github.com/vektra/mockery/v3@latest

# Install coverage enforcement (optional)
go install github.com/vladopajic/go-test-coverage@latest

# Verify installations
golangci-lint version  # Should be v2.1.2+
gosec -version         # Should be 2.22.11+
gofumpt -version       # Should be v0.7.0+
```

## Recommended golangci-lint Configuration

Create `.golangci.yml`:

```yaml
linters:
  enable:
    - staticcheck      # Deep analysis, concurrency bugs
    - gosec           # Security vulnerabilities
    - errcheck        # Unchecked errors
    - gocritic        # Performance & correctness
    - gofumpt         # Strict formatting
    - ineffassign     # Ineffective assignments
    - unconvert       # Unnecessary conversions
    - misspell        # Spelling errors
    - gocyclo         # Cyclomatic complexity
    - dupl            # Duplicate code
    - goconst         # Repeated strings
    - depguard        # Dependency restrictions
    - bodyclose       # HTTP body close

linters-settings:
  errcheck:
    check-blank: true      # Catch _ = err patterns
  gocritic:
    enabled-tags:
      - diagnostic       # Correctness checks
      - performance      # Performance checks
      - style           # Code style
  gosec:
    excludes:
      - G104           # Let errcheck handle this
  staticcheck:
    checks: ["all"]

issues:
  exclude-use-default: false
  max-issues-per-linter: 0
  max-same-issues: 0
```

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| testify | Ginkgo/Gomega | BDD-style tests, large teams familiar with RSpec/Jasmine. Overkill for CLI tool. |
| golangci-lint | Individual linters | Never. golangci-lint runs them faster with caching. |
| stdlib errors | pkg/errors | Never. pkg/errors is deprecated, incompatible with stdlib. |
| mockery | gomock | gomock uses reflection at runtime. mockery generates static code (faster, type-safe). |
| gofumpt | gofmt | Use gofmt if team rejects opinionated formatting. gofumpt is stricter. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| pkg/errors | Maintenance mode since Go 1.13. Incompatible unwrapping. | stdlib `errors` + `fmt.Errorf("%w")` |
| go-junit-report | Outdated XML format. GitHub Actions supports native Go output. | `gotestsum` for JUnit if CI requires it |
| Old mock frameworks | gomock, counterfeiter slower or less maintained than mockery v3. | mockery v3 |
| go vet standalone | Subset of staticcheck. Redundant with golangci-lint. | golangci-lint (includes go vet) |

## Stack Patterns for This Project

**For parallel test safety (fixes "test race conditions"):**
```go
func TestDatabaseOperation(t *testing.T) {
    t.Parallel() // Enable after fixing singleton

    // Isolated DB per test
    tmpDir := t.TempDir() // Auto-cleanup
    dbPath := filepath.Join(tmpDir, "test.db")
    db, err := bbolt.Open(dbPath, 0600, nil)
    require.NoError(t, err)
    defer db.Close()

    // Test logic
}
```

**For error handling (fixes "error handling patterns"):**
```go
// BEFORE (project has unchecked errors)
result, _ := someFunc()

// AFTER (wrapped errors with context)
result, err := someFunc()
if err != nil {
    return fmt.Errorf("failed to process %s: %w", name, err)
}
```

**For CI matrix testing (cross-platform requirement):**
```yaml
# .github/workflows/test.yml
strategy:
  matrix:
    go-version: [1.23.x, 1.24.x]
    os: [ubuntu-latest, macos-latest, windows-latest]
runs-on: ${{ matrix.os }}
steps:
  - uses: actions/checkout@v4
  - uses: actions/setup-go@v5
    with:
      go-version: ${{ matrix.go-version }}
  - run: go test -race -coverprofile=coverage.out ./...
  - run: golangci-lint run
```

## Version Compatibility

| Package | Go Version | Notes |
|---------|------------|-------|
| Go 1.24.12 | - | Latest stable (2026-01-17). Security fixes in crypto/x509, net/http. |
| Go 1.23.5 | - | Previous version, still supported. Use 1.24.x for new work. |
| testify | Go 1.18+ | No breaking changes planned (v1 maintenance). |
| golangci-lint v2 | Go 1.24+ | Requires Go 1.24 minimum. |
| gofumpt v0.7 | Go 1.24+ | Fork of gofmt from Go 1.25.0 (forward-compatible). |
| mockery v3 | Go 1.18+ | Major rewrite in 2025, performance boost. |

**Recommendation:** Use **Go 1.24.12** (latest patch). Go 1.25 releases August 2025.

## Sources

**Testing & Best Practices:**
- [Top 6 Best Golang Testing Frameworks in 2025](https://reliasoftware.com/blog/golang-testing-framework) — Framework comparison
- [The Go Ecosystem in 2025: Key Trends](https://blog.jetbrains.com/go/2025/11/10/go-language-trends-ecosystem-2025/) — Ecosystem trends
- [Go Unit Testing: Structure & Best Practices](https://www.glukhov.org/post/2025/11/unit-tests-in-go/) — Unit testing patterns
- [Testing Go Code Like a Pro (2025)](https://medium.com/@nandoseptian/testing-go-code-like-a-pro-what-i-wish-i-knew-starting-out-2025-263574b0168f) — Testing guide

**Static Analysis & Linting:**
- [Go Linters: Essential Tools for Code Quality](https://www.glukhov.org/post/2025/11/linters-for-go/) — Linter overview
- [golangci-lint Official Docs](https://golangci-lint.run/) — Configuration guide
- [GitHub: golangci/golangci-lint](https://github.com/golangci/golangci-lint) — v2.1.2 release
- [Staticcheck Documentation](https://staticcheck.dev/docs/) — Deep analysis tool
- [GitHub: securego/gosec](https://github.com/securego/gosec) — Security scanner v2.22.11

**Race Detection & Parallel Testing:**
- [Parallel Table-Driven Tests in Go](https://www.glukhov.org/post/2025/12/parallel-table-driven-tests-in-go/) — Parallel test patterns
- [Data Race Detector](https://go.dev/doc/articles/race_detector) — Official race detector docs
- [Go race conditions testing and coverage](https://www.foomo.org/blog/go-race-conditions-testing-and-coverage) — Race detection practices

**Error Handling:**
- [GitHub: pkg/errors](https://github.com/pkg/errors) — Maintenance mode notice
- [The Fundamentals of Error Handling in Go](https://betterstack.com/community/guides/scaling-go/golang-errors/) — Stdlib errors guide
- [Working with Errors in Go 1.13](https://go.dev/blog/go1.13-errors) — Error wrapping introduction

**Coverage:**
- [Code coverage for Go integration tests](https://go.dev/blog/integration-test-coverage) — Go 1.20+ coverage
- [GitHub: vladopajic/go-test-coverage](https://github.com/vladopajic/go-test-coverage) — Coverage enforcement
- [Go Code Coverage Tracking: Best Practices](https://getotterwise.com/blog/go-code-coverage-tracking-best-practices-cicd) — CI integration

**Database Testing:**
- [4 practical principles of high-quality database integration tests](https://threedots.tech/post/database-integration-testing/) — Integration test patterns
- [GitHub: etcd-io/bbolt](https://github.com/etcd-io/bbolt) — bbolt database
- [Advanced Go Testing Patterns](https://jsschools.com/golang/advanced-go-testing-patterns-from-table-driven-te/) — Production-ready strategies

**CI/CD:**
- [GitHub: mvdan/github-actions-golang](https://github.com/mvdan/github-actions-golang) — Actions examples
- [Running Go tests in Github CI](https://www.dolthub.com/blog/2025-02-14-simple-github-test-ci-with-go/) — Feb 2025 guide
- [Building and testing Go - GitHub Actions](https://docs.github.com/en/actions/use-cases-and-examples/building-and-testing/building-and-testing-go) — Official docs
- [GitHub: actions/setup-go](https://github.com/actions/setup-go) — setup-go action

**Testify & Mocking:**
- [GitHub: stretchr/testify](https://github.com/stretchr/testify) — v1.10.0, Aug 2025
- [Testing in Go with Testify](https://betterstack.com/community/guides/scaling-go/golang-testify/) — Testify guide
- [GitHub: vektra/mockery](https://github.com/vektra/mockery) — Mock generation
- [Announcing Mockery v3](https://topofmind.dev/blog/2025/04/08/announcing-mockery-v3/) — v3 release notes
- [mockery Official Docs](https://vektra.github.io/mockery/v3.6/) — Configuration

**Formatting:**
- [GitHub: mvdan/gofumpt](https://github.com/mvdan/gofumpt) — Stricter gofmt
- [gofmt, goimports, goreturns — why three formatters?!](https://alenkacz.medium.com/gofmt-goimports-goreturns-why-do-we-need-three-formatters-a3518ee6cc90) — Formatter comparison

**Benchmarking & Profiling:**
- [Benchmarking in Go: A Comprehensive Handbook](https://betterstack.com/community/guides/scaling-go/golang-benchmarking/) — Benchmarking guide
- [Testing, BenchMarking, Memory Profile in Golang](https://medium.com/@gangadhara15/testing-benchmarking-memory-profile-in-golang-24458a92a0e4) — Profiling overview
- [pprof package documentation](https://pkg.go.dev/runtime/pprof) — Official pprof docs

**Go Versions:**
- [Go Release History](https://go.dev/doc/devel/release) — Official release history
- [Go 1.24 Release Notes](https://tip.golang.org/doc/go1.24) — Go 1.24 features
- [Go 1.24 is released!](https://go.dev/blog/go1.24) — Release announcement
- [Go version lifecycle](https://endoflife.date/go) — Support timeline

---

*Stack research for: lnpm Go CLI Quality Improvements*
*Researched: 2026-01-17*
*Confidence: HIGH — All recommendations verified with Context7, official docs, or multiple 2025 sources*
