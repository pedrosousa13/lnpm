TITLE: Fix macOS reflink: the clonefile syscall is invoked with the wrong signature and never succeeds
LABELS: bug
---
## Severity

High — on macOS the intended fast, isolated copy path (reflink) never runs, so every store/link operation silently falls through to hardlinking, which is what enables store corruption elsewhere.

## Background

lnpm is a Go CLI that maintains a content-addressed store of npm packages at `~/.lnpm/store` for local package development. When it populates the store or links a package into a consumer project, it tries three strategies in order: (1) a **reflink** — a copy-on-write clone that is fast like a hardlink but logically independent, so writing to one copy leaves the other untouched (supported on APFS, macOS's filesystem); (2) a **hardlink** — a second name for the exact same underlying file, where editing one path edits the other; (3) a plain byte copy. Reflink is the best option: it is instant and safe. The reflink implementation is platform-specific; on macOS it lives in `internal/fsutil/reflink_darwin.go` and is supposed to call the kernel's `clonefile` facility.

## Problem

The darwin code defines `SYS_CLONEFILE = 462` and invokes it with three arguments via `syscall.Syscall`: a source path pointer, a destination path pointer, and flags. But syscall number 462 on XNU (the macOS kernel) is **`clonefileat`**, which takes **five** arguments: `(src_dirfd, src_path, dst_dirfd, dst_path, flags)`. Because the code passes the source *path pointer* where the kernel expects `src_dirfd` (a directory file descriptor), the kernel interprets a pointer value as a file descriptor and the call always fails with `EINVAL` (errno 22).

The failure is swallowed: `tryReflink` returns `false`, `Reflink` returns a generic "not supported" error, and both `store.Store` and `link.Link` treat that as "reflink unavailable" and fall through to the next strategy. So on APFS — the default macOS filesystem — reflinks *never* work. Every publish and every link silently uses hardlinks instead.

Concrete failure scenario: a developer on a stock Mac runs `lnpm publish`. They expect (per the README and debug logs) reflinked, isolated store entries. Instead every file is hardlinked from their source tree into the store, sharing inodes with their working copy. The debug log line "reflinked N" always reports 0. This is empirically confirmed: calling syscall 462 in the current 3-argument form returns errno 22, while calling it in the correct 5-argument `clonefileat` form succeeds.

## Where to look

- `internal/fsutil/reflink_darwin.go:14` — `const SYS_CLONEFILE = 462` (this number is `clonefileat`, not the 3-arg `clonefile`).
- `internal/fsutil/reflink_darwin.go:35-40` — the `syscall.Syscall(uintptr(SYS_CLONEFILE), srcPtr, dstPtr, 0)` call with only three arguments.
- `internal/fsutil/reflink_darwin.go:42-45` — the swallowed `errno` that hides the failure.
- `internal/store/store.go:150-153` — caller: `if fsutil.Reflink(f.Path, destFile) == nil { ... }`, falls through on failure.
- `internal/link/link.go:102-105` — caller: same pattern for project linking.
- `internal/fsutil/reflink_linux.go` — the Linux sibling, for how the platform build tags and the `tryReflink`/`Reflink` pair are structured.

## How to fix

1. Replace the raw `syscall.Syscall` call with the correct clonefile invocation. Preferred: use `golang.org/x/sys/unix.Clonefile(src, dst, flags)` — it wraps the real `clonefile(2)` (two-path form) and avoids the deprecated `syscall.Syscall` on darwin. The module already depends on `golang.org/x/sys` (currently `// indirect` in `go.mod`); promote it to a direct dependency and run `go mod tidy`.
2. If for some reason you must keep a raw syscall, use `syscall.Syscall6` against `clonefileat` (462) passing `unix.AT_FDCWD` for *both* the source and destination dirfd arguments: `(AT_FDCWD, srcPtr, AT_FDCWD, dstPtr, flags, 0)`. Do not pass path pointers in the dirfd slots.
3. Keep the existing behavior on failure: return `false` from `tryReflink` so the caller falls back to hardlink/copy. Keep the `debug.Logf` on the errno so genuinely-unsupported filesystems (e.g. a non-APFS volume) stay diagnosable.
4. Leave `reflink_linux.go` and `reflink_other.go` untouched — this is a darwin-only fix. Match the existing file's style (build tag, `tryReflink` returning `bool`, `Reflink` wrapping it).

## Acceptance criteria

- [ ] On an APFS volume, `fsutil.Reflink(src, dst)` returns `nil` and produces an independent copy of `src` at `dst` (writing to `dst` does not change `src`, and vice versa).
- [ ] `lnpm publish` / `lnpm add` on macOS report a non-zero reflink count in debug output (`LNPM_DEBUG=1`) instead of always 0.
- [ ] `tryReflink` still returns `false` (not a panic or error propagation) on filesystems that do not support cloning, so the copy fallback still works.
- [ ] `go.mod` lists `golang.org/x/sys` as a direct dependency if the `unix.Clonefile` approach is taken; `go build ./...` succeeds on darwin.

## Testing

From the repo root:

```
go build ./...
go test ./internal/fsutil/...
go test ./internal/store/... ./internal/link/...
go test ./...
```

Add a new test file `internal/fsutil/reflink_test.go` (there is currently no test for this package). Gate it with `if runtime.GOOS != "darwin" { t.Skip(...) }` for the darwin-specific assertions (mirror the `runtime.GOOS` skips used in `internal/store/store_permissions_test.go`). The test should: write a source file into `t.TempDir()`, call `Reflink` to clone it, assert the call succeeds and the destination content matches, then overwrite the destination and assert the source is unchanged (proving CoW isolation, not a hardlink). Because `t.TempDir()` on macOS is on APFS, the reflink path will actually exercise.
