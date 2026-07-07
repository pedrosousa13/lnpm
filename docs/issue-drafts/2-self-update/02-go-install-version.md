TITLE: Report a real version for go install builds so they can self-update
LABELS: bug
---
## Severity

High — every user who installs with `go install` gets a binary that reports its version as "dev", cannot self-update, and receives no update notices. This is a supported install path advertised in the docs.

## Background

The build version is stamped into the binary at compile time using "ldflags" — linker flags that overwrite the value of a Go variable. The release build (via goreleaser) passes `-X main.Version=<tag>`, so release binaries know their own version. But `go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest` does *not* pass those flags, so the `Version` variable keeps its source default of `"dev"`. Go does, however, record the module version a binary was built from in its embedded build metadata, which is readable at runtime via `runtime/debug.ReadBuildInfo()`.

## Problem

Because `go install` builds report `Version == "dev"`, three things break for those users:

1. `lnpm --version` prints `lnpm version dev` instead of the actual version.
2. `lnpm update` refuses to run: it returns the error "update not supported for dev builds. Install from source: go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest" — which is circular advice, because the user already installed via `go install` and is being told to do exactly that again.
3. The background update check is skipped entirely for `"dev"`, so these users never even see the "Update available" notice.

Ironically, the updater already has a working code path for `go install` users (`installLatestViaGo`, which runs `go install ...@latest`), but it is unreachable because the "dev" guard rejects the build before that path is ever considered.

Concrete failure: user runs `go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest`, then `lnpm update`. Expected: it re-runs `go install` to fetch the newest version. Actual: error telling them to run `go install`, exit non-zero.

## Where to look

- `cmd/lnpm/main.go:11` — `Version = "dev"`, the default that `go install` never overwrites. Also `Commit` and `Date` on lines 12-13.
- `cmd/lnpm/main.go:16-20` — `main()` calls `cli.SetVersion(Version)` with the raw value.
- `internal/cli/update.go:51-53` — the `if currentVersion == "dev" || currentVersion == "" { return fmt.Errorf(...) }` guard with the circular advice.
- `internal/cli/update.go:84-86` — `wasInstalledViaGo()` / `installLatestViaGo()` — the path that would work for these users but is never reached.
- `internal/update/update.go:78-82` — `CheckAsync` skips the background check when version is `"dev"` or empty.

## How to fix

1. In `cmd/lnpm/main.go`, before calling `cli.SetVersion`, resolve the effective version: if the ldflags value is still the `"dev"` default, fall back to the embedded build metadata.
   ```go
   import "runtime/debug"

   func resolveVersion() string {
       if Version != "dev" {
           return Version // release build, stamped via ldflags
       }
       if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
           return info.Main.Version // e.g. "v1.11.0" from `go install ...@latest`
       }
       return "dev" // built locally from a checkout with no module version
   }
   ```
   Call `cli.SetVersion(resolveVersion())`.
2. `info.Main.Version` is typically already `v`-prefixed (e.g. `v1.11.0`), which the existing normalization in `compareVersions` handles. Do not strip or add prefixes in `main.go`.
3. Leave the `"dev"` guards in `internal/cli/update.go` and `internal/update/update.go` as-is: a bare `go build` from a local checkout still has no module version and correctly stays `"dev"`. This fix only rescues the `@version` install path.
4. Verify the go-install detection still routes correctly: with a real version, `RunUpdate` proceeds past the guard, `wasInstalledViaGo()` returns true for a `~/go/bin` binary, and `installLatestViaGo()` runs. (Note: the boundary check in `wasInstalledViaGo` is hardened separately — see the cleanup issue.)

## Acceptance criteria

- [ ] A binary built with `go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest` prints a real version (e.g. `lnpm version v1.11.0`) from `lnpm --version`.
- [ ] A release binary (built with ldflags) still prints the ldflags version, unchanged.
- [ ] A local `go build` from a checkout with no module version still reports `dev`.
- [ ] For a go-install binary, `lnpm update` no longer errors with the "not supported for dev builds" message and instead runs the `go install` update path.
- [ ] Background update notices appear for go-install binaries.

## Testing

From the repository root:

```
go build ./...
go vet ./...
```

Manual verification (the build-info fallback cannot be exercised by `go build` in a checkout, which is why it must be tested via install):

```
go install ./cmd/lnpm
"$(go env GOPATH)/bin/lnpm" --version   # should NOT print "dev"
```

Add a unit test for `resolveVersion` in `cmd/lnpm/` (e.g. `main_test.go`) covering the case where `Version != "dev"` returns it unchanged. The `ReadBuildInfo` branch is environment-dependent and is best left to the manual install check above.
