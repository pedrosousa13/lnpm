# Project Research Summary

**Project:** lnpm Go CLI Quality Improvements
**Domain:** Production-ready Go CLI tooling
**Researched:** 2026-01-17
**Confidence:** HIGH

## Executive Summary

lnpm is a Go CLI tool for local npm package linking that needs quality refactoring to reach production-ready status. Research shows the primary blocker is **singleton database pattern** preventing parallel tests and proper dependency injection. This creates cascading issues: race conditions, tight coupling, and flaky tests.

The recommended approach follows standard Go quality patterns: **dependency injection** to eliminate singletons, **repository interfaces** for testable boundaries, **context propagation** for cancellation/timeouts, and **race-free concurrency** with proper synchronization. The stack is solid (stdlib testing + testify + golangci-lint) — no new frameworks needed. Focus should be on **refactoring architecture**, not adopting new tools.

Key risk: refactoring singleton to DI touches every package. Mitigation: **incremental phased approach** — define interfaces first, wrap existing DB, create services, then migrate CLI. Each phase testable independently. Critical: run `-race` flag throughout to catch concurrency bugs early.

## Key Findings

### Recommended Stack

Go 1.24.12 with stdlib testing provides everything needed. Add testify for ergonomic assertions, golangci-lint v2 for static analysis (staticcheck + errcheck + gosec), and mockery v3 for interface mocks. No external test frameworks needed — Ginkgo/BDD tools are overkill for CLI.

**Core technologies:**
- **Go stdlib testing + testify**: Industry standard, supports all test types (unit/integration/fuzz/benchmark) with better assertions than bare `if` checks
- **golangci-lint v2**: Meta-linter running 50+ linters in parallel, catches race conditions, error handling bugs, security issues. Essential for stability goals
- **mockery v3**: Auto-generate type-safe mocks from interfaces, integrates with testify/mock. 2025 rewrite with order-of-magnitude performance improvements
- **Race detector (`-race`)**: Non-negotiable for catching concurrency bugs. Project has known test race conditions and concurrency issues

**Critical version note:** golangci-lint v2 requires Go 1.24+. Current project should upgrade to Go 1.24.12 (latest patch with security fixes).

### Expected Features

**Must have (table stakes):**
- Comprehensive error handling — all errors checked, wrapped with context (`fmt.Errorf("%w", err)`)
- Race-free concurrency — all tests pass with `-race`, goroutines properly synchronized
- Context support — timeouts, cancellation through operations
- Graceful shutdown — signal handling (SIGTERM/SIGINT) with cleanup
- Integration/E2E tests — full workflow validation via actual binary execution
- Atomic file operations — temp-file-rename pattern prevents corruption
- Cross-platform compatibility — works on Linux, macOS, Windows

**Should have (competitive):**
- Parallel test execution — tests use `t.Parallel()` with isolated environments
- Comprehensive coverage — >80% overall, 100% on critical paths
- Database resilience — handles corruption, timeouts, concurrent access
- Debug mode — `--debug` flag for verbose troubleshooting

**Defer (v2+):**
- Testscript DSL — pattern-matching CLI tests
- OpenTelemetry integration — distributed tracing
- Fuzz testing — edge case discovery
- Database migrations — schema versioning

### Architecture Approach

Standard layered architecture: CLI handlers delegate to application services, services depend on domain repository interfaces, repositories implement infrastructure (BoltDB, filesystem). **Critical pattern: dependency injection via constructor** eliminates singleton, enables parallel tests.

**Major components:**
1. **CLI Layer (Cobra)** — thin handlers: parse input, call services, format output
2. **Application Services** — business logic (PackageService, LinkService, WatchService) with injected dependencies
3. **Domain Repositories** — interfaces defined by consumers, implemented by infrastructure (repository pattern isolates DB)
4. **Infrastructure** — BoltDB, filesystem, platform-specific code behind interfaces

**Key architectural decision:** Accept interfaces, return structs. Each service defines narrow interfaces for its needs (not provider-defined interfaces). This enables mocking exactly what's needed without interface pollution.

### Critical Pitfalls

1. **Loop variable capture in parallel tests** — Pre-Go 1.22 loop variables shared across iterations cause races when adding `t.Parallel()`. Fix: `tt := tt` to shadow loop variable before subtest
2. **Singleton → DI without interfaces** — Mechanically replacing `db.Get()` with constructor params but passing concrete `*sql.DB` everywhere creates tight coupling. Fix: define domain repository interfaces, encapsulate DB behind store types
3. **Error wrapping without context** — Adding `if err != nil` checks but wrapping with generic messages loses debugging context. Fix: `fmt.Errorf("operation context %s: %w", path, err)` at every layer
4. **Goroutine leaks** — Adding concurrency without lifecycle management causes resource exhaustion. Fix: context-based cancellation (`ctx.Done()`) for all goroutines
5. **Race conditions in shared state** — Works in dev (single CPU), fails in prod (multi-CPU). Fix: run ALL tests with `-race` in CI, use mutexes/atomic/channels for shared data
6. **Global state in parallel tests** — Tests modify env vars/working dir while running parallel, causing flakes. Fix: use `t.Setenv()` or remove `t.Parallel()` from integration tests
7. **Defer + t.Parallel() = premature cleanup** — Parent test function exits after spawning parallel subtests, causing `defer` to run before subtests finish. Fix: use `t.Cleanup()` in subtests

## Implications for Roadmap

Based on architecture research (Phase 1-7 refactoring order), suggested structure groups related changes and minimizes risk.

### Phase 1: Foundation (Error Handling + Linting)
**Rationale:** Low-risk baseline improvements, no architectural changes yet. Establishes quality tooling before refactoring
**Delivers:** All errors checked/wrapped, golangci-lint configured, CI runs race detector
**Addresses:** Error handling (table stakes), static analysis setup
**Avoids:** Pitfall #3 (error wrapping without context), sets up tools to catch future issues

**Research flag:** NO — error handling patterns well-documented, use stdlib `errors` + `fmt.Errorf("%w")`

### Phase 2: Architecture Prep (Domain Interfaces)
**Rationale:** Define repository interfaces before breaking singleton. No behavior changes, low risk
**Delivers:** Domain repository interfaces (PackageRepository, ProjectRepository, FileStore)
**Addresses:** Architecture foundation for DI
**Avoids:** Pitfall #2 (singleton → DI without interfaces)

**Research flag:** NO — interface patterns standard, documented in ARCHITECTURE.md

### Phase 3: Dependency Injection (Repository Layer)
**Rationale:** Wrap existing DB behind repositories, create services with injected dependencies. Singleton still exists but wrapped
**Delivers:** Repository implementations, application services, DI container
**Addresses:** Refactor singleton DB (table stakes), testability
**Avoids:** Pitfall #2 (interfaces defined in Phase 2), enables testing services independently

**Research flag:** NO — DI patterns covered in research, follows Phase 1-4 from ARCHITECTURE.md

### Phase 4: CLI Migration + Singleton Removal
**Rationale:** CLI uses services, remove `db.GetDB()` singleton. Breaking change isolated to container
**Delivers:** CLI commands delegate to services, singleton pattern removed
**Addresses:** Complete DI refactor, eliminate global state
**Avoids:** Breaking changes isolated, functionality unchanged (services tested in Phase 3)

**Research flag:** NO — Standard refactoring, service layer already exists

### Phase 5: Concurrency Safety (Race Fixes + Context)
**Rationale:** DI complete, now safe to fix concurrency issues and add context propagation
**Delivers:** All goroutines have cancellation, mutexes on shared state, context throughout
**Addresses:** Race-free concurrency (table stakes), context support (table stakes), graceful shutdown
**Avoids:** Pitfall #4 (goroutine leaks), #5 (race conditions), #6 (global state)

**Research flag:** NO — Concurrency patterns covered, use stdlib `context` + `sync`

### Phase 6: Parallel Tests + Coverage
**Rationale:** After singleton removed and races fixed, safe to parallelize tests
**Delivers:** Tests use `t.Parallel()` with isolated environments, >80% coverage
**Addresses:** Parallel test execution (competitive), comprehensive coverage (competitive)
**Avoids:** Pitfall #1 (loop variable capture), #6 (global state in tests), #7 (defer + parallel)

**Research flag:** NO — Parallel testing patterns in PITFALLS.md, standard Go testing

### Phase 7: Integration Tests + Cross-Platform
**Rationale:** With unit tests solid, add E2E validation and platform testing
**Delivers:** CLI integration tests, CI on Linux/macOS/Windows, atomic file ops
**Addresses:** Integration tests (table stakes), cross-platform (table stakes), atomic operations (table stakes)
**Avoids:** Platform-specific bugs caught in CI

**Research flag:** MAYBE — If platform-specific features (reflink, device checks) need deeper investigation

### Phase Ordering Rationale

- **Phases 1-2 (Foundation + Interfaces)**: Low risk, no breaking changes, sets up quality tooling
- **Phases 3-4 (DI Refactor)**: Follows proven ARCHITECTURE.md order (Phase 1-6), each phase testable independently
- **Phase 5 (Concurrency)**: Requires DI complete (can't fix races with singleton), enables graceful shutdown
- **Phase 6 (Parallel Tests)**: Only safe after singleton removed and races fixed (Dependencies: Phase 4, 5)
- **Phase 7 (Integration)**: Tests full stack after architecture stable

**Critical path dependencies:**
```
Phase 1 (tooling) → Phase 2 (interfaces) → Phase 3 (repos) → Phase 4 (CLI migration) → Phase 5 (concurrency) → Phase 6 (parallel tests) → Phase 7 (integration)
```

Cannot skip phases without risk. Early phases enable later phases.

### Research Flags

**Needs research during planning:**
- Phase 7 (Integration Tests) — IF platform-specific features complex (reflink, device checks). Otherwise standard patterns sufficient

**Standard patterns (skip research-phase):**
- Phase 1 — Error handling stdlib patterns, golangci-lint config template in STACK.md
- Phase 2 — Interface definitions, no implementation
- Phase 3 — Repository pattern well-documented
- Phase 4 — CLI refactoring standard Cobra usage
- Phase 5 — Context/concurrency stdlib patterns
- Phase 6 — Parallel testing patterns in PITFALLS.md

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | All recommendations verified with official docs, 2025+ sources. No controversial choices |
| Features | HIGH | Production CLI feature landscape well-documented. MVP baseline clear from competitor analysis |
| Architecture | HIGH | DI patterns standard, phased refactoring order from multiple sources aligns |
| Pitfalls | HIGH | Parallel testing pitfalls extensively documented, race condition patterns validated |

**Overall confidence:** HIGH

### Gaps to Address

**During Phase 7 planning:**
- Platform-specific optimizations (reflink on APFS/Btrfs) — verify if existing implementation sufficient or needs enhancement
- Windows CI testing — may need Windows-specific test fixtures or path handling

**During execution:**
- Test suite performance after parallelization — monitor if `-race` flag causes CI timeouts (>10k tests). May need selective race detection
- Database migration strategy — if schema changes needed, validate backward compatibility approach

**No significant research gaps.** All major questions answered. Roadmap can proceed to requirements definition.

## Sources

### Primary (HIGH confidence)
- **Go Official Docs** — stdlib testing, context, errors, race detector
- **golangci-lint Official** — v2 configuration, linter selection
- **testify/mockery GitHub** — v1.10.0, v3.6.0 releases and patterns
- **Context7 research** — 40+ sources across STACK, FEATURES, ARCHITECTURE, PITFALLS

### Secondary (MEDIUM confidence)
- **Production CLI analysis** — kubectl, gh patterns (observed via docs, not internal implementation)
- **2025 ecosystem trends** — JetBrains Go ecosystem report, testing frameworks comparison

### Tertiary (LOW confidence)
- None — all findings verified with primary sources or multiple secondary sources

---
*Research completed: 2026-01-17*
*Ready for roadmap: yes*
