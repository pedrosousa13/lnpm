# Feature Research

**Domain:** Production-ready Go CLI tools
**Researched:** 2026-01-17
**Confidence:** HIGH

## Feature Landscape

### Table Stakes (Users Expect These)

Features users assume exist. Missing these = product feels incomplete or unreliable.

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| **Comprehensive error handling** | All errors checked, wrapped with context | MEDIUM | Must check every error return, wrap with function context. Zero ignored/unchecked errors. |
| **Proper exit codes** | Standard 0=success, 1-255=failure conventions | LOW | Reserve `os.Exit` for main, return errors up stack. Print to stderr before exit. |
| **Race-free concurrency** | No data races in production | MEDIUM | All tests run with `-race`, goroutines properly synchronized, shared state protected. |
| **Table-driven unit tests** | Standard Go testing patterns | LOW | Uses stdlib `testing` package, table-driven tests, subtests with `t.Run()`. |
| **Integration/E2E tests** | Full workflow validation | HIGH | CLI tested via actual binary execution, validates STDOUT/STDERR/exit codes, uses fixtures. |
| **Context support** | Timeouts and cancellation | MEDIUM | Context propagated through operations, enables graceful shutdown, timeout handling. |
| **Graceful shutdown** | Signal handling (SIGTERM/SIGINT) | MEDIUM | `signal.NotifyContext()` for Ctrl+C, cleanup on termination, bounded shutdown timeout. |
| **Cross-platform compatibility** | Works on Linux, macOS, Windows | HIGH | Platform-specific code isolated, tests run on all platforms, filesystem assumptions validated. |
| **Structured logging** | Contextual, parseable logs | MEDIUM | Use slog/zerolog for structured output, log levels, timestamps, context correlation. |
| **CLI flag validation** | Proper arg parsing with help | LOW | Cobra/CLI frameworks with validation, auto-generated help, flag conflicts handled. |
| **Atomic operations** | File writes are atomic | MEDIUM | Temp file + rename pattern, prevents corruption on crash/interrupt. |
| **Idempotent operations** | Same command same result | MEDIUM | Repeated execution safe, checks before destructive ops, clear about side effects. |

### Differentiators (Competitive Advantage)

Features that set production-grade tools apart. Not required, but valuable for reliability.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| **Testscript/E2E DSL** | Comprehensive CLI behavior tests | HIGH | Pattern-matching CLI tests, validates edge cases, easier than shell scripts. |
| **Parallel test execution** | Fast test suite at scale | LOW | Tests use `t.Parallel()` where safe, isolated temp dirs, no shared global state. |
| **Permission handling** | Graceful degradation on permission issues | MEDIUM | Detects read-only filesystems, recovers from permission errors, clear error messages. |
| **Observability hooks** | Production debugging capability | MEDIUM | Trace correlation, metrics endpoints (Prometheus), OpenTelemetry integration. |
| **Database/state resilience** | Handles corruption, timeouts, locking | HIGH | DB open timeouts, corruption recovery, concurrent access safety, migration support. |
| **Comprehensive coverage** | >80% code coverage, critical paths 100% | MEDIUM | Coverage measured, integration tests cover critical paths, edge cases tested. |
| **Platform-specific optimizations** | Best performance per platform | HIGH | Reflink on APFS/Btrfs, hardlinks, parallel copy fallback with performance awareness. |
| **Config validation** | Schema validation, helpful errors | LOW | Config loaded with validation, defaults documented, migration on schema changes. |
| **Debug mode** | Verbose troubleshooting output | LOW | `--debug` flag or env var, timestamps, operation traces to stderr. |
| **Backward compatibility** | Version-to-version upgrades safe | HIGH | DB migrations tested, config backward compat, deprecation warnings before removal. |

### Anti-Features (Commonly Requested, Often Problematic)

Features that seem good but create reliability problems.

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| **Singleton global state** | Convenient, simple API | Impossible to test in parallel, tight coupling, goroutine races | Dependency injection: pass DB/config to functions explicitly |
| **Panic for errors** | Easier than error handling | Crashes production, no recovery, loses context | Return errors, handle at main level, only panic for truly unrecoverable bugs |
| **Skipped tests** | Tests flaky on some platforms | Masks real issues, reduces coverage, false confidence | Fix root cause: isolation, platform detection, conditional logic not skip |
| **Unchecked errors** | Less verbose code | Silent failures, data corruption, unpredictable behavior | Wrap all errors: `if err != nil { return fmt.Errorf("context: %w", err) }` |
| **defer without error check** | Cleaner syntax | Ignores close/cleanup failures, resource leaks | Check: `defer func() { if err := f.Close(); err != nil {...} }()` |
| **Global logger** | Simple, no injection needed | Can't test output, can't control levels per test, race conditions | Pass logger as dependency, structured logger with context |
| **File operations without atomic writes** | Simpler implementation | Corruption on crash, partial writes | Always: write temp → fsync → rename to final location |
| **t.Parallel() everywhere** | Faster test suite | Breaks tests with global state, hidden dependencies surface | Use selectively: only for isolated tests, document why tests can't parallelize |
| **Empty error messages** | Less code | Impossible to debug, no context on what failed where | Always wrap: operation name, file path, what was attempted |

## Feature Dependencies

```
[Error Handling]
    ├──requires──> [Exit Codes] (errors must map to exit codes)
    └──requires──> [Logging] (errors logged before exit)

[Context Support]
    ├──enables──> [Graceful Shutdown] (context cancellation triggers shutdown)
    ├──enables──> [Timeouts] (context deadlines for long operations)
    └──enables──> [Trace Correlation] (context carries trace IDs)

[Atomic Operations]
    └──requires──> [Error Handling] (atomic writes need rollback on error)

[Integration Tests]
    └──requires──> [Test Isolation] (temp dirs, separate DB instances)

[Dependency Injection]
    ├──enables──> [Parallel Tests] (no shared global state)
    └──enables──> [Testability] (mock dependencies in tests)

[Race Detection]
    └──requires──> [Proper Synchronization] (mutexes, channels, atomic)

[Cross-Platform]
    ├──requires──> [Platform-Specific Tests] (CI on all platforms)
    └──conflicts──> [Platform Assumptions] (hardcoded paths, shell commands)
```

### Dependency Notes

- **Error Handling requires Exit Codes:** Every error path must map to appropriate exit code (0=success, 1=general error, >1=specific failures)
- **Context enables Graceful Shutdown:** Signal handlers create cancelled context, operations check context.Done() and cleanup
- **Atomic Operations require Error Handling:** Write-temp-rename pattern must handle errors at each step and cleanup on failure
- **Dependency Injection enables Parallel Tests:** No singleton globals means tests can run in parallel with isolated dependencies
- **Cross-Platform conflicts with Platform Assumptions:** Hardcoded `/` paths break Windows, `sh` commands not available everywhere

## MVP Definition

### Production-Ready Baseline (Current Milestone)

Minimum quality level to call lnpm "production-ready":

- [x] **All errors checked and wrapped** — Zero `_ = err` patterns, all returns checked
- [x] **Exit codes standardized** — 0=success, proper error codes, messages to stderr
- [ ] **Race detector clean** — All tests pass with `-race`, no data races detected
- [ ] **Context propagation** — Long operations accept context, check cancellation
- [ ] **Graceful shutdown** — Handle SIGTERM/SIGINT, cleanup resources, bounded timeout
- [ ] **Integration test coverage** — Critical paths (publish, add, push, remove) tested E2E
- [ ] **Atomic file operations** — All writes use temp-rename, corruption-resistant
- [ ] **Refactor singleton DB** — Dependency injection for DB, config; eliminate global state
- [ ] **Permission test coverage** — Read-only scenarios tested, graceful degradation
- [ ] **Cross-platform CI** — Tests pass on Linux, macOS, Windows in CI

### Add After Baseline (Post-MVP)

Quality features to add once baseline is solid:

- [ ] **Testscript DSL** — CLI testing with pattern matching, cleaner than shell scripts
- [ ] **OpenTelemetry integration** — Trace correlation, distributed tracing capability
- [ ] **Benchmark suite** — Performance regression detection, optimization targets
- [ ] **Fuzz testing** — Discover edge cases in parsing, file handling
- [ ] **Database migrations** — Schema versioning, backward-compatible upgrades
- [ ] **Config schema validation** — Validate config on load, helpful error messages
- [ ] **Health check command** — `lnpm doctor` validates environment, config, state

### Future Consideration (v2+)

Advanced quality features for mature product:

- [ ] **Plugin system** — Extend functionality without forking
- [ ] **Telemetry/metrics** — Opt-in usage metrics for improvement insights
- [ ] **Auto-recovery** — Detect and fix common corruption scenarios
- [ ] **Multi-arch binaries** — Native ARM/x86 builds, optimal performance

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| Race-free concurrency | HIGH (correctness) | MEDIUM (fix singletons, add sync) | P1 |
| Context support | HIGH (reliability) | MEDIUM (thread through functions) | P1 |
| Graceful shutdown | HIGH (data safety) | MEDIUM (signal handling) | P1 |
| Refactor singletons | HIGH (testability) | HIGH (refactor all callers) | P1 |
| Integration tests | HIGH (confidence) | HIGH (fixtures, setup) | P1 |
| Atomic operations | HIGH (corruption prevention) | MEDIUM (already partially done) | P1 |
| Permission handling | MEDIUM (edge cases) | MEDIUM (already partially tested) | P2 |
| Testscript DSL | MEDIUM (cleaner tests) | HIGH (new testing approach) | P2 |
| OpenTelemetry | MEDIUM (debugging) | HIGH (integration complexity) | P2 |
| Database migrations | MEDIUM (upgrade safety) | MEDIUM (schema versioning) | P2 |
| Benchmark suite | LOW (optimization) | MEDIUM (define benchmarks) | P3 |
| Fuzz testing | LOW (edge case discovery) | MEDIUM (setup fuzzing) | P3 |
| Plugin system | LOW (extensibility) | HIGH (architecture change) | P3 |

**Priority key:**
- P1: Must have for production-ready status (current milestone)
- P2: Should have, add when P1 complete
- P3: Nice to have, future consideration

## Competitor Feature Analysis

| Feature | kubectl (production CLI) | gh (GitHub CLI) | lnpm (current) | lnpm (target) |
|---------|-------------------------|----------------|----------------|---------------|
| Error wrapping | Yes (k8s.io/apimachinery) | Yes (with context) | Partial (recent fixes) | Complete with context |
| Race detection | Clean with `-race` | Clean with `-race` | Fails on singletons | Clean with `-race` |
| Context support | Throughout (k8s ctx) | Yes (cobra ctx) | No (add milestone) | Yes (timeouts, cancel) |
| Graceful shutdown | Yes (SIGTERM) | Yes (signal handling) | No | Yes (signal.NotifyContext) |
| Integration tests | Extensive (E2E) | Comprehensive | Basic (fixtures) | Comprehensive E2E |
| Atomic operations | Yes (temp-rename) | Yes (safe writes) | Partial | Complete |
| DB/state handling | etcd (resilient) | API (stateless mostly) | bbolt singleton | DI, timeout, recovery |
| Cross-platform | All platforms in CI | All platforms in CI | CI (Mac/Linux) | All platforms + Windows |
| Structured logging | klog (levels, ctx) | Custom structured | Basic debug flag | slog with levels |
| Exit codes | Standard conventions | Standard conventions | Inconsistent | Standard (0, 1, >1) |

## Quality Anti-Patterns Found in lnpm (to fix)

Based on codebase analysis:

1. **Singleton DB pattern** (`db.GetDB()` with `sync.Once`)
   - **Problem:** Global state, cannot test in parallel, tight coupling
   - **Fix:** Dependency injection, pass `*DB` to functions
   - **Files affected:** All CLI commands, watch, internal packages

2. **Unchecked defer errors** (recently partially fixed)
   - **Problem:** `defer os.Chmod()` ignores errors, potential permission leaks
   - **Fix:** `defer func() { if err := ...; err != nil {...} }()`
   - **Status:** Mostly fixed, validate complete

3. **Tests skip on platform issues** (e.g., `t.Skip("flaky in CI")`)
   - **Problem:** Reduces coverage, masks real cross-platform bugs
   - **Fix:** Isolate properly, fix race conditions, conditional logic not skip
   - **Example:** Symlink tests skipped in CI

4. **`t.Parallel()` removed due to global state**
   - **Problem:** Tests can't parallelize because of singleton DB
   - **Fix:** DI allows test isolation, restore parallel execution
   - **Impact:** Slower test suite, hidden coupling

5. **ResetForTesting() hack**
   - **Problem:** Special testing function to reset singleton
   - **Fix:** No singleton = no reset needed
   - **File:** `internal/db/db.go:170`

## Sources

**Go CLI Best Practices:**
- [Go CLI Solutions - Official](https://go.dev/solutions/clis)
- [Build Production-Ready Go APIs 10x Faster in 2026](https://dev.to/vahiiiid/build-production-ready-go-apis-10x-faster-in-2026-the-ai-first-developers-guide-51io)
- [Go CLI Mastery: Crafting Developer Tools That Don't Suck](https://dev.to/tavernetech/go-cli-mastery-crafting-developer-tools-that-dont-suck-3p53)

**Testing Patterns:**
- [testscript: a DSL for testing CLI tools in Go](https://bitfieldconsulting.com/posts/cli-testing)
- [System Testing in Golang: End-to-End Testing in Practice (2026)](https://the-code-genin.medium.com/system-testing-in-golang-end-to-end-testing-in-practice-65e16a6eee85)
- [Writing Integration Tests for a Go CLI Application](https://lucapette.me/writing/writing-integration-tests-for-a-go-cli-application/)
- [GoLang Integration Testing: A Comprehensive Guide](https://payodatechnologyinc.medium.com/golang-integration-testing-a-comprehensive-guide-with-examples-7a1727e33be0)

**Error Handling:**
- [Error handling and Go - Official](https://go.dev/blog/error-handling-and-go)
- [Best Practices for Error Handling in Go - JetBrains](https://www.jetbrains.com/guide/go/tutorials/handle_errors_in_go/best_practices/)
- [A practical guide to error handling in Go - Datadog](https://www.datadoghq.com/blog/go-error-handling/)
- [Error Handling in Go: Patterns and Best Practices](https://dev.to/nguyn_ph_22a40697040669/error-handling-in-go-patterns-and-best-practices-38co)

**Race Detection:**
- [Data Race Detector - Official](https://go.dev/doc/articles/race_detector)
- [Introducing the Go Race Detector](https://go.dev/blog/race-detector)
- [Detecting Race Conditions With Go - Ardan Labs](https://www.ardanlabs.com/blog/2013/09/detecting-race-conditions-with-go.html)

**Context & Cancellation:**
- [Go Concurrency Patterns: Context - Official](https://go.dev/blog/context)
- [context package - Official Docs](https://pkg.go.dev/context)
- [Mastering Go Contexts: Cancellation, Timeouts, Request-Scoped Values](https://medium.com/@harshithgowdakt/mastering-go-contexts-a-deep-dive-into-cancellation-timeouts-and-request-scoped-values-392122ad0a47)
- [Golang Context - Cancellation, Timeout and Propagation](https://golangbot.com/context-timeout-cancellation/)

**Graceful Shutdown:**
- [Graceful Shutdown in Go: Practical Patterns](https://victoriametrics.com/blog/go-graceful-shutdown/)
- [How to Implement Graceful Shutdown in Go for Kubernetes (2026)](https://oneuptime.com/blog/post/2026-01-07-go-graceful-shutdown-kubernetes/view)
- [Graceful Shutdown Explained: Signals, Contexts, Shutdown Sequence](https://rafalroppel.medium.com/graceful-shutdown-in-go-explained-signals-contexts-and-the-correct-shutdown-sequence-f24fd9ef8fac)

**Exit Codes:**
- [How to Manage Process Exit Codes in Go](https://labex.io/tutorials/go-how-to-manage-process-exit-codes-in-go-431345)
- [Mastering Exit Codes in Go](https://medium.com/@thisara.weerakoon2001/signaling-success-and-failure-mastering-exit-codes-in-go-47fe3d884423)
- [Error Handling of CLI applications in Golang](https://dev.to/eminetto/error-handling-of-cli-applications-in-golang-4c7l)

**Logging & Observability:**
- [Logging in Go with Slog: A Practitioner's Guide](https://www.dash0.com/guides/logging-in-go-with-slog)
- [How to Set Up Structured Logging in Go with OpenTelemetry (2026)](https://oneuptime.com/blog/post/2026-01-07-go-structured-logging-opentelemetry/view)
- [Building Observability in Go: Logs, Metrics, and Traces](https://medium.com/@aryadotid21/building-observability-in-go-logs-metrics-and-traces-a3f980a7cdba)

**Singleton vs Dependency Injection:**
- [Singleton vs Dependency Injection - Enterprise Craftsmanship](https://enterprisecraftsmanship.com/posts/singleton-vs-dependency-injection/)
- [The Problems with Singletons and Why You Should Use DI Instead](https://medium.com/@fatihcyln/the-problems-with-singletons-and-why-you-should-use-di-instead-5a0fa0a5baed)
- [Database Connection Management: DI vs Singleton](https://leapcell.io/blog/database-connection-management-in-go-web-apps-a-dive-into-dependency-injection-vs-singleton)
- [Hands-On Implementation for Dependency Injection in Golang](https://reliasoftware.com/blog/practical-guide-to-dependency-injection-in-go-hands-on-implementation)

**Cross-Platform:**
- [Cross-Platform Go Libraries: Options, Pitfalls, and Conclusions](https://alek.dev/en/blog/go-libraries/)
- [Building a cross-platform TUI CLI app in Go](https://blog.techygrrrl.stream/cross-platform-tui-cli-go-lang)
- [Common Challenges in Cross-Platform App Development (2026)](https://www.techloy.com/common-challenges-in-cross-platform-app-development-and-how-to-overcome-them-in-2026/)

---
*Feature research for: Production-ready Go CLI tools*
*Researched: 2026-01-17*
