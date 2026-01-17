# Requirements: lnpm Stability & Quality

**Defined:** 2026-01-17
**Core Value:** Reliability and correctness across all platforms

## v1 Requirements (Production-Ready Baseline)

### Testing & Reliability

- [ ] **TEST-01**: All tests pass with `-race` flag — zero data races detected
- [ ] **TEST-02**: Parallel test execution enabled — tests use `t.Parallel()` where safe
- [ ] **TEST-03**: Integration test coverage for critical paths — publish, add, push, remove, watch workflows tested E2E
- [ ] **TEST-04**: Cross-platform CI validation — tests pass on Linux, macOS, Windows in GitHub Actions
- [ ] **TEST-05**: Flaky test fixes — symlink test works reliably in CI, no skip workarounds
- [ ] **TEST-06**: Test isolation — each test gets isolated DB and filesystem with `t.TempDir()`

### Architecture

- [ ] **ARCH-01**: Refactor database singleton to dependency injection — repository interfaces defined
- [ ] **ARCH-02**: Repository layer implementation — BoltDB wrapped in repository pattern
- [ ] **ARCH-03**: Service layer extraction — business logic separated from CLI commands
- [ ] **ARCH-04**: DI container creation — dependencies wired at startup, passed to commands
- [ ] **ARCH-05**: CLI migration to services — commands become thin, delegate to service layer
- [ ] **ARCH-06**: Singleton removal — `db.GetDB()` eliminated, DB passed through constructors

### Concurrency & Context

- [ ] **CONC-01**: Context support throughout — all long-running operations accept `context.Context`
- [ ] **CONC-02**: Graceful shutdown — handle SIGTERM/SIGINT, cleanup resources, bounded timeout
- [ ] **CONC-03**: Context cancellation — operations check `ctx.Done()`, terminate gracefully
- [ ] **CONC-04**: Race condition fixes — all identified races from CONCERNS.md resolved
- [ ] **CONC-05**: Watch mode concurrency tests — debouncing, event handling, goroutine lifecycle tested

### Error Handling

- [ ] **ERR-01**: All errors checked — zero unchecked error returns (`errcheck` linter clean)
- [ ] **ERR-02**: Error wrapping with context — all errors wrapped with `fmt.Errorf("context: %w", err)`
- [ ] **ERR-03**: String error comparison eliminated — use `errors.Is`/`errors.As` instead of string equality
- [ ] **ERR-04**: Deferred error handling — audit `defer Close()` patterns, check errors in critical paths
- [ ] **ERR-05**: Exit code standardization — 0=success, proper error codes, messages to stderr

### Security

- [ ] **SEC-01**: Input validation for package names — prevent path traversal attacks
- [ ] **SEC-02**: Store directory permissions — restrict to 0700, prevent unauthorized access
- [ ] **SEC-03**: Update mechanism verification — checksum/signature validation for downloaded binaries
- [ ] **SEC-04**: Atomic file operations — all writes use temp-rename pattern, corruption-resistant

### Code Quality

- [ ] **QUAL-01**: Linting setup — golangci-lint v2 with staticcheck, gosec, errcheck, gocritic enabled
- [ ] **QUAL-02**: Code formatting — gofumpt enforced, consistent style
- [ ] **QUAL-03**: Pre-commit hooks — lint and format checks run before commit
- [ ] **QUAL-04**: CI linting — golangci-lint runs in GitHub Actions, fails on new issues

## v2 Requirements (Post-Baseline Improvements)

### Test Coverage

- **TEST-07**: Comprehensive coverage target — >80% code coverage, critical paths 100%
- **TEST-08**: Reflink fallback tests — cross-filesystem copies, filesystems without reflink, permission errors
- **TEST-09**: Database corruption recovery — handle corrupted DB files, partial writes, bucket recovery
- **TEST-10**: Monorepo workspace edge cases — circular dependencies, invalid configurations, complex setups
- **TEST-11**: Cross-platform symlink tests — symlink preservation across platforms during publish/push
- **TEST-12**: Permission test coverage — read-only scenarios, graceful degradation

### Performance

- **PERF-01**: Eliminate two-pass file hashing — stream files directly to worker pool
- **PERF-02**: Database read optimization — use RWMutex for read operations, reduce lock contention
- **PERF-03**: Buffer pool implementation — sync.Pool for copy buffers, reduce allocations
- **PERF-04**: BoltDB monitoring — track file size, document limits, improve garbage collection

### Architecture

- **ARCH-07**: Platform-specific test coverage — reflink syscalls tested on target filesystems
- **ARCH-08**: Cross-device link testing — network filesystems, device boundary validation
- **ARCH-09**: Database migration system — schema versioning, backward-compatible upgrades
- **ARCH-10**: File descriptor limit handling — watch mode respects OS limits, prunes deep hierarchies

### Dependencies

- **DEP-01**: fsnotify abstraction — interface wrapper for file watching, enable alternative implementations
- **DEP-02**: BoltDB migration documentation — SQLite WAL mode migration path documented

### Observability

- **OBS-01**: Structured logging — migrate from debug flags to slog with levels
- **OBS-02**: Testscript DSL — comprehensive CLI testing with pattern matching
- **OBS-03**: Health check command — `lnpm doctor` validates environment, config, state

## Out of Scope

| Feature | Reason |
|---------|--------|
| Performance micro-optimizations | Defer until after baseline stability, focus on known bottlenecks only |
| TUI interface | Separate branch planned, not part of quality milestone |
| New user-facing features | Quality first, features after stability |
| SQLite migration | Documented as future path, not implemented in v1 |
| Breaking API changes | Must maintain backward compatibility for databases and lock files |
| OpenTelemetry integration | Advanced observability, defer to v2+ |
| Fuzz testing | Edge case discovery, defer to v2+ |
| Plugin system | Extensibility, defer to v2+ |

## Traceability

Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| TBD | TBD | Pending |

**Coverage:**
- v1 requirements: 39 total
- Mapped to phases: 0 (pending roadmap)
- Unmapped: 39 ⚠️

---
*Requirements defined: 2026-01-17*
*Last updated: 2026-01-17 after initial definition*
