# Codebase Concerns

**Analysis Date:** 2026-01-17

## Tech Debt

**Parallel testing disabled:**
- Issue: `t.Parallel()` removed from tests to avoid race conditions
- Files: All test files in `tests/` and `internal/*/` directories
- Impact: Tests run sequentially, slower CI/CD pipeline
- Fix approach: Identify and fix shared state issues, re-enable parallel testing for isolated tests

**Database singleton with global state:**
- Issue: Single DB instance with `sync.Once` initialization in `internal/db/db.go:86-95`
- Files: `internal/db/db.go`
- Impact: Tests require `ResetForTesting()` at `internal/db/db.go:170-176`, potential race conditions
- Fix approach: Pass DB instance through function parameters instead of global singleton

**Error comparison using string equality:**
- Issue: `err.Error() == "EOF"` at `internal/link/link.go:282` instead of using `errors.Is`
- Files: `internal/link/link.go`
- Impact: Fragile error handling, breaks if error messages change
- Fix approach: Use `io.EOF` constant or `errors.Is` for error comparison

**Unchecked defer Close() calls:**
- Issue: Many `defer func() { _ = srcFile.Close() }()` patterns ignoring close errors
- Files: `internal/link/link.go:265-271`, `internal/store/store.go:363-369`, `internal/cli/update.go:182-344`, `internal/cli/completion.go:91-181`, `internal/pack/pack.go:423`
- Impact: Resource leaks possible in error paths, silent failures
- Fix approach: Check Close() errors in critical paths, log in non-critical paths

**Context not used for cancellation:**
- Issue: Long-running operations (file watching, network requests) don't accept context.Context
- Files: `internal/watch/watch.go`, `internal/update/update.go`
- Impact: Cannot cancel operations gracefully, difficult to implement timeouts
- Fix approach: Add context.Context parameters to public APIs, propagate through call stack

## Known Bugs

**Flaky symlink test in CI:**
- Symptoms: Test fails intermittently in CI environment
- Files: `internal/store/store_permissions_test.go:382-385`
- Trigger: Running in CI environment (`CI` env var set)
- Workaround: Test skipped when `CI != ""`

**Windows permission test incompatibility:**
- Symptoms: Permission-based tests fail on Windows due to different filesystem behavior
- Files: `internal/store/store_permissions_test.go:14-379`, `internal/db/db_permissions_test.go:14-424`, `internal/link/link_permissions_test.go:15-363`, `internal/gitignore/gitignore_permissions_test.go:13-272`
- Trigger: Running tests on Windows (`runtime.GOOS == "windows"`)
- Workaround: All permission tests skipped on Windows

**Recent race condition fixes:**
- Issue: Multiple race conditions fixed in recent commits (see commit `e5f6793`, `d2df554`)
- Files: `internal/pack/pack.go` (parallel hashing), database access patterns
- Impact: Data corruption possible under concurrent access
- Workaround: Mutexes added, but more testing needed

## Security Considerations

**Database file permissions:**
- Risk: Database created with `0600` permissions at `internal/db/db.go:114`, but store directory created with `0755`
- Files: `internal/db/db.go:106-114`
- Current mitigation: Database file itself is restrictive
- Recommendations: Consider making store directory `0700` to prevent other users listing packages

**No input validation on package names:**
- Risk: Path traversal possible if package names contain `..` or absolute paths
- Files: All CLI commands accepting package names
- Current mitigation: None detected
- Recommendations: Validate package names against allowed character set, reject path separators

**External update mechanism:**
- Risk: `lnpm update` downloads and executes binaries from GitHub releases
- Files: `internal/cli/update.go:182-344`
- Current mitigation: HTTPS for downloads, but no signature verification
- Recommendations: Verify checksums or signatures of downloaded binaries

## Performance Bottlenecks

**Sequential file hashing in large packages:**
- Problem: Files hashed sequentially before parallel worker pool created
- Files: `internal/pack/pack.go:240-279`
- Cause: First pass collects files to hash, second pass hashes in parallel (coordination overhead)
- Improvement path: Stream files directly to worker pool, eliminate two-pass approach

**Database lock contention:**
- Problem: Single BoltDB instance with global mutex (`internal/db/db.go:37`)
- Files: `internal/db/db.go:35-38`
- Cause: All database operations serialize through single lock
- Improvement path: Use read-write lock for read operations, consider SQLite with WAL mode (noted in `go.mod:23`)

**Large file copy buffer:**
- Problem: 1MB buffer allocated per file copy operation
- Files: `internal/link/link.go:273`, similar patterns in `internal/store/store.go`
- Cause: Each goroutine allocates own buffer
- Improvement path: Use sync.Pool for buffer reuse, or rely on OS-level copy optimizations

## Fragile Areas

**Reflink platform-specific implementations:**
- Files: `internal/store/reflink_linux.go`, `internal/store/reflink_darwin.go`, `internal/link/reflink_linux.go`, `internal/link/reflink_darwin.go`
- Why fragile: Filesystem-dependent (APFS, Btrfs, XFS) and only exercised on the matching platform; Linux still issues the FICLONE ioctl through a raw syscall, macOS goes through `golang.org/x/sys/unix`
- Safe modification: Always test on target filesystems, verify fallback to hardlink/copy works
- Test coverage: Limited testing on different filesystems

**File watcher debouncing logic:**
- Files: `internal/watch/watch.go:30-244`
- Why fragile: Complex state machine with multiple mutexes, timer cancellation, channel coordination
- Safe modification: Add comprehensive tests for concurrent event handling, verify no goroutine leaks
- Test coverage: No dedicated watcher tests found

**Database migration/schema changes:**
- Files: `internal/db/db.go:121-140` (bucket initialization)
- Why fragile: No migration system, bucket structure hardcoded
- Safe modification: Never rename/remove buckets without migration path
- Test coverage: Schema changes not tested across versions

**Cross-device link detection:**
- Files: `internal/store/device_unix.go`, `internal/store/device_windows.go`
- Why fragile: Platform-specific device ID extraction, Windows returns `0` always
- Safe modification: Test on network filesystems, verify fallback behavior
- Test coverage: Device boundary tests exist but may not cover all edge cases

## Scaling Limits

**BoltDB file size:**
- Current capacity: Single file database grows unbounded
- Limit: BoltDB performance degrades with large files (multi-GB), mmap size limits
- Scaling path: Implement garbage collection (exists at `internal/cli/gc.go`), or migrate to SQLite (planned in `go.mod:23`)

**File descriptor limits:**
- Current capacity: Watch mode adds all directories recursively
- Limit: OS file descriptor limits (typically 256-1024 per process)
- Scaling path: Use recursive watch with single watcher, prune deep hierarchies

**Concurrent package operations:**
- Current capacity: All operations serialize through database lock
- Limit: Multiple users/processes on same machine will serialize completely
- Scaling path: Implement finer-grained locking, or use database with better concurrency (SQLite WAL mode)

## Dependencies at Risk

**fsnotify maintenance:**
- Risk: Core dependency for watch mode, had previous API changes
- Impact: Watch mode breaks if fsnotify API changes
- Migration plan: Abstract behind interface to allow alternative implementations

**bbolt archival:**
- Risk: BoltDB (go.etcd.io/bbolt) is in maintenance mode, minimal updates
- Impact: No new features, potential security issues may not be fixed
- Migration plan: Already noted in `go.mod:23`, SQLite migration planned

## Missing Critical Features

**Atomic operations:**
- Problem: No transaction support for multi-package operations
- Blocks: Reliable rollback if partial monorepo publish fails
- Priority: Medium - workarounds exist but error recovery is manual

**Checksum verification:**
- Problem: No integrity checking of stored packages
- Blocks: Detecting corruption, verifying reflinks worked correctly
- Priority: High - silent data corruption possible

**Concurrent access coordination:**
- Problem: No file locking on database, no inter-process coordination
- Blocks: Multiple lnpm processes on same machine may corrupt database
- Priority: High - BoltDB needs exclusive access, concurrent opens at `internal/db/db.go:114` timeout after 1s

## Test Coverage Gaps

**Watch mode file system events:**
- What's not tested: Concurrent event handling, debounce timer edge cases, directory creation/deletion during watch
- Files: `internal/watch/watch.go`
- Risk: Race conditions, goroutine leaks, missed events under load
- Priority: High

**Reflink fallback scenarios:**
- What's not tested: Cross-filesystem copies, permission errors during the clone, and the Btrfs/XFS success path (Linux CI runs on ext4, which has no reflink support). Filesystems without reflink support, and the APFS success path, are covered by `internal/fsutil/reflink_test.go`
- Files: `internal/store/reflink_*.go`, `internal/link/reflink_*.go`
- Risk: Silent fallback failures, performance degradation not detected
- Priority: Medium

**Database recovery from corruption:**
- What's not tested: Corrupted database file handling, partial write recovery, bucket missing scenarios
- Files: `internal/db/db.go`
- Risk: Complete data loss if database corrupted, no recovery mechanism
- Priority: High

**Monorepo workspace detection:**
- What's not tested: Circular dependencies, invalid workspace configurations
- Files: `internal/workspace/workspace.go`
- Risk: Silent failures in complex monorepo setups
- Priority: Medium

**Cross-platform symlink handling:**
- What's not tested: Symlink behavior differences between platforms during publish/push
- Files: Multiple across store/link packages
- Risk: Different behavior on Windows vs Unix, symlinks may not be preserved
- Priority: Medium - current test at `tests/publish_test.go:334` skips if symlinks unsupported

---

*Concerns audit: 2026-01-17*
