# lnpm + Monorepos

Complete guide for using lnpm with Turborepo, Nx, and other monorepo tools.

> **⚠️ Important:** lnpm is a **standalone Go binary**, not an npm package. Install it globally on your system once, then use it in any project. Do NOT add it to package.json dependencies.

## Installation (One-Time, System-Wide)

```bash
# macOS/Linux
curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.sh | sh

# Or with Go
go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest

# Verify installation
lnpm --version
```

After installation, the `lnpm` command is available system-wide in any directory.

## Table of Contents

- [Overview](#overview)
- [Turborepo](#turborepo)
- [Nx](#nx)
- [PNPM Workspaces](#pnpm-workspaces)
- [NPM Workspaces](#npm-workspaces)
- [Yarn Workspaces](#yarn-workspaces)
- [Best Practices](#best-practices)
- [Troubleshooting](#troubleshooting)

## Overview

lnpm integrates seamlessly with monorepo tools by:
- Installing **once on your system** (not per-project)
- Working with your existing package manager (npm, pnpm, yarn, bun)
- Respecting your build/task orchestration tool (Turborepo, Nx)
- Enabling fast iteration without interfering with your monorepo's dependency management

### Key Concept

lnpm is a **system-wide binary** that can link packages from any workspace to any project (inside or outside the monorepo).

```
my-monorepo/                    external-app/
├── package.json                ├── package.json
├── node_modules/               └── node_modules/
│   └── lnpm ← installed here       └── @my/ui ← linked from monorepo
├── packages/
│   └── ui/
│       ├── package.json
│       └── src/
└── apps/
    └── web/
        └── package.json
```

---

## Turborepo

Turborepo handles task orchestration and caching. lnpm handles local package iteration.

### Setup

**1. Install lnpm globally (one-time, system-wide):**

```bash
# macOS/Linux
curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.sh | sh

# Or with Go
go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest
```

**2. Add convenience scripts to root `package.json` (optional):**

```json
{
  "scripts": {
    "dev": "turbo run dev",
    "build": "turbo run build",
    "lnpm:publish": "lnpm publish --all",
    "lnpm:push": "lnpm push"
  },
  "devDependencies": {
    "turbo": "latest"
  }
}
```

### Workflow: Publishing Monorepo Packages

**Publish all workspace packages:**

```bash
# From monorepo root
lnpm publish --all
# or via npm script
npm run lnpm:publish
```

This publishes all packages defined in your workspace to the local lnpm store.

### Workflow: Linking to External Project

```bash
# In external project (outside monorepo)
cd ~/projects/external-app
lnpm add @my/ui
lnpm add @my/components

# Back in monorepo - build and push changes
cd ~/projects/my-monorepo/packages/ui
turbo run build --filter=@my/ui
lnpm push
```

**What happens:**
1. Turborepo builds only affected packages (using cache when possible)
2. lnpm pushes built files to linked external projects
3. External app receives the updated package

### Workflow: Linking Between Workspaces

Even though Turborepo/workspace protocol handles internal dependencies, you can use lnpm for rapid iteration:

```bash
# Publish workspace packages to lnpm store
npm run lnpm:publish

# Link specific package to another workspace
cd apps/web
lnpm add @my/ui

# Build and push changes
turbo run build --filter=@my/ui
lnpm push
```

### Integration with turbo.json

```json
{
  "$schema": "https://turbo.build/schema.json",
  "pipeline": {
    "build": {
      "dependsOn": ["^build"],
      "outputs": ["dist/**", ".next/**"]
    },
    "dev": {
      "cache": false,
      "persistent": true
    },
    "lnpm:push": {
      "dependsOn": ["build"],
      "cache": false
    }
  }
}
```

Add a custom task:

```json
{
  "scripts": {
    "lnpm:push": "lnpm push"
  }
}
```

Then run: `turbo run lnpm:push --filter=@my/ui`

---

## Nx

Nx handles task orchestration, caching, and dependency graph. lnpm complements it for external linking.

### Setup

**1. Install lnpm globally (one-time, system-wide):**

```bash
# macOS/Linux
curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.sh | sh

# Or with Go
go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest
```

**2. Add convenience scripts to root `package.json` (optional):**

```json
{
  "scripts": {
    "lnpm:publish": "lnpm publish --all",
    "lnpm:push": "lnpm push"
  }
}
```

### Workflow: Publishing Libraries

```bash
# From nx workspace root
lnpm publish --all
# or via npm script
npm run lnpm:publish
```

This publishes all buildable libraries in your Nx workspace to lnpm store.

### Workflow: Linking to External Project

```bash
# In external project
cd ~/projects/external-app
lnpm add @my-org/feature-auth
lnpm add @my-org/ui

# Back in Nx workspace - build and push
cd ~/projects/my-nx-workspace
nx build feature-auth
cd libs/feature-auth
lnpm push
```

**Nx + lnpm workflow:**
1. Nx builds affected libs (with computation caching)
2. lnpm pushes to external project
3. External app receives changes

### Nx Project Configuration

Add a custom target in `project.json` for your library:

```json
{
  "name": "feature-auth",
  "targets": {
    "build": {
      "executor": "@nx/js:tsc",
      "outputs": ["{options.outputPath}"],
      "options": {
        "outputPath": "dist/libs/feature-auth"
      }
    },
    "lnpm-push": {
      "executor": "nx:run-commands",
      "options": {
        "command": "lnpm push",
        "cwd": "libs/feature-auth"
      },
      "dependsOn": ["build"]
    }
  }
}
```

Then run: `nx run feature-auth:lnpm-push`

### Nx Task Pipeline

Use Nx task pipeline to auto-push after build:

```json
{
  "targetDefaults": {
    "lnpm-push": {
      "dependsOn": ["build"]
    }
  }
}
```

### Publishing Specific Libraries

If you only want to publish specific Nx libraries:

```bash
# Publish single library
cd libs/feature-auth
lnpm publish

# Or from root with filters
cd libs/ui && lnpm publish
cd libs/data-access && lnpm publish
```

---

## PNPM Workspaces

lnpm works alongside pnpm's workspace protocol.

### Setup

**pnpm-workspace.yaml:**

```yaml
packages:
  - 'packages/*'
  - 'apps/*'
```

**Root package.json:**

```json
{
  "scripts": {
    "lnpm:publish": "lnpm publish --all"
  },
  "devDependencies": {
    "lnpm": "*"
  }
}
```

### Workflow

```bash
# Install lnpm globally (one-time)
curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.sh | sh

# Publish all workspace packages
lnpm publish --all

# Link to external project
cd ~/external-app
lnpm add @my/package

# Build and push from monorepo
cd ~/my-monorepo
pnpm --filter @my/package build
lnpm push
```

---

## NPM Workspaces

### Setup

**Root package.json:**

```json
{
  "name": "my-monorepo",
  "private": true,
  "workspaces": [
    "packages/*",
    "apps/*"
  ],
  "scripts": {
    "lnpm:publish": "lnpm publish --all"
  },
  "devDependencies": {
    "lnpm": "*"
  }
}
```

### Workflow

```bash
# Install lnpm globally (one-time)
curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.sh | sh

# Publish all workspaces
lnpm publish --all

# Link to external project
cd ~/external-app
lnpm add @my/package

# Build and push from monorepo
cd ~/my-monorepo
npm run build -w @my/package
lnpm push
```

---

## Yarn Workspaces

### Setup

**Root package.json:**

```json
{
  "name": "my-monorepo",
  "private": true,
  "workspaces": [
    "packages/*"
  ],
  "scripts": {
    "lnpm:publish": "lnpm publish --all"
  },
  "devDependencies": {
    "lnpm": "*"
  }
}
```

### Workflow

```bash
# Install lnpm globally (one-time)
curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.sh | sh

# Publish all workspaces
lnpm publish --all

# Link to external project
cd ~/external-app
lnpm add @my/package

# Build and push from monorepo
cd ~/my-monorepo
yarn workspace @my/package build
lnpm push
```

---

## Best Practices

### 1. Install lnpm Globally

lnpm is a system binary, not an npm package. Install it globally once:

```bash
# ✅ Correct - install globally
curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.sh | sh

# ❌ Wrong - not an npm package!
npm install -D lnpm  # This won't work
```

### 2. Use `--all` Flag for Publishing

Publish all workspace packages at once:

```bash
lnpm publish --all
```

This respects your workspace configuration automatically.

### 3. Combine with Task Orchestration

Let your task runner handle builds, use lnpm for distribution:

**Turborepo:**
```bash
turbo run build --filter=@my/ui && lnpm push
```

**Nx:**
```bash
nx build my-lib && lnpm push
```

### 4. Link External Projects, Not Internal

Use your monorepo tool for internal dependencies. Use lnpm to link to **external** projects:

```bash
# Internal (use workspace protocol)
{
  "dependencies": {
    "@my/ui": "workspace:*"
  }
}

# External (use lnpm)
cd ~/external-app
lnpm add @my/ui
```

### 5. Git Stage for Fast Iteration

Enable fast change detection with git staging:

```bash
# Make changes
vim packages/ui/src/button.tsx

# Stage changes (no commit needed)
git add packages/ui/src/button.tsx

# Push immediately detects staged changes
lnpm push
```

### 6. Use Build Tool's Watch Mode

For active development, use your build tool's watch mode combined with `lnpm push`:

```bash
# Terminal 1: Watch and build with turbo/nx
turbo run build --filter=@my/ui --watch

# Terminal 2: Push when ready
lnpm push
```

Or integrate into your build scripts:

```json
{
  "scripts": {
    "build:dev": "tsc --watch --onSuccess \"lnpm push\""
  }
}
```

---

## Troubleshooting

### Issue: `lnpm: command not found`

lnpm must be installed globally as a system binary:

```bash
# Install globally
curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.sh | sh

# Then use directly
lnpm publish --all
```

### Issue: "No packages found in workspace"

Ensure your workspace configuration is correct:

**PNPM:** Check `pnpm-workspace.yaml`
**NPM/Yarn:** Check `workspaces` field in root `package.json`
**Turborepo:** Uses package manager's workspace config

### Issue: Changes not detected

Use git staging or force push:

```bash
# Stage changes
git add .

# Or force push
lnpm push --force
```

### Issue: Slow push times

Make sure store and source are on same filesystem for instant reflinks:

```bash
# Check if different filesystems
df -h ~/my-monorepo
df -h ~/.lnpm

# If different, move store to same filesystem
lnpm config store_path /Users/you/dev-store
```

### Issue: Nx/Turbo cache conflicts

lnpm and your task runner caches are independent:

```bash
# Clear Turborepo cache
turbo run build --force

# Clear Nx cache
nx reset

# lnpm doesn't need cache clearing (content-addressed)
```

---

## Advanced Patterns

### Selective Publishing

Publish only changed packages since last publish:

```bash
# Check status first
lnpm status

# Publish specific package
cd packages/ui
lnpm publish

# Push changes to linked projects
lnpm push
```

### CI/CD Integration

In CI, restore original dependencies before publishing to npm:

```bash
# Before npm publish
lnpm retreat --force

# Now safe to publish to npm registry
npm publish
```

### Debugging Monorepo Issues

Enable debug mode:

```bash
LNPM_DEBUG=1 lnpm publish --all
LNPM_DEBUG=1 lnpm push
```

---

## Summary

| Tool | Install Location | Publish Command | Push Command |
|------|------------------|-----------------|--------------|
| **Turborepo** | System (global) | `lnpm publish --all` | `lnpm push` |
| **Nx** | System (global) | `lnpm publish --all` | `lnpm push` |
| **PNPM** | System (global) | `lnpm publish --all` | `lnpm push` |
| **NPM** | System (global) | `lnpm publish --all` | `lnpm push` |
| **Yarn** | System (global) | `lnpm publish --all` | `lnpm push` |

**Key Takeaway:** lnpm complements your monorepo tool—it doesn't replace it. Use your tool for internal dependencies and orchestration, use lnpm to rapidly iterate with external projects.

