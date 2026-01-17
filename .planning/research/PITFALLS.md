# Pitfalls Research

**Domain:** Go Quality Refactoring
**Researched:** 2026-01-17
**Confidence:** HIGH

## Critical Pitfalls

### Pitfall 1: Loop Variable Capture in Parallel Tests

**What goes wrong:**
When converting sequential tests to parallel with `t.Parallel()`, the loop variable gets shared across all subtests. Since subtests run concurrently, they all reference the same variable which gets overwritten, causing race conditions and non-deterministic test failures.

**Why it happens:**
Pre-Go 1.22, loop variables were shared across iterations. Developers forget to capture the loop variable before spawning parallel subtests, especially when refactoring existing sequential tests.

**How to avoid:**
```go
// WRONG - race condition
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        t.Parallel()
        // tt is shared across all subtests
    })
}

// CORRECT - capture loop variable
for _, tt := range tests {
    tt := tt  // Shadow loop variable
    t.Run(tt.name, func(t *testing.T) {
        t.Parallel()
        // Each subtest has own tt
    })
}
```

**Warning signs:**
- Parallel tests pass individually but fail when run together
- Test failures appear non-deterministic
- `go test -race` reports data races on test variables
- Go 1.20+ vet reports the issue

**Phase to address:**
Phase addressing parallel test migration. Run `go vet` during refactoring and enforce `-race` flag in CI.

---

### Pitfall 2: Defer + t.Parallel() = Premature Cleanup

**What goes wrong:**
When `t.Parallel()` is called, the test function returns immediately, causing `defer` statements to execute before subtests complete. Resources get cleaned up while parallel tests are still running, causing failures or leaks.

**Why it happens:**
Developers assume `defer` waits for subtests to complete, but `t.Parallel()` changes control flow. The parent test function exits immediately after spawning subtests.

**How to avoid:**
```go
// WRONG - defer runs before subtests finish
func TestDatabase(t *testing.T) {
    db := setupDB()
    defer db.Close()  // Closes immediately!

    for _, tt := range tests {
        tt := tt
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            // db already closed
        })
    }
}

// CORRECT - use t.Cleanup in subtest
func TestDatabase(t *testing.T) {
    db := setupDB()

    for _, tt := range tests {
        tt := tt
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            t.Cleanup(func() {
                db.Close()  // Cleanup per subtest
            })
            // or share db safely with proper sync
        })
    }
}
```

**Warning signs:**
- "connection closed" or "file already closed" errors in parallel tests
- Tests fail with "use of closed network connection"
- Resource leak warnings from linters

**Phase to address:**
Parallel test migration phase. Audit all `defer` statements when adding `t.Parallel()`. Use `t.Cleanup()` instead.

---

### Pitfall 3: Singleton → DI Without Interface Boundaries

**What goes wrong:**
Replacing database singleton with dependency injection but passing concrete `*sql.DB` everywhere instead of defining interfaces. This creates tight coupling, makes testing harder, and forces global state through constructor injection.

**Why it happens:**
Developers mechanically replace `db.Get()` with parameter passing without rethinking abstractions. "We're using DI now" becomes the goal instead of "we have better boundaries."

**How to avoid:**
```go
// WRONG - concrete dependency everywhere
func ProcessUser(db *sql.DB, userID int) error {
    // 50 functions all take *sql.DB
}

// CORRECT - interface at boundaries
type UserStore interface {
    GetUser(id int) (*User, error)
    UpdateUser(u *User) error
}

type userStore struct {
    db *sql.DB
}

func (s *userStore) GetUser(id int) (*User, error) {
    // db encapsulated
}

func ProcessUser(store UserStore, userID int) error {
    // Testable with mock UserStore
}
```

**Warning signs:**
- `*sql.DB` appears in 20+ function signatures
- Test setup still requires real database
- Circular import issues emerge
- Functions that only need GetUser also take full DB handle

**Phase to address:**
Dependency injection phase. Define domain interfaces before removing singleton. Encapsulate DB access behind store types.

---

### Pitfall 4: Error Wrapping Without Context

**What goes wrong:**
Adding error checks (`if err != nil`) to unchecked errors but wrapping with generic messages or not wrapping at all. Stack traces become useless, debugging requires printf debugging.

**Why it happens:**
Focus on "check all errors" checklist item without considering error *quality*. Quick fix: add `return err` everywhere without context about what failed.

**How to avoid:**
```go
// WRONG - no context
func LoadConfig(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err  // Where? Why?
    }
    // ...
}

// CORRECT - wrap with context
func LoadConfig(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("load config from %s: %w", path, err)
    }
    // ...
}
```

**Warning signs:**
- Error logs show "file not found" without saying which file
- Multiple call sites, can't tell which returned the error
- Errors.Is()/As() don't work because wrapping chain broken
- Production incidents require code archaeology to debug

**Phase to address:**
Error handling phase. Establish error wrapping convention: `fmt.Errorf("operation context: %w", err)`. Add linter rules.

---

### Pitfall 5: Goroutine Leaks During Refactoring

**What goes wrong:**
Adding concurrency to improve performance but spawning goroutines without ensuring they terminate. Each leaked goroutine holds memory and prevents GC, leading to resource exhaustion in production.

**Why it happens:**
Developer focuses on "make it concurrent" without considering lifecycle. No clear ownership of goroutine shutdown. Context cancellation not plumbed through.

**How to avoid:**
```go
// WRONG - goroutine never terminates
func WatchChanges() {
    go func() {
        for {
            // Infinite loop, no way to stop
            checkForChanges()
            time.Sleep(1 * time.Second)
        }
    }()
}

// CORRECT - context-based cancellation
func WatchChanges(ctx context.Context) {
    go func() {
        ticker := time.NewTicker(1 * time.Second)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                checkForChanges()
            case <-ctx.Done():
                return  // Goroutine terminates
            }
        }
    }()
}
```

**Warning signs:**
- Memory usage grows over time
- `runtime.NumGoroutine()` keeps increasing
- Goroutine profiles show thousands of leaked goroutines
- Tests never complete (goroutines blocked on channels)

**Phase to address:**
Concurrency refactoring phase. All goroutines must have clear termination path. Use context.Context for cancellation.

---

### Pitfall 6: Race Conditions in Shared State

**What goes wrong:**
Multiple goroutines accessing shared data without synchronization. Most insidious: works in dev/tests (single CPU), fails in prod (multi-CPU). According to Google SRE study, ~42% of critical outages trace to race conditions.

**Why it happens:**
Shared variables seem "simple enough" that explicit sync feels unnecessary. Race detector not run routinely. Optimistic assumption that operations are atomic when they're not.

**How to avoid:**
```go
// WRONG - unsynchronized map access
var cache = make(map[string]string)

func Get(key string) string {
    return cache[key]  // Race!
}

func Set(key, val string) {
    cache[key] = val  // Race!
}

// CORRECT - use sync.Map or mutex
var (
    cache = make(map[string]string)
    mu    sync.RWMutex
)

func Get(key string) string {
    mu.RLock()
    defer mu.RUnlock()
    return cache[key]
}

func Set(key, val string) {
    mu.Lock()
    defer mu.Unlock()
    cache[key] = val
}
```

**Warning signs:**
- Tests pass locally, fail in CI
- `go test -race` reports "DATA RACE"
- Sporadic panics: "concurrent map writes"
- Non-deterministic test failures

**Phase to address:**
Concurrency audit phase. Run ALL tests with `-race` flag. Use `sync.Mutex`, `sync.RWMutex`, `atomic` package, or channels. Never naked global vars.

---

### Pitfall 7: Global State in Parallel Tests

**What goes wrong:**
Tests modify global state (env vars, working directory, global config) while running in parallel. Tests interfere with each other, causing flaky failures that disappear when run individually.

**Why it happens:**
Original sequential tests used `os.Setenv()`, `os.Chdir()`, or global config safely. Parallel migration doesn't isolate state.

**How to avoid:**
```go
// WRONG - parallel tests + global state
func TestConfig(t *testing.T) {
    t.Parallel()
    os.Setenv("KEY", "value")  // Affects ALL tests!
    defer os.Unsetenv("KEY")
    // ...
}

// CORRECT - use t.Setenv (Go 1.17+)
func TestConfig(t *testing.T) {
    t.Parallel()
    t.Setenv("KEY", "value")  // Isolated to this test
    // ...
}

// OR: don't use t.Parallel() for integration tests
func TestConfigIntegration(t *testing.T) {
    // No t.Parallel() - modifies global state safely
    os.Setenv("KEY", "value")
    defer os.Unsetenv("KEY")
}
```

**Warning signs:**
- Tests pass when run alone: `go test -run TestFoo`
- Tests fail when run together: `go test ./...`
- CI failures that don't reproduce locally
- "permission denied" or "directory not found" in parallel tests

**Phase to address:**
Parallel test migration phase. Audit for `os.Setenv()`, `os.Chdir()`, global vars. Use `t.Setenv()` or remove `t.Parallel()` from integration tests.

---

## Technical Debt Patterns

Shortcuts that seem reasonable but create long-term problems.

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Using `_ = err` instead of proper handling | Silences linter fast | Hidden failures in production, no debugging info | Never - use `//nolint` with justification if truly intentional |
| Injecting `*sql.DB` without interface | Quick DI refactor, "it works" | Tight coupling, hard to test, can't swap implementations | Never - define domain interfaces |
| Global `var db *sql.DB` with Init() | "Single source of truth" | Impossible to test in parallel, hidden dependencies | Never in new code - refactor incrementally |
| Adding `t.Parallel()` to all tests | Faster test suite | Flaky tests from shared state, race conditions | Only for truly isolated unit tests |
| High coverage via shallow tests | Hit coverage %, green checkmark | False confidence, tests don't catch bugs | Never - quality over quantity |
| Wrapping errors without adding context | Satisfies "wrap errors" rule | Debugging requires printf archaeology | Never - context is the whole point |

## Integration Gotchas

Common mistakes when connecting to external services.

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| Database connections | Creating new connection per operation | Use connection pool (`sql.Open` returns pool) |
| HTTP clients | Creating new `http.Client` per request | Reuse client with connection pooling |
| File operations | Hardcoded paths with `/` or `\` | Use `filepath.Join()` for cross-platform compatibility |
| External APIs | No timeout, blocks forever | Set `context.WithTimeout()`, handle cancellation |
| Test databases | Shared DB across parallel tests | Separate DB per test or no `t.Parallel()` |

## Performance Traps

Patterns that work at small scale but fail as usage grows.

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| Parallel tests on fast operations | Tests slower after adding `t.Parallel()` | Only parallelize blocking I/O tests | Overhead > test runtime (< 1ms tests) |
| Goroutine per request without limits | Works in dev, OOMs in prod | Use worker pool, limit concurrency | > 1000 concurrent requests |
| Global mutex on hot path | High CPU usage, slow requests | Use `sync.RWMutex` or sharding | > 100 req/sec contention |
| Unbuffered channels in loops | Goroutines block, memory leaks | Use buffered channels or worker pool | > 10 producers/consumers |
| Testing with `-race` in CI | CI times out or OOMs | Run `-race` selectively, not on all tests | > 10k tests or integration tests |

## Security Mistakes

Domain-specific security issues beyond general web security.

| Mistake | Risk | Prevention |
|---------|------|------------|
| Path traversal in file operations | User input like `../../etc/passwd` escapes intended directory | Use `filepath.Clean()`, validate with `filepath.HasPrefix()` |
| Error messages exposing paths | Stack traces reveal `/home/deploy/secrets/` | Strip absolute paths from production errors |
| Race conditions in auth checks | Time-of-check vs time-of-use bugs | Atomic operations, proper locking |
| Goroutine context leaks | Cancelled requests still access DB/secrets | Check `ctx.Err()` before sensitive operations |

## "Looks Done But Isn't" Checklist

Things that appear complete but are missing critical pieces.

- [ ] **Parallel tests:** Often missing loop variable capture — verify `tt := tt` before `t.Parallel()`
- [ ] **Error handling:** Often missing context in wrapping — verify `fmt.Errorf("context: %w", err)` not bare `return err`
- [ ] **Dependency injection:** Often missing interfaces — verify domain interfaces exist, not just `*sql.DB` params
- [ ] **Concurrency:** Often missing cancellation — verify goroutines have `ctx.Done()` or other termination
- [ ] **Cross-platform code:** Often missing `filepath` usage — verify no hardcoded `/` or `\` in paths
- [ ] **Test cleanup:** Often missing `t.Cleanup()` in parallel tests — verify no `defer` in parent test with `t.Parallel()` subtests
- [ ] **Race detection:** Often missing from CI — verify `-race` flag in test pipeline
- [ ] **Global state in tests:** Often missing isolation — verify no `os.Setenv()` in tests with `t.Parallel()`

## Recovery Strategies

When pitfalls occur despite prevention, how to recover.

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Loop variable capture | LOW | Add `tt := tt` line, rerun tests with `-race` |
| Defer + parallel | LOW | Move `defer` to `t.Cleanup()` in subtest |
| Singleton without interfaces | HIGH | Define interfaces incrementally, refactor call sites one package at a time |
| Error wrapping | MEDIUM | Add wrapper functions, use linter to enforce, fix call sites |
| Goroutine leaks | MEDIUM | Add context plumbing, profile with `pprof goroutine`, fix leaks in hot paths first |
| Race conditions | HIGH | Run `-race`, fix reported races, add sync primitives, possible architectural changes |
| Global state in tests | LOW-MEDIUM | Use `t.Setenv()` or remove `t.Parallel()`, rerun test suite |

## Pitfall-to-Phase Mapping

How roadmap phases should address these pitfalls.

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| Loop variable capture | Parallel test migration | `go vet` clean, all tests have `tt := tt` |
| Defer + parallel | Parallel test migration | Audit: no `defer` in parent tests with `t.Parallel()` subtests |
| Singleton without interfaces | Dependency injection | Each package has domain interfaces, no `*sql.DB` in business logic |
| Error wrapping | Error handling | All errors have context, `errors.Is()`/`As()` work |
| Goroutine leaks | Concurrency refactoring | `pprof goroutine` shows stable count, all goroutines have exit path |
| Race conditions | Concurrency audit | CI runs `-race`, zero data races reported |
| Global state in tests | Parallel test migration | Tests use `t.Setenv()` or skip `t.Parallel()` |

## Sources

**Singleton to DI Refactoring:**
- [How not to do dependency injection - the static or singleton container](https://www.devtrends.co.uk/blog/how-not-to-do-dependency-injection-the-static-or-singleton-container)
- [Database Connection Management in Go Web Apps: DI vs. Singleton](https://leapcell.io/blog/database-connection-management-in-go-web-apps-a-dive-into-dependency-injection-vs-singleton)
- [Dependency Injection in Go](https://www.linkedin.com/pulse/dependency-injection-go-rafael-vargas)

**Parallel Testing:**
- [Parallel Table-Driven Tests in Go](https://www.glukhov.org/post/2025/12/parallel-table-driven-tests-in-go/)
- [Be Careful with Table Driven Tests and t.Parallel()](https://gist.github.com/posener/92a55c4cd441fc5e5e85f27bca008721)
- [Parallel Execution of Table-Driven Tests in Go](https://dasroot.net/posts/2025/12/parallel-execution-of-table-driven/)
- [On Using Go's t.Parallel()](https://brandur.org/t-parallel)
- [Cutting Test Runtime by 60% with Selective Parallelism in Go](https://mattermost.com/blog/cutting-test-runtime-by-60-with-selective-parallelism-in-go/)

**Error Handling:**
- [Mastering Go Error Handling: A Practical Guide](https://dev.to/leapcell/mastering-go-error-handling-a-practical-guide-3411)
- [Go Error Handling Best Practices and the errors Package](https://dasroot.net/posts/2025/12/go-error-handling-best-practices-and/)
- [Best Practices for Error Handling in Go](https://www.jetbrains.com/guide/go/tutorials/handle_errors_in_go/best_practices/)

**Test Coverage:**
- [Understanding Go Coverage: A Guide to Test Coverage in Go](https://dev.to/keploy/understanding-go-coverage-a-guide-to-test-coverage-in-go-2gg3)
- [Go Code Coverage Tracking: Best Practices and CI/CD Integration](https://getotterwise.com/blog/go-code-coverage-tracking-best-practices-cicd)
- [Go 1.24 Release Notes](https://go.dev/doc/go1.24)

**Concurrency & Race Conditions:**
- [7 Powerful Golang Concurrency Patterns That Will Transform Your Code in 2025](https://cristiancurteanu.com/7-powerful-golang-concurrency-patterns-that-will-transform-your-code-in-2025/)
- [Concurrency in Go: Race Conditions, Deadlocks, and Common Pitfalls](https://medium.com/@nagarjun_nagesh/concurrency-in-go-race-conditions-deadlocks-and-common-pitfalls-52243faf1a2f)
- [Mastering Go Concurrency: Taming Race Conditions Like a Pro](https://dev.to/jones_charles_ad50858dbc0/mastering-go-concurrency-taming-race-conditions-like-a-pro-1kn2)
- [7 Common Concurrency Pitfalls in Go (And How to Avoid Them)](https://cristiancurteanu.com/7-common-concurrency-pitfalls-in-go-and-how-to-avoid-them/)

**Cross-Platform:**
- [What are the most common cross-platform compatibility mistakes to avoid?](https://www.linkedin.com/advice/1/what-most-common-cross-platform-compatibility)
- [Go 1.24 Release Notes](https://go.dev/doc/go1.24)

---
*Pitfalls research for: Go Quality Refactoring*
*Researched: 2026-01-17*
