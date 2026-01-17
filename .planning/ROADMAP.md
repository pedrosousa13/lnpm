# Roadmap: lnpm Stability & Quality

## Overview

Transform lnpm from functional to production-ready by refactoring singleton database pattern to dependency injection, eliminating race conditions, and achieving comprehensive test coverage. Seven phases move from foundational tooling through architecture refactoring to full cross-platform validation.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [ ] **Phase 1: Foundation** - Error handling, linting, quality tooling
- [ ] **Phase 2: Architecture Prep** - Domain repository interfaces
- [ ] **Phase 3: Dependency Injection** - Repository layer, services
- [ ] **Phase 4: CLI Migration** - Remove singleton, wire DI
- [ ] **Phase 5: Concurrency Safety** - Race fixes, context propagation, graceful shutdown
- [ ] **Phase 6: Parallel Tests** - Test isolation, parallel execution
- [ ] **Phase 7: Integration Tests** - Cross-platform validation, E2E workflows

## Phase Details

### Phase 1: Foundation
**Goal**: Establish quality baseline with comprehensive error handling and static analysis tooling
**Depends on**: Nothing (first phase)
**Requirements**: ERR-01, ERR-02, ERR-03, ERR-04, ERR-05, QUAL-01, QUAL-02, QUAL-03, QUAL-04
**Success Criteria** (what must be TRUE):
  1. All errors checked - zero unchecked returns, errcheck linter passes
  2. All errors wrapped with context - every return includes operation context via fmt.Errorf
  3. String error comparison eliminated - codebase uses errors.Is/errors.As exclusively
  4. golangci-lint v2 configured - runs in CI with staticcheck, gosec, errcheck, gocritic enabled
  5. All CI checks pass - race detector, linters, formatting enforced
**Plans**: TBD

Plans:
- [ ] 01-01-PLAN.md: TBD
- [ ] 01-02-PLAN.md: TBD

### Phase 2: Architecture Prep
**Goal**: Define domain repository interfaces without behavior changes
**Depends on**: Phase 1
**Requirements**: ARCH-01, ARCH-02
**Success Criteria** (what must be TRUE):
  1. Repository interfaces defined - PackageRepository, ProjectRepository, FileStore documented
  2. Interface boundaries clear - each service declares narrow interface for its needs
  3. Zero implementation - interfaces only, no breaking changes to existing code
**Plans**: TBD

Plans:
- [ ] 02-01-PLAN.md: TBD

### Phase 3: Dependency Injection
**Goal**: Wrap BoltDB behind repositories, create application services with injected dependencies
**Depends on**: Phase 2
**Requirements**: ARCH-03, ARCH-04
**Success Criteria** (what must be TRUE):
  1. Repository implementations exist - BoltDB wrapped in types implementing Phase 2 interfaces
  2. Application services created - PackageService, LinkService, WatchService accept repository interfaces
  3. DI container wired - constructor assembles dependencies at startup
  4. Singleton still exists but wrapped - db.GetDB() works, services don't call it
  5. Services independently testable - unit tests use mocked repositories
**Plans**: TBD

Plans:
- [ ] 03-01-PLAN.md: TBD
- [ ] 03-02-PLAN.md: TBD

### Phase 4: CLI Migration
**Goal**: CLI commands delegate to services, singleton pattern fully removed
**Depends on**: Phase 3
**Requirements**: ARCH-05, ARCH-06
**Success Criteria** (what must be TRUE):
  1. CLI commands are thin - parse input, call service methods, format output
  2. db.GetDB() eliminated - no global database access, DI only
  3. All commands wired - publish, add, push, watch, remove, status, doctor, gc use services
  4. Zero behavior changes - functionality identical to pre-refactor
  5. Integration tests pass - full command execution works via DI
**Plans**: TBD

Plans:
- [ ] 04-01-PLAN.md: TBD

### Phase 5: Concurrency Safety
**Goal**: Eliminate race conditions, add context propagation, enable graceful shutdown
**Depends on**: Phase 4
**Requirements**: CONC-01, CONC-02, CONC-03, CONC-04, CONC-05, SEC-02, SEC-04
**Success Criteria** (what must be TRUE):
  1. All goroutines cancellable - context.Context passed through operations
  2. Signal handling works - SIGTERM/SIGINT trigger cleanup with bounded timeout
  3. Zero race conditions - all tests pass with -race flag
  4. Watch mode concurrency tested - debouncing, event handling, goroutine lifecycle verified
  5. Atomic file operations - all writes use temp-rename pattern
  6. Store directory permissions enforced - 0700 mode on creation
**Plans**: TBD

Plans:
- [ ] 05-01-PLAN.md: TBD
- [ ] 05-02-PLAN.md: TBD

### Phase 6: Parallel Tests
**Goal**: Enable parallel test execution with isolated environments
**Depends on**: Phase 5
**Requirements**: TEST-01, TEST-02, TEST-06
**Success Criteria** (what must be TRUE):
  1. Parallel execution safe - tests use t.Parallel() with no flakes
  2. Test isolation complete - each test gets isolated DB and filesystem via t.TempDir()
  3. Loop variable capture fixed - Go 1.22+ patterns or explicit shadowing
  4. No shared global state - env vars use t.Setenv(), working dir isolated
  5. CI runs with -race - GitHub Actions enforces race detection
**Plans**: TBD

Plans:
- [ ] 06-01-PLAN.md: TBD

### Phase 7: Integration Tests
**Goal**: Validate full workflows cross-platform with E2E tests
**Depends on**: Phase 6
**Requirements**: TEST-03, TEST-04, TEST-05, SEC-01, SEC-03
**Success Criteria** (what must be TRUE):
  1. E2E workflows tested - publish, add, push, remove, watch tested via actual binary
  2. Cross-platform CI passes - tests run on Linux, macOS, Windows in GitHub Actions
  3. Symlink handling verified - symlinks preserved across platforms during publish/push
  4. Input validation enforced - package names reject path traversal attempts
  5. Update mechanism secured - checksum/signature verification implemented
**Plans**: TBD

Plans:
- [ ] 07-01-PLAN.md: TBD
- [ ] 07-02-PLAN.md: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 → 6 → 7

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Foundation | 0/2 | Not started | - |
| 2. Architecture Prep | 0/1 | Not started | - |
| 3. Dependency Injection | 0/2 | Not started | - |
| 4. CLI Migration | 0/1 | Not started | - |
| 5. Concurrency Safety | 0/2 | Not started | - |
| 6. Parallel Tests | 0/1 | Not started | - |
| 7. Integration Tests | 0/2 | Not started | - |
