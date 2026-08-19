# lnpm Stability & Quality Improvements

## What This Is

lnpm is a fast, reliable local npm package development tool - a better alternative to yalc. It uses reflink/CoW + hard links for instant operations on large packages, with smart fallback strategies and cross-platform support. This project focuses on comprehensive stability improvements and tech debt resolution before adding performance enhancements and TUI features.

## Core Value

Reliability and correctness across all platforms. Every operation must work correctly on Linux, macOS, and Windows before optimizing for speed or adding new features.

## Requirements

### Validated

<!-- Existing functionality that works and is relied upon -->

- ✓ Publish packages to local store with content-addressable storage — existing
- ✓ Add packages to projects with intelligent linking (reflink → hardlink → copy fallback) — existing
- ✓ Watch mode with auto-sync changes to all linked projects — existing
- ✓ Monorepo workspace support (npm, yarn, pnpm, bun, Turborepo, Nx) — existing
- ✓ Cross-platform support (Linux, macOS, Windows) — existing
- ✓ CLI commands: publish, add, push, watch, remove, status, doctor, gc — existing
- ✓ Gitignore management for .lnpm directories — existing
- ✓ Lock file management (lnpm.lock) — existing
- ✓ Platform-specific optimizations (APFS reflink on macOS, Btrfs/XFS on Linux) — existing

### Active

<!-- Current scope - fixing all issues from CONCERNS.md -->

#### Tech Debt Resolution

- [ ] **DEBT-01**: Fix parallel testing - identify and resolve shared state issues, re-enable t.Parallel() where safe
- [ ] **DEBT-02**: Refactor database singleton to dependency injection pattern
- [ ] **DEBT-03**: Replace string error comparison with errors.Is/errors.As
- [ ] **DEBT-04**: Audit and fix unchecked defer Close() calls
- [ ] **DEBT-05**: Add context.Context support for cancellable operations

#### Bug Fixes

- [ ] **BUG-01**: Fix flaky symlink test in CI
- [ ] **BUG-02**: Make permission tests work on Windows or document platform-specific behavior
- [ ] **BUG-03**: Verify race condition fixes with thorough concurrent testing

#### Security Hardening

- [ ] **SEC-01**: Restrict store directory permissions to 0700
- [ ] **SEC-02**: Add input validation for package names (prevent path traversal)
- [ ] **SEC-03**: Add checksum/signature verification for update mechanism

#### Performance Improvements

- [ ] **PERF-01**: Eliminate two-pass file hashing, stream directly to worker pool
- [ ] **PERF-02**: Use RWMutex for database read operations
- [ ] **PERF-03**: Implement sync.Pool for copy buffers

#### Fragile Area Strengthening

- [ ] **FRAG-01**: Add comprehensive tests for reflink platform-specific code
- [ ] **FRAG-02**: Add comprehensive tests for file watcher debouncing and concurrent events
- [ ] **FRAG-03**: Add database migration system for schema changes
- [ ] **FRAG-04**: Test cross-device link detection on network filesystems

#### Scaling & Architecture

- [ ] **SCALE-01**: Monitor and document BoltDB file size limits, improve GC
- [ ] **SCALE-02**: Add file descriptor limit handling for watch mode
- [ ] **SCALE-03**: Implement finer-grained database locking

#### Dependency Risk Mitigation

- [ ] **DEP-01**: Abstract fsnotify behind interface for future flexibility
- [ ] **DEP-02**: Document BoltDB migration path (SQLite WAL mode planned)

#### Critical Missing Features

- [ ] **CRIT-01**: Add atomic transaction support for multi-package operations
- [ ] **CRIT-02**: Implement checksum verification for stored packages
- [ ] **CRIT-03**: Add file locking for inter-process coordination

#### Test Coverage

- [ ] **TEST-01**: Add comprehensive watch mode event handling tests
- [ ] **TEST-02**: Add reflink fallback scenario tests
- [ ] **TEST-03**: Add database corruption recovery tests
- [ ] **TEST-04**: Add monorepo workspace edge case tests
- [ ] **TEST-05**: Add cross-platform symlink handling tests

### Out of Scope

- Performance micro-optimizations beyond bottlenecks identified in CONCERNS.md — defer until after stability
- TUI interface implementation — planned for separate branch after stability
- New feature additions not related to reliability — defer until after stability
- Migration to SQLite — documented as future work, not in current scope
- Breaking API changes — must maintain backward compatibility

## Context

**Codebase State:**
- Brownfield Go project mapped via /gsd:map-codebase
- Architecture: Domain-driven CLI with bbolt database, intelligent linking strategies
- Platform-specific code: macOS APFS reflink, Linux Btrfs/XFS reflink, Windows fallback
- Concurrency: Worker pools for parallel hashing/copying, fsnotify-based watch mode
- Current branch: gitignore-improvements (recent addition of gitignore management)

**Recent Work:**
- Fixed multiple race conditions (commits e5f6793, d2df554)
- Added gitignore management to prevent husky breakage
- Identified comprehensive concerns via codebase audit

**Next Planned:**
- After this stability work: performance optimizations
- Separate branch: TUI interface development

## Constraints

- **Cross-platform**: Must work correctly on Linux, macOS, and Windows — no platform-specific regressions
- **Package manager compatibility**: Must support npm, yarn, pnpm, bun — all existing integrations must continue working
- **Backward compatibility**: Cannot break existing databases or lock files — migration paths required for schema changes
- **No external dependencies**: CLI tool remains standalone binary with no runtime dependencies
- **Go 1.26+**: Maintain current Go version requirement

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Fix all concerns before new features | Stability foundation required for performance work and TUI | — Pending |
| Dependency injection over singleton | Enables proper testing, eliminates global state race conditions | — Pending |
| context.Context for cancellation | Standard Go pattern, enables timeouts and graceful shutdown | — Pending |
| Maintain backward compatibility | Users rely on existing databases, no data loss acceptable | — Pending |

---
*Last updated: 2026-01-17 after initialization*
