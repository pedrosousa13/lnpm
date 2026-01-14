# lnpm

> Fast, reliable local npm package development tool — a better alternative to yalc.

[![CI](https://github.com/user/lnpm/actions/workflows/ci.yaml/badge.svg)](https://github.com/user/lnpm/actions/workflows/ci.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/user/lnpm)](https://goreportcard.com/report/github.com/user/lnpm)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## Features

- **Hard links** — Instant syncing without symlink resolution issues
- **Watch mode** — Auto-sync changes to all linked projects
- **Monorepo support** — Publish all workspace packages at once
- **Cross-platform** — Works on Linux, macOS, and Windows
- **All package managers** — npm, yarn, pnpm, and bun
- **Full visibility** — Know exactly what's linked where

## Installation

### Quick Install (Linux/macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/user/lnpm/main/install.sh | sh
```

### Homebrew (macOS/Linux)

```bash
brew install user/tap/lnpm
```

### Go

```bash
go install github.com/user/lnpm/cmd/lnpm@latest
```

### From Source

```bash
git clone https://github.com/user/lnpm.git
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

# Or use watch mode for auto-sync
lnpm watch
```

## Commands

| Command | Description |
|---------|-------------|
| `lnpm publish` | Publish package to local store |
| `lnpm add <pkg>` | Add package from store to project |
| `lnpm remove <pkg>` | Remove linked package |
| `lnpm push` | Push changes to all linked projects |
| `lnpm watch` | Auto-sync on file changes |
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

# Add to a project
lnpm add my-package

# Push updates after making changes
lnpm push

# Or watch for auto-sync
lnpm watch --exec "npm run build"
```

### Monorepo Support

```bash
# Publish all workspace packages
lnpm publish --all

# Works with pnpm-workspace.yaml and package.json workspaces
```

### Before Publishing to npm

```bash
# Remove all lnpm links and restore original dependencies
lnpm retreat --force
npm publish
```

### Shell Completions

```bash
# Bash
lnpm completion bash > /etc/bash_completion.d/lnpm

# Zsh
lnpm completion zsh > "${fpath[1]}/_lnpm"

# Fish
lnpm completion fish > ~/.config/fish/completions/lnpm.fish
```

## Configuration

Configuration file: `~/.lnpm/config.yaml`

```yaml
# Custom store location
store_path: /path/to/store

# Link mode: hardlink or copy
link_mode: hardlink

# Watch debounce in milliseconds
debounce_ms: 100

# Patterns to always ignore
default_ignore:
  - node_modules
  - .git
  - "*.log"

# Hooks
hooks:
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

## How It Works

1. **Publish** — Copies package files to `~/.lnpm/store/{name}/{hash}/`
2. **Add** — Creates hard links from store to `project/.lnpm/{package}/`
3. **Symlink** — Links `node_modules/{package}` → `.lnpm/{package}`
4. **Push/Watch** — Updates store and re-links changed files

```
Source Package          Store                    Project
──────────────          ─────                    ───────
src/index.ts    ──►     (copy)
dist/index.js   ──►     ~/.lnpm/store/      ══► .lnpm/pkg/     ──► node_modules/pkg
package.json    ──►       pkg/abc123/           (hard links)       (symlink)
```

### Why Hard Links?

| Strategy | Pros | Cons |
|----------|------|------|
| Symlinks | Instant | Module resolution breaks |
| Copy | Reliable | Slow, stale data |
| **Hard Links** | Instant, reliable | Cross-filesystem limitation |

lnpm automatically falls back to copy mode when hard links aren't possible.

## Comparison with yalc

| Feature | lnpm | yalc |
|---------|------|------|
| Link method | Hard links | Copy |
| Watch mode | Built-in | Requires chokidar |
| State tracking | bbolt database | Hash files |
| Monorepo | Native support | Manual |
| Speed | ~10ms startup | ~100ms startup |
| Visibility | `lnpm status` | Limited |

## Platform Support

- **Linux** — amd64, arm64
- **macOS** — amd64 (Intel), arm64 (Apple Silicon)
- **Windows** — amd64

## Requirements

- Go 1.22+ (for building from source)
- Node.js project with `package.json`

## Contributing

Contributions are welcome! Please read our contributing guidelines first.

```bash
# Clone and build
git clone https://github.com/user/lnpm.git
cd lnpm
make deps
make build
make test
```

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
