# lnpm

> Fast, reliable local npm package development tool — a better alternative to yalc.

[![CI](https://github.com/pedrosousa13/lnpm/actions/workflows/ci.yaml/badge.svg)](https://github.com/pedrosousa13/lnpm/actions/workflows/ci.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/pedrosousa13/lnpm)](https://goreportcard.com/report/github.com/pedrosousa13/lnpm)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## Features

- **Blazing fast** — Reflink/CoW + hard links for instant operations on large packages
- **Smart linking** — Automatically uses the fastest method: reflink → hardlink → parallel copy
- **Monorepo support** — Publish all workspace packages at once
- **Cross-platform** — Works on Linux, macOS, and Windows
- **All package managers** — npm, yarn, pnpm, and bun
- **Full visibility** — Know exactly what's linked where

## Installation

### Quick Install (Linux/macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.sh | sh
```

### Quick Install (Windows)

```powershell
irm https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.ps1 | iex
```

Installs to `%LOCALAPPDATA%\lnpm`. Override with `$env:LNPM_INSTALL_DIR`.

> **Note:** On Windows, lnpm uses NTFS junction points for linking — no Developer Mode or admin privileges required.

### Go

```bash
go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest
```

### From Source

```bash
git clone https://github.com/pedrosousa13/lnpm.git
cd lnpm
make install
```

## Quick Start

```bash
# In your library package
cd ~/code/my-library
lnpm publish

# In your app that uses the library
cd ~/code/my-app
lnpm add my-library

# Make changes to my-library, then push updates
cd ~/code/my-library
lnpm push
```

## Documentation

- **[Monorepo Guide](MONOREPO.md)** — Turborepo, Nx, PNPM/NPM/Yarn workspaces integration
- **[Architecture](ARCHITECTURE.md)** — System design and implementation details
- **[Changelog](CHANGELOG.md)** — Version history and updates
- **[Roadmap](ROADMAP.md)** — Planned features and improvements

## Commands

| Command | Description |
|---------|-------------|
| `lnpm publish` | Publish package to local store |
| `lnpm add <pkg...>` | Add package(s) from store to project |
| `lnpm remove <pkg>` | Remove linked package |
| `lnpm push` | Push changes to all linked projects |
| `lnpm status` | Show current project's links |
| `lnpm list` | List packages in store |
| `lnpm doctor` | Diagnose issues |
| `lnpm gc` | Garbage collect unused packages |
| `lnpm retreat` | Remove all lnpm changes |
| `lnpm config` | View/edit configuration |
| `lnpm completion` | Generate shell completions |

## Usage Examples

### Basic Workflow

```bash
# Publish a package
lnpm publish

# Add to a project (supports multiple packages)
lnpm add my-package
lnpm add pkg-a pkg-b pkg-c

# Push updates after making changes
lnpm push
```

### Monorepo Support

lnpm integrates seamlessly with Turborepo, Nx, and all workspace managers:

```bash
# lnpm is installed globally (one-time system install)
# See installation section above

# Publish all workspace packages
lnpm publish --all

# Push updates to all linked projects
lnpm push
```

**📖 See [MONOREPO.md](MONOREPO.md) for complete guides on:**
- Turborepo integration
- Nx integration
- PNPM/NPM/Yarn workspaces
- Best practices and troubleshooting

### Before Publishing to npm

```bash
# Remove all lnpm links and restore original dependencies
lnpm retreat --force
npm install  # Restore original packages
npm publish
```

### Shell Completions

**Quick setup (recommended):**
```bash
lnpm completion install
```

This auto-detects your shell and installs completions. Follow the on-screen instructions to enable.

**Alternative - Dynamic loading** (add to shell config):
```bash
# Zsh (~/.zshrc)
eval "$(lnpm completion zsh)"

# Bash (~/.bashrc)
eval "$(lnpm completion bash)"

# Fish (~/.config/fish/config.fish)
lnpm completion fish | source
```

**Manual installation:**
```bash
# Zsh
lnpm completion zsh > ~/.zsh/completions/_lnpm

# Bash
lnpm completion bash > /etc/bash_completion.d/lnpm

# Fish
lnpm completion fish > ~/.config/fish/completions/lnpm.fish
```

## Configuration

Configuration file: `~/.lnpm/config.yaml`

```yaml
# Custom store location (default: ~/.lnpm)
store_path: /path/to/store

# Link mode: "hardlink" (try reflink/hardlink) or "copy" (force copy)
# Default: hardlink
link_mode: hardlink

# Auto-manage .gitignore (add/remove .lnpm/ entry)
# Default: true
manage_gitignore: true

# Build scripts and hooks
hooks:
  # Skip prepare scripts (prepare, prepublishOnly, prepack)
  skip_prepare: false      # Default: false (run scripts)

  # Skip post-add hook (npm install after add)
  skip_post_add: false     # Default: false (run npm install)

  # Custom hooks (optional)
  pre_publish: npm run build
  post_add: npm install
```

View/modify config:

```bash
lnpm config                     # Show all
lnpm config store_path          # Get value
lnpm config store_path /data    # Set value
lnpm config --path              # Show config file path
```

## Build Workflows & Hooks

lnpm automatically runs build scripts and dependency installation to ensure packages work correctly when linked.

### Prepare Scripts

Before publishing or pushing, lnpm runs these scripts from your `package.json` (in order of precedence):

1. `prepare` — General build script
2. `prepublishOnly` — Runs only on publish
3. `prepack` — Runs before packing files

**Example package.json:**
```json
{
  "name": "my-library",
  "scripts": {
    "build": "tsc",
    "prepare": "npm run build"
  }
}
```

**Commands:**
```bash
lnpm publish              # Runs prepare, then publishes
lnpm push                 # Runs prepare, then pushes
lnpm publish --skip-hooks # Skip prepare scripts
```

### Post-Add Hook

After adding a package, lnpm automatically runs `npm install` (or your package manager) to resolve peer dependencies and install linked package dependencies.

```bash
lnpm add my-package       # Adds package, then runs npm install
lnpm add --skip-hooks     # Skip post-add npm install
```

### Package Validation

lnpm validates packages before publishing:
- Checks `package.json` has `name` and `version`
- Verifies `main` entry point exists (if declared)

```bash
lnpm publish                  # With validation
lnpm publish --skip-validation # Skip validation (for broken packages)
```

### TypeScript Workflow Example

```bash
# In your TypeScript library
cd my-library
# package.json has "prepare": "tsc"

lnpm publish    # Automatically builds TypeScript → dist/

# In your app
cd my-app
lnpm add my-library   # Links and runs npm install

# Make changes to library
cd my-library
# Edit src/index.ts
lnpm push       # Rebuilds and updates all linked projects
```

### Troubleshooting

**Husky/git hooks fail during npm install:**
- lnpm strips `prepare` and `prepublish` scripts from stored packages (like yalc)
- If you see husky errors, re-publish the package: `lnpm publish`

**Duplicate React or other peer dependency issues:**
- Don't use `--install` flag; run `npm install` manually with your preferred flags
- Or configure npm: `npm config set legacy-peer-deps true`

**npm ERESOLVE peer dependency errors:**
- npm has a bug where `file:` dependencies show as `@undefined` ([npm/cli#2199](https://github.com/npm/cli/issues/2199))
- When using `--install`, lnpm uses `--legacy-peer-deps` automatically
- Or run `npm install --legacy-peer-deps` manually

**Retreat restores wrong version:**
- Fixed in latest version - lnpm now properly tracks original versions
- If stuck, manually edit package.json and delete lnpm.lock

## Debugging

Enable debug mode for verbose logging:

```bash
# Flag
lnpm --debug publish
lnpm -d status

# Environment variable
LNPM_DEBUG=1 lnpm publish
```

Debug output goes to stderr with timestamps, useful for diagnosing slow operations or unexpected behavior.

## How It Works

1. **Publish** — Uses `npm pack` rules to determine files, strips lifecycle scripts (`prepare`/`prepublish`), then links or copies to `~/.lnpm/store/{name}/{hash}/`
2. **Add** — Creates reflinks or hard links from store to `project/.lnpm/{package}/`, updates package.json to `file:.lnpm/{package}`
3. **Symlink** — Links `node_modules/{package}` → `.lnpm/{package}`
4. **Push/Watch** — Updates store and re-links changed files
5. **Auto .gitignore** — Optionally manages `.lnpm/` in `.gitignore` (enabled by default)

**Note:** Unlike some tools, lnpm does NOT run `npm install` automatically (matches yalc). Use `--install` flag or run manually if needed.

```
Source Package          Store                    Project
──────────────          ─────                    ───────
src/index.ts    ──►   (reflink/hardlink)
dist/index.js   ──►     ~/.lnpm/store/      ══► .lnpm/pkg/     ──► node_modules/pkg
package.json    ──►       pkg/abc123/      (reflink/hardlink)     (symlink)
```

### File Filtering

lnpm uses **npm's standard packing rules** (via `npm pack --dry-run`) to determine which files to include, ensuring consistency with actual npm publish behavior. This means:

- Respects `package.json` `files` field
- Honors `.npmignore` or falls back to `.gitignore`
- Follows npm's default exclusions (`.git`, `node_modules`, etc.)
- **Additional safety**: Explicit `.git` filtering prevents any VCS files from being linked
- **Symlinks are skipped, never followed** — a symlink inside a package can't pull files from outside it (e.g. `~/.ssh`) into the store
- Package names with path separators or `..` are rejected to prevent path traversal
- Automatic fallback to custom filtering if npm is unavailable

This approach prevents issues with git hooks (like Husky) running in linked packages and ensures lnpm behaves identically to npm publish.

### Automatic .gitignore Management

lnpm automatically manages `.gitignore` entries to prevent committing linked packages (enabled by default, configurable via `manage_gitignore: false`):

- **On `lnpm add`** — Adds `.lnpm/` to `.gitignore` with marker comment `# Added by lnpm`
- **On `lnpm retreat`** — Removes `.lnpm/` entry and marker from `.gitignore`
- **Smart detection** — Skips if pattern already exists
- **Atomic writes** — Uses temp file + rename to prevent corruption
- **Configurable** — Set `manage_gitignore: false` in config to disable

### Smart Linking Strategy

lnpm uses an intelligent priority system for maximum performance:

**Priority Order:** Reflink → Hard Link → Parallel Copy

| Method | Speed | Platform Support | Disk Usage |
|--------|-------|------------------|------------|
| **Reflink (CoW)** | Instant | macOS APFS, Linux Btrfs/XFS | Zero (copy-on-write) |
| **Hard Link** | Instant | Same filesystem | Zero (shared inodes) |
| **Parallel Copy** | Fast | Always works | Full copy (8 workers) |

**Benefits:**
- ⚡ **10,000+ files?** Instant with reflink or hard links
- 🔄 **Copy-on-write** — Modify files safely without affecting store
- 🚀 **Parallel copying** — 4-8x faster when copying is needed
- 💾 **Space efficient** — No duplication on modern filesystems

lnpm automatically detects the best method and falls back gracefully with helpful warnings.

## Comparison with yalc

| Feature | lnpm | yalc |
|---------|------|------|
| Link method | Reflink → Hard link → Parallel copy | Sequential copy only |
| Large packages (10k+ files) | Instant (~5ms) on APFS/Btrfs | Slow (~5s+) |
| State tracking | bbolt database | Hash files |
| Monorepo | Native support | Manual |
| Speed | ~10ms startup | ~100ms startup |
| Cross-filesystem | Parallel copy (4-8x faster) | Sequential copy |
| Visibility | `lnpm status` | Limited |

## Benchmarks

### lnpm vs yalc (100 files)

| Operation | lnpm | yalc | Speedup |
|-----------|------|------|---------|
| `publish` | **75ms** | 164ms | 2.2x |
| `add` | **71ms** | 251ms | 3.5x |
| `push` | **90ms** | 385ms | 4.3x |

### Detailed benchmarks (Apple M1 Pro, APFS)

| Operation | Files | Time | Memory |
|-----------|-------|------|--------|
| `publish` | 100 | **17ms** | 3.5 MB |
| `add` | 100 | **48ms** | 340 KB |
| `push` (1 project) | 100 | **146ms** | 3.8 MB |
| `push` (5 projects) | 100 | **183ms** | 4.5 MB |
| `publish` | 500 | **41ms** | 17 MB |

Run benchmarks yourself:
```bash
make bench          # Go benchmarks
make bench-mem      # With memory stats
make bench-compare  # Compare vs yalc (if installed)
```

## Platform Support

- **Linux** — amd64, arm64
- **macOS** — amd64 (Intel), arm64 (Apple Silicon)
- **Windows** — amd64

## Requirements

- Go 1.22+ (for building from source)
- Node.js project with `package.json`

## Contributing

Contributions are welcome!

### Prerequisites

- Go 1.22+
- Node.js (for integration tests that invoke npm)

### Setup & Testing

**Linux / macOS:**

```bash
git clone https://github.com/pedrosousa13/lnpm.git
cd lnpm
make deps
make build
make test              # all tests
make test-coverage     # with coverage report
make lint              # golangci-lint (falls back to go vet)
```

**Windows (PowerShell):**

```powershell
git clone https://github.com/pedrosousa13/lnpm.git
cd lnpm
go mod download
go build ./cmd/lnpm
go test -v ./...
go vet ./...
```

### Cross-compile check

Verify your changes compile for all platforms:

```bash
GOOS=linux go build ./...
GOOS=darwin go build ./...
GOOS=windows go build ./...
```

### Running specific test suites

```bash
go test -v -race ./internal/...   # unit tests
go test -v -race ./tests/...      # integration tests
```

> **Windows note:** Some symlink tests are skipped on Windows since lnpm uses NTFS junctions (absolute paths) instead of relative symlinks. This is expected.

## License

MIT License — see [LICENSE](LICENSE) for details.

---

## Disclaimer

> **Note:** This project was built with assistance from Claude (Anthropic's AI assistant).
> The architecture, implementation, and documentation were developed through an AI-assisted
> development process. While the code has been reviewed and tested, users should evaluate
> it according to their own requirements and standards before use in production environments.

---

## Acknowledgments

- Inspired by [yalc](https://github.com/wclr/yalc)
- Built with [Cobra](https://github.com/spf13/cobra) for CLI
- Uses [bbolt](https://github.com/etcd-io/bbolt) for state storage
- File watching via [fsnotify](https://github.com/fsnotify/fsnotify)
