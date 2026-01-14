# lnpm - Local NPM Package Development Tool

> A faster, more reliable alternative to yalc for local package development.

## Design Principles

1. **Speed** - Go for fast startup, hard links for instant syncing
2. **Visibility** - SQLite-backed state, always know what's linked where
3. **Reliability** - Hard links avoid symlink resolution issues
4. **Simplicity** - Minimal commands, sensible defaults
5. **Universal** - Works with npm, yarn, pnpm, and bun

---

## Technical Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                           lnpm CLI (Go)                             │
│         Commands: publish | add | remove | push | watch | status    │
└──────────────────────────────────┬──────────────────────────────────┘
                                   │
        ┌──────────────────────────┼──────────────────────────────────┐
        │                          │                                  │
        ▼                          ▼                                  ▼
┌───────────────┐         ┌───────────────┐                 ┌─────────────────┐
│  File Watcher │         │ Package Store │                 │  Link Manager   │
│  (fsnotify)   │         │ ~/.lnpm/store │                 │  (hard links)   │
└───────────────┘         └───────┬───────┘                 └─────────────────┘
                                  │
                                  ▼
                          ┌───────────────┐
                          │   SQLite DB   │
                          │ ~/.lnpm/lnpm.db│
                          └───────────────┘
```

### Directory Structure

```
~/.lnpm/
├── lnpm.db                      # SQLite database
├── store/                       # Package store
│   └── {package-name}/
│       └── {content-hash}/      # Versioned by content hash
│           ├── package.json
│           ├── dist/
│           └── ...
└── config.toml                  # Global config (optional)

project/
├── .lnpm/                       # Local package cache (hard linked)
│   └── {package-name}/
│       └── ...
├── lnpm.lock                    # Lock file
├── node_modules/
│   └── {package-name} -> ../.lnpm/{package-name}  # Symlink to .lnpm
└── package.json                 # Modified with file: protocol
```

---

## Hard Link Strategy

### Why Hard Links?

| Strategy | Pros | Cons |
|----------|------|------|
| **Symlinks** | Instant, no space | Module resolution breaks, peer deps issues |
| **Copy** | Reliable | Slow, uses disk space, stale on source change |
| **Hard Links** | Instant, no extra space, reliable resolution | Cross-filesystem limitations |

### How It Works

1. `lnpm publish` copies package to `~/.lnpm/store/{name}/{hash}/`
2. `lnpm add` creates hard links from store to `project/.lnpm/{name}/`
3. A symlink connects `node_modules/{name}` → `.lnpm/{name}`
4. `lnpm push` updates the store and re-hard-links changed files only

```
Source Package          Store                    Project
──────────────          ─────                    ───────
src/index.ts    ──►     (copy)
dist/index.js   ──►     ~/.lnpm/store/      ══► .lnpm/pkg/     ──► node_modules/pkg
package.json    ──►       pkg/abc123/           (hard links)       (symlink)
```

### Cross-Filesystem Handling

Hard links only work within the same filesystem. lnpm will:
1. Detect cross-filesystem scenarios
2. Fall back to copy mode with a warning
3. Suggest moving store location via config

---

## SQLite Schema

```sql
-- Package versions stored in the global store
CREATE TABLE packages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    source_path TEXT NOT NULL,
    store_path TEXT NOT NULL,
    files_count INTEGER NOT NULL DEFAULT 0,
    total_size INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(name, content_hash)
);

CREATE INDEX idx_packages_name ON packages(name);
CREATE INDEX idx_packages_hash ON packages(content_hash);

-- Projects that consume packages
CREATE TABLE projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL UNIQUE,
    name TEXT,
    package_manager TEXT CHECK(package_manager IN ('npm', 'yarn', 'pnpm', 'bun')),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_projects_path ON projects(path);

-- Links between packages and projects
CREATE TABLE links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    package_id INTEGER NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    link_type TEXT NOT NULL DEFAULT 'hardlink' CHECK(link_type IN ('hardlink', 'copy')),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(package_id, project_id)
);

CREATE INDEX idx_links_package ON links(package_id);
CREATE INDEX idx_links_project ON links(project_id);

-- Active watch sessions
CREATE TABLE watches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    package_id INTEGER NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    pid INTEGER,
    started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(package_id)
);

-- File manifest for incremental updates
CREATE TABLE files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    package_id INTEGER NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    relative_path TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    size INTEGER NOT NULL,
    mode INTEGER NOT NULL,
    UNIQUE(package_id, relative_path)
);

CREATE INDEX idx_files_package ON files(package_id);
```

---

## CLI Commands

### `lnpm publish`

Publish a package to the local store.

```bash
# Basic usage (in package directory)
lnpm publish

# Publish and push to all linked projects
lnpm publish --push

# Publish with custom tag
lnpm publish --tag beta
```

**Process:**
1. Read `package.json` for name/version
2. Determine files to include (respects `.npmignore`, `files` field)
3. Calculate content hash of all files
4. Copy files to `~/.lnpm/store/{name}/{hash}/`
5. Record in SQLite database
6. If `--push`, update all linked projects

### `lnpm add <package>`

Add a package from the store to current project.

```bash
# Add latest published version
lnpm add my-package

# Add specific version/tag
lnpm add my-package@1.0.0
lnpm add my-package@beta

# Add without modifying package.json
lnpm add my-package --pure

# Add with dev dependency
lnpm add my-package --dev
```

**Process:**
1. Find package in store (latest or specified version)
2. Create `.lnpm/{package}/` directory
3. Hard link all files from store
4. Create symlink `node_modules/{package}` → `.lnpm/{package}`
5. Update `package.json` with `file:.lnpm/{package}`
6. Update `lnpm.lock`
7. Register link in SQLite

### `lnpm remove <package>`

Remove a linked package.

```bash
lnpm remove my-package

# Remove all linked packages
lnpm remove --all
```

**Process:**
1. Remove `.lnpm/{package}/` directory
2. Remove `node_modules/{package}` symlink
3. Restore original `package.json` dependency (if any)
4. Update `lnpm.lock`
5. Remove link from SQLite

### `lnpm push`

Push updates to all linked projects.

```bash
# Push current package to all consumers
lnpm push

# Push with force (re-link all files)
lnpm push --force
```

**Process:**
1. Calculate new content hash
2. If unchanged and not `--force`, skip
3. Update store with new version
4. For each linked project:
   - Remove old hard links
   - Create new hard links
   - Preserve `node_modules` symlink

### `lnpm watch`

Watch for changes and auto-push.

```bash
# Start watching current package
lnpm watch

# Watch with custom ignore patterns
lnpm watch --ignore "**/*.test.ts"

# Watch and run command before push
lnpm watch --exec "npm run build"
```

**Process:**
1. Start fsnotify watcher on package directory
2. On file change:
   - Debounce (default 100ms)
   - If `--exec`, run command and wait
   - Copy changed files to store
   - Update hard links in all projects
3. Register watch in SQLite (for `lnpm status`)

### `lnpm status`

Show current state of all links and watches.

```bash
lnpm status

# Output:
# 📦 Published Packages
# ┌──────────────┬─────────┬──────────┬─────────────────────┐
# │ Package      │ Version │ Hash     │ Published           │
# ├──────────────┼─────────┼──────────┼─────────────────────┤
# │ my-package   │ 1.0.0   │ abc123   │ 2 minutes ago       │
# │ other-pkg    │ 2.1.0   │ def456   │ 1 hour ago          │
# └──────────────┴─────────┴──────────┴─────────────────────┘
#
# 🔗 Active Links
# ┌──────────────┬─────────────────────────┬──────────┐
# │ Package      │ Project                 │ Type     │
# ├──────────────┼─────────────────────────┼──────────┤
# │ my-package   │ ~/code/my-app           │ hardlink │
# │ my-package   │ ~/code/other-app        │ hardlink │
# └──────────────┴─────────────────────────┴──────────┘
#
# 👀 Active Watches
# ┌──────────────┬─────────────────────────┬─────────────────────┐
# │ Package      │ Source                  │ Started             │
# ├──────────────┼─────────────────────────┼─────────────────────┤
# │ my-package   │ ~/code/my-package       │ 5 minutes ago       │
# └──────────────┴─────────────────────────┴─────────────────────┘
```

### `lnpm list`

List packages in a project or the store.

```bash
# List packages linked in current project
lnpm list

# List all packages in store
lnpm list --store

# List all projects using a package
lnpm list my-package --projects
```

### `lnpm gc`

Garbage collect unused packages from store.

```bash
# Dry run - show what would be removed
lnpm gc --dry-run

# Remove packages with no active links
lnpm gc

# Remove packages older than 30 days
lnpm gc --older-than 30d
```

### `lnpm doctor`

Diagnose and fix common issues.

```bash
lnpm doctor

# Output:
# 🔍 Checking lnpm health...
#
# ✓ Store directory exists
# ✓ Database is valid
# ✓ 3 packages in store
# ✓ 2 active links
# ⚠ 1 orphaned link found (project deleted)
#   Run: lnpm gc --fix-links
# ✓ No cross-filesystem issues detected
```

---

## Configuration

### Global Config (`~/.lnpm/config.toml`)

```toml
# Store location (default: ~/.lnpm)
store_path = "~/.lnpm"

# Default link type: "hardlink" | "copy"
link_type = "hardlink"

# Watch debounce in milliseconds
watch_debounce = 100

# Files to always ignore
ignore = [
    "node_modules",
    ".git",
    "*.log"
]
```

### Project Config (`.lnpmrc` or `lnpm` field in `package.json`)

```json
{
  "lnpm": {
    "publishDir": "dist",
    "ignore": ["**/*.test.ts"],
    "prePublish": "npm run build",
    "postAdd": "npm install"
  }
}
```

---

## Lock File Format (`lnpm.lock`)

```yaml
version: 1
packages:
  my-package:
    version: 1.0.0
    hash: abc123def456
    source: ~/code/my-package
    linked: 2024-01-15T10:30:00Z
  other-pkg:
    version: 2.1.0
    hash: 789xyz000111
    source: ~/code/other-pkg
    linked: 2024-01-15T09:00:00Z
```

---

## Package Manager Integration

### Detection

lnpm auto-detects package manager by checking for:
1. `bun.lockb` → bun
2. `pnpm-lock.yaml` → pnpm
3. `yarn.lock` → yarn
4. `package-lock.json` → npm

### package.json Modification

```json
{
  "dependencies": {
    "my-package": "file:.lnpm/my-package"
  }
}
```

The `file:` protocol is universally supported by all package managers.

### Cleanup Before Publish

Before publishing to npm registry, run:
```bash
lnpm retreat  # or: lnpm remove --all
```

This restores original version specifiers in `package.json`.
