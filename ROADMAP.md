# lnpm Implementation Roadmap

## Go Dependencies

```go
// Core
github.com/spf13/cobra       // CLI framework
github.com/spf13/viper       // Configuration
modernc.org/sqlite           // Pure Go SQLite (no CGO)

// File operations
github.com/bmatcuk/doublestar // Glob patterns

// UX
github.com/charmbracelet/lipgloss   // Styling
github.com/charmbracelet/bubbles    // Spinners, tables
github.com/fatih/color              // Terminal colors

// Utilities
github.com/cespare/xxhash/v2        // Fast hashing
github.com/pelletier/go-toml/v2     // TOML config
gopkg.in/yaml.v3                    // YAML lock file
```

---

## Phase 1: Core Foundation

**Goal:** Basic publish/add/remove cycle working end-to-end.

### 1.1 Project Setup
- [ ] Initialize Go module (`go mod init github.com/pedrosousa13/lnpm`)
- [ ] Set up directory structure:
  ```
  lnpm/
  ├── cmd/
  │   └── lnpm/
  │       └── main.go
  ├── internal/
  │   ├── cli/           # Cobra commands
  │   ├── store/         # Package store operations
  │   ├── db/            # SQLite operations
  │   ├── link/          # Hard link manager
  │   ├── pack/          # Package file resolution
  │   └── config/        # Configuration
  ├── pkg/
  │   └── lockfile/      # Lock file parsing
  ├── go.mod
  └── go.sum
  ```
- [ ] Configure Cobra with root command
- [ ] Add version flag (`lnpm --version`)

### 1.2 SQLite Database
- [ ] Database initialization with migrations
- [ ] Connection pool management
- [ ] Schema creation (packages, projects, links, files tables)
- [ ] Basic CRUD operations for each table
- [ ] Database location: `~/.lnpm/lnpm.db`

### 1.3 Package Store
- [ ] Store directory structure (`~/.lnpm/store/{name}/{hash}/`)
- [ ] Content hash calculation (xxhash of all file contents)
- [ ] File manifest generation
- [ ] Copy files to store
- [ ] Retrieve package from store by name/hash

### 1.4 Pack Logic (npm pack equivalent)
- [ ] Parse `package.json`
- [ ] Read `files` field (whitelist)
- [ ] Read `.npmignore` (blacklist)
- [ ] Read `.gitignore` as fallback
- [ ] Default includes: `package.json`, `README*`, `LICENSE*`, `CHANGELOG*`
- [ ] Default excludes: `node_modules`, `.git`, `*.log`, etc.
- [ ] Return list of files to publish

### 1.5 `lnpm publish` Command
- [ ] Validate current directory has `package.json`
- [ ] Extract name and version
- [ ] Calculate files to include
- [ ] Generate content hash
- [ ] Check if already published (skip if same hash)
- [ ] Copy files to store
- [ ] Record in database
- [ ] Print success message with hash

### 1.6 `lnpm add` Command
- [ ] Parse package argument (name, optional @version)
- [ ] Look up package in store
- [ ] Create `.lnpm/{package}/` directory
- [ ] Hard link files from store
- [ ] Create symlink `node_modules/{package}` → `.lnpm/{package}`
- [ ] Detect package manager
- [ ] Update `package.json` with `file:` dependency
- [ ] Create/update `lnpm.lock`
- [ ] Record link in database

### 1.7 `lnpm remove` Command
- [ ] Parse package argument
- [ ] Remove `.lnpm/{package}/` directory
- [ ] Remove `node_modules/{package}` symlink
- [ ] Restore original dependency in `package.json` (from lock)
- [ ] Update `lnpm.lock`
- [ ] Remove link from database

### 1.8 Lock File
- [ ] Define YAML schema
- [ ] Parse existing lock file
- [ ] Write lock file
- [ ] Store original dependency versions for restore

**Deliverable:** Can publish a package and add it to another project with hard links.

---

## Phase 2: Push & Sync

**Goal:** Update linked packages without re-adding.

### 2.1 `lnpm push` Command
- [ ] Calculate new content hash
- [ ] Compare with stored hash
- [ ] If different:
  - [ ] Update store with new files
  - [ ] For each linked project:
    - [ ] Remove old hard links
    - [ ] Create new hard links
  - [ ] Update database
- [ ] If same: print "No changes"

### 2.2 Incremental Updates
- [ ] Store file-level hashes in `files` table
- [ ] On push, compare file hashes
- [ ] Only re-link changed files
- [ ] Track added/removed files

### 2.3 `lnpm publish --push`
- [ ] Combine publish and push in one command
- [ ] Publish to store
- [ ] Push to all linked projects

### 2.4 Cross-Filesystem Detection
- [ ] Detect if store and project are on different filesystems
- [ ] Fall back to copy mode with warning
- [ ] Suggest config change for store location

**Deliverable:** Can update linked packages with single command.

---

## Phase 3: Status & Visibility

**Goal:** Full visibility into lnpm state.

### 3.1 `lnpm status` Command
- [ ] Query all published packages
- [ ] Query all active links
- [ ] Format as tables with lipgloss
- [ ] Show relative times ("2 minutes ago")

### 3.2 `lnpm list` Command
- [ ] `lnpm list` - packages in current project
- [ ] `lnpm list --store` - all packages in store
- [ ] `lnpm list <pkg> --projects` - projects using package

### 3.3 `lnpm doctor` Command
- [ ] Check store directory exists
- [ ] Validate database integrity
- [ ] Find orphaned links (project deleted)
- [ ] Find orphaned packages (no links)
- [ ] Detect cross-filesystem issues
- [ ] Suggest fixes for each issue

### 3.4 `lnpm gc` Command
- [ ] `--dry-run` - show what would be removed
- [ ] Remove packages with no links
- [ ] `--older-than` - remove old packages
- [ ] `--fix-links` - clean orphaned link records

**Deliverable:** Full visibility and maintenance tools.

---

## Phase 4: DX Polish

**Goal:** Delightful developer experience.

### 4.1 Rich Output
- [ ] Colored success/error messages
- [ ] Spinners for long operations
- [ ] Progress bars for multi-file copies
- [ ] Emoji indicators (optional, detect terminal support)

### 4.2 Error Messages
- [ ] Actionable error messages with suggestions
- [ ] "Did you mean?" for typos
- [ ] Link to docs for common issues

### 4.3 Shell Completions
- [ ] Bash completions
- [ ] Zsh completions
- [ ] Fish completions
- [ ] Auto-install completions

### 4.4 Configuration
- [ ] Global config (`~/.lnpm/config.toml`)
- [ ] Project config (`.lnpmrc`)
- [ ] Environment variable overrides
- [ ] `lnpm config` command to view/edit

### 4.5 Hooks
- [ ] `prePublish` - run before publish
- [ ] `postPublish` - run after publish
- [ ] `postAdd` - run after add (e.g., `npm install`)
- [ ] Configure in project or package

**Deliverable:** Polished, professional CLI tool.

---

## Phase 5: Advanced Features

**Goal:** Power user features and monorepo support.

### 5.1 Monorepo Support
- [ ] Detect workspace root (pnpm-workspace.yaml, etc.)
- [ ] `lnpm publish --all` - publish all workspace packages
- [ ] Resolve `workspace:*` protocol
- [ ] Handle inter-package dependencies

### 5.2 Tags
- [ ] `lnpm publish --tag beta`
- [ ] `lnpm add pkg@beta`
- [ ] Store tags in database
- [ ] `lnpm tag` command to manage tags

### 5.3 History
- [ ] Keep multiple versions in store
- [ ] `lnpm list pkg --versions`
- [ ] `lnpm add pkg@abc123` (by hash)
- [ ] `lnpm rollback pkg` - revert to previous version

### 5.4 Retreat Command
- [ ] `lnpm retreat` - remove all lnpm changes
- [ ] Restore original package.json
- [ ] Clean .lnpm directory
- [ ] Useful before `npm publish`

**Deliverable:** Full-featured local package management.

---

## Phase 6: Distribution

**Goal:** Easy installation everywhere.

### 6.1 Build Pipeline
- [ ] GitHub Actions workflow
- [ ] Build for linux/amd64, linux/arm64
- [ ] Build for darwin/amd64, darwin/arm64
- [ ] Build for windows/amd64

### 6.2 Release Automation
- [ ] goreleaser configuration
- [ ] Semantic versioning
- [ ] Changelog generation
- [ ] GitHub releases with binaries

### 6.3 Package Managers
- [ ] Homebrew formula (macOS/Linux)
- [ ] Scoop manifest (Windows)
- [ ] AUR package (Arch Linux)
- [ ] npm wrapper (`npx lnpm`)

### 6.4 Installation Script
- [ ] `curl -fsSL https://lnpm.dev/install.sh | sh`
- [ ] Detect OS and architecture
- [ ] Download and install binary
- [ ] Add to PATH

**Deliverable:** One-command installation on any platform.

---

## Success Metrics

| Metric | Target |
|--------|--------|
| Startup time | < 10ms |
| `publish` (100 files) | < 500ms |
| `add` (hard link) | < 100ms |
| `push` (1 changed file) | < 50ms |
| Binary size | < 15MB |

---

## Testing Strategy

### Unit Tests
- Pack logic (file inclusion/exclusion)
- Hash calculation
- Lock file parsing
- Database operations

### Integration Tests
- Full publish → add → push cycle
- Cross-project linking
- Package manager detection

### E2E Tests
- Real npm/pnpm/yarn/bun projects
- Monorepo scenarios
- Cross-filesystem scenarios (Docker)

---

## Timeline Estimate

| Phase | Complexity |
|-------|------------|
| Phase 1: Core | High |
| Phase 2: Push | Medium |
| Phase 3: Status | Low |
| Phase 4: DX | Medium |
| Phase 5: Advanced | High |
| Phase 6: Distribution | Medium |
