# Technology Stack

**Analysis Date:** 2026-01-17

## Languages

**Primary:**
- Go 1.22 - All application code, CLI, internal packages

**Secondary:**
- Shell script - Installation script (`install.sh`)
- YAML - Configuration files (`.github/workflows/*.yaml`, `config.yaml`)

## Runtime

**Environment:**
- Go 1.22+

**Package Manager:**
- Go modules (go.mod/go.sum)
- Lockfile: Present (`go.sum`)

## Frameworks

**Core:**
- `github.com/spf13/cobra` v1.8.1 - CLI framework for command structure

**Testing:**
- Standard Go testing (`testing` package) - All test files use built-in Go testing

**Build/Dev:**
- Make - Build automation (`Makefile`)
- golangci-lint (optional) - Linting with fallback to `go vet`
- goimports (optional) - Import formatting

## Key Dependencies

**Critical:**
- `go.etcd.io/bbolt` v1.3.8 - Embedded key-value database for state tracking
  - Stores packages, projects, links, files metadata
  - Database location: `~/.lnpm/lnpm.db`

**File Operations:**
- `github.com/fsnotify/fsnotify` v1.7.0 - Cross-platform file system notifications for watch mode
- `github.com/bmatcuk/doublestar/v4` v4.6.1 - Glob pattern matching for file filtering

**Performance:**
- `github.com/cespare/xxhash/v2` v2.3.0 - Fast hashing for content-based deduplication
- `github.com/panjf2000/ants/v2` v2.11.4 - Goroutine pool for parallel file operations

**Infrastructure:**
- `gopkg.in/yaml.v3` v3.0.1 - YAML parsing for configuration
- `golang.org/x/sync` v0.11.0 - Extended synchronization primitives (indirect)
- `golang.org/x/sys` v0.4.0 - Low-level system calls for reflink/device detection (indirect)

## Configuration

**Environment:**
- `LNPM_STORE` - Custom store location (default: `~/.lnpm`)
- `LNPM_CONFIG` - Custom config file path (default: `~/.lnpm/config.yaml`)
- `LNPM_DEBUG=1` - Enable debug logging to stderr

**Build:**
- `go.mod` - Go module dependencies
- `Makefile` - Build targets, testing, cross-platform compilation
- `.github/workflows/ci.yaml` - CI pipeline configuration

**User Config:**
- `~/.lnpm/config.yaml` - User configuration (YAML format)
  - `store_path`, `link_mode`, `debounce_ms`, `manage_gitignore`, `default_ignore`, `hooks`

## Platform Requirements

**Development:**
- Go 1.22+ required
- Optional: golangci-lint, goimports, entr (for watch mode)
- Git hooks support via `.githooks/`

**Production:**
- Cross-platform binaries: Linux (amd64, arm64), macOS (Intel, Apple Silicon), Windows (amd64)
- Distributed via: GitHub releases, `go install`, shell installer (`install.sh`)
- No runtime dependencies beyond the binary

## Build System

**Build Commands:**
- `make build` - Build single binary for current platform
- `make release` - Cross-compile for all platforms (Linux, macOS, Windows)
- `make test` - Run all tests
- `make test-coverage` - Generate HTML coverage report
- `make lint` - Run linter (golangci-lint or go vet)
- `make install` - Install to `$GOPATH/bin`

**Versioning:**
- Version injected at build time via ldflags: `-ldflags "-X main.Version=$(VERSION)"`
- Version extracted from git tags: `git describe --tags --always --dirty`

---

*Stack analysis: 2026-01-17*
