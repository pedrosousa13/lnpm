# External Integrations

**Analysis Date:** 2026-01-17

## APIs & External Services

**Package Managers (External Commands):**
- npm - Used to run scripts (`npm run <script>`) and installs, never to select files
  - Purpose: Lifecycle hooks on publish/push, and `npm install` on `add --install`
  - Location: `internal/hooks/hooks.go`, `internal/config/config.go`
- File selection - Pure Go, modeled on npm's rules but not identical to them
  - Purpose: Decide which files land in the store
  - Location: `internal/pack/pack.go`
  - Differences from `npm publish`: see the README's "File Filtering" section

**Version Control:**
- git - Optional integration for git-aware file filtering
  - Purpose: Respect `.gitignore` patterns when filtering files
  - Location: `internal/pack/git_filter_test.go`

**No external APIs:** This is a local-first CLI tool with no network dependencies for core operations.

## Data Storage

**Databases:**
- bbolt (embedded key-value store)
  - Connection: Local file at `~/.lnpm/lnpm.db`
  - Client: `go.etcd.io/bbolt` v1.5.0
  - Purpose: Track packages, projects, links, file metadata
  - Location: `internal/db/db.go`

**File Storage:**
- Local filesystem only
  - Store path: `~/.lnpm/store/{package-name}/{content-hash}/`
  - Configuration: `LNPM_STORE` env var or `store_path` in config

**Caching:**
- None - Content-addressable storage via xxhash provides deduplication
- File metadata cached in bbolt database

## Authentication & Identity

**Auth Provider:**
- None - Runs entirely in local user context
  - Uses OS user home directory (`os.UserHomeDir()`)
  - File permissions based on OS user

## Monitoring & Observability

**Error Tracking:**
- None - CLI tool, errors written to stderr

**Logs:**
- Debug logging via `internal/debug/debug.go`
  - Enabled via `LNPM_DEBUG=1` or `--debug` flag
  - Output: stderr with timestamps
  - Purpose: Diagnose slow operations and unexpected behavior

## CI/CD & Deployment

**Hosting:**
- GitHub (source code and releases)
  - Repository: `github.com/pedrosousa13/lnpm`

**CI Pipeline:**
- GitHub Actions (`.github/workflows/ci.yaml`)
  - Jobs: test (unit + integration), lint (golangci-lint), build (cross-platform)
  - Coverage: Uploaded to codecov
  - Artifacts: Multi-platform binaries uploaded on build

**Release:**
- GitHub Actions (`release-please.yaml`)
- Manual: `make release` generates binaries for all platforms

**Distribution:**
- `go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest`
- Shell installer: `curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.sh | sh`
- GitHub releases with pre-built binaries

## Environment Configuration

**Required env vars:**
- None (all have defaults)

**Optional env vars:**
- `LNPM_STORE` - Override store location (default: `~/.lnpm`)
- `LNPM_CONFIG` - Override config file path (default: `~/.lnpm/config.yaml`)
- `LNPM_DEBUG=1` - Enable debug logging
- `LNPM_INSTALL_DIR` - Install directory for shell installer (default: `$HOME/.local/bin`)

**Secrets location:**
- None - No secrets or API keys required

## Webhooks & Callbacks

**Incoming:**
- None

**Outgoing:**
- None

## Package Manager Detection

**Supported Package Managers:**
- npm - Detected via `package-lock.json`
- yarn - Detected via `yarn.lock`
- pnpm - Detected via `pnpm-lock.yaml`
- bun - Detected via `bun.lockb`

**Detection Logic:**
- Location: `internal/config/config.go` - `DetectPackageManager()`
- Checked in order: bun → pnpm → yarn → npm (default fallback)

## Configuration Hooks

**User-defined Hooks:**
- `pre_publish` - Run before package publish (e.g., `npm run build`)
- `post_publish` - Run after package publish
- `post_add` - Run after adding package (e.g., `npm install`)

**Hook Configuration:**
- Location: `~/.lnpm/config.yaml` under `hooks` section
- Executed via shell (e.g., via `os/exec`)

## File System Integration

**Special File Operations:**
- Reflink (Copy-on-Write) on macOS APFS and Linux Btrfs/XFS
  - Platform-specific syscalls in `internal/store/reflink_*.go` and `internal/link/reflink_*.go`
  - Detection: `internal/store/device_*.go` and `internal/link/device_*.go`
- Hard links (cross-platform fallback)
- Parallel copy (final fallback with goroutine pool)

**Workspace Detection:**
- npm workspaces - `workspaces` field in `package.json`
- yarn workspaces - `workspaces` field in `package.json`
- pnpm workspaces - `pnpm-workspace.yaml`
- Turborepo - Detected via `turbo.json`
- Nx - Detected via `nx.json`

---

*Integration audit: 2026-01-17*
