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
lnpm ← one binary on your PATH (e.g. /usr/local/bin/lnpm), never in node_modules

my-monorepo/                    external-app/
├── package.json                ├── package.json
├── packages/                   └── node_modules/
│   └── ui/                         └── @my/ui ← linked from monorepo
│       ├── package.json
│       └── src/index.js
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
    "lnpm:publish": "lnpm publish --all"
  },
  "devDependencies": {
    "turbo": "latest"
  }
}
```

There is deliberately no root `lnpm:push` script here. `lnpm publish --all` reads your workspace configuration and so works from the root, but `lnpm push` only ever acts on the `package.json` in the directory it runs from — a root script would pack the root package and exit 0 without touching your library. Push an individual package from inside that package's directory:

```bash
cd packages/ui && lnpm push
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

# Build and push changes — push from the package you changed, not from apps/web
cd ../../packages/ui
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

Add the matching script to **the package's own `package.json`** — `packages/ui/package.json`, not the root one. Turborepo runs each task with that package's directory as the working directory, which is what makes `lnpm push` act on `@my/ui`:

```json
{
  "name": "@my/ui",
  "version": "1.0.0",
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
    "lnpm:publish": "lnpm publish --all"
  }
}
```

As in the Turborepo section, there is no root push script: `lnpm push` acts on whichever `package.json` is in the current directory. Push a single library either from inside it (`cd libs/feature-auth && lnpm push`) or through the Nx target below, which sets `cwd` for you.

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

Note the `outputPath`: it follows the Nx default of `dist/libs/feature-auth` at the workspace root, which is outside the directory `lnpm push` packs. Point it at `libs/feature-auth/dist` instead, or every push ships the library's previous build no matter how often you rebuild.

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
# From the workspace root, publish one library
(cd libs/feature-auth && lnpm publish)

# Or several, each from its own directory
(cd libs/ui && lnpm publish)
(cd libs/data-access && lnpm publish)
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
  }
}
```

lnpm is not listed as a dependency — it is a system binary, so nothing in the workspace installs it.

### Workflow

```bash
# Install lnpm globally (one-time)
curl -fsSL https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.sh | sh

# Publish all workspace packages
lnpm publish --all

# Link to external project
cd ~/external-app
lnpm add @my/ui

# Build from the root, then push from the package directory
cd ~/my-monorepo
pnpm --filter @my/ui build
cd packages/ui
lnpm push
```

---

## NPM Workspaces

### Setup

**Root package.json:**

```json
{
  "name": "my-monorepo",
  "version": "0.0.0",
  "private": true,
  "workspaces": [
    "packages/*",
    "apps/*"
  ],
  "scripts": {
    "lnpm:publish": "lnpm publish --all"
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
lnpm add @my/ui

# Build from the root, then push from the package directory
cd ~/my-monorepo
npm run build -w @my/ui
cd packages/ui
lnpm push
```

---

## Yarn Workspaces

### Setup

**Root package.json:**

```json
{
  "name": "my-monorepo",
  "version": "0.0.0",
  "private": true,
  "workspaces": [
    "packages/*"
  ],
  "scripts": {
    "lnpm:publish": "lnpm publish --all"
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
lnpm add @my/ui

# Build from the root, then push from the package directory
cd ~/my-monorepo
yarn workspace @my/ui build
cd packages/ui
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

Let your task runner handle builds, use lnpm for distribution. The build is filtered by package name, but the push is decided by the current directory, so `cd` into the package before pushing:

**Turborepo:**
```bash
turbo run build --filter=@my/ui
cd packages/ui && lnpm push
```

**Nx:**
```bash
nx build feature-auth
cd libs/feature-auth && lnpm push
```

Nx writes build output to `dist/libs/<name>` at the workspace root by default, which push never sees — see [Nx Project Configuration](#nx-project-configuration).

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

### 5. Use Build Tool's Watch Mode

For active development, use your build tool's watch mode combined with `lnpm push`:

```bash
# Terminal 1: Watch and build with turbo/nx
turbo run build --filter=@my/ui --watch

# Terminal 2: Push when ready, from the package directory
cd packages/ui
lnpm push
```

Or integrate into the package's own `package.json` — never the root one, since the script's working directory is what push acts on:

```json
{
  "name": "@my/ui",
  "version": "1.0.0",
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

### Issue: Changes not picked up after a push

`lnpm push` re-packs whatever is on disk in the directory you run it from. It does not consult git, so committing or staging makes no difference. Two things usually explain a push that appears to succeed but changes nothing.

**1. You ran it from the workspace root instead of the package directory.**

The root `package.json` of a monorepo is itself a package as far as lnpm is concerned, so pushing from the root publishes the root package, not your library:

```bash
$ cd ~/my-monorepo
$ lnpm push
Package my-monorepo not published yet, publishing...
Publishing my-monorepo@0.0.0 (4 files)...
✓ Published my-monorepo@0.0.0
  Hash: 10618390
  Files: 4
  Size: 334 B
  Store: /home/you/.lnpm/store/my-monorepo/10618390ed098b47
  Packed:
    apps/web/package.json
    package.json
    packages/ui/package.json
    packages/ui/src/index.js
```

That exits 0 and nothing warns you. The `Packed:` list is the tell — it is the whole workspace laid out above, not your library. `@my/ui`'s sources are in there under `packages/ui/`, but as data inside `my-monorepo`, and an app that ran `lnpm add @my/ui` resolves that name and never sees them. `cd` into the package you actually changed and push from there:

```bash
$ cd packages/ui
$ lnpm push
Pushing @my/ui@1.0.0...
  Packed:
    package.json
    src/index.js
Updating 1 linked projects...
  ✓ /home/you/external-app (1 changed, 1 unchanged)

Pushed to 1/1 projects
```

A directory with no `package.json` at all is the one case push does complain about:

```bash
$ cd ~/my-monorepo/apps
$ lnpm push
Error: failed to read package.json: failed to read package.json: open /home/you/my-monorepo/apps/package.json: no such file or directory
```

**2. You changed a source file but did not rebuild.**

If `files` in your `package.json` ships `dist/`, then `dist/` is what push packs. Editing `src/` without running the build sends the previous build's output, and the push still reports `Pushed to 1/1 projects`. Run your build first:

```bash
# Turborepo
turbo run build --filter=@my/ui
cd packages/ui && lnpm push
```

```bash
# Nx
nx build feature-auth
cd libs/feature-auth && lnpm push
```

With Nx, also check where the build writes: its default `outputPath` sits at the workspace root, outside the directory push packs, so the rebuild never reaches the pushed package. See [Nx Project Configuration](#nx-project-configuration).

lnpm runs your `prepublishOnly`, `prepack` and `prepare` scripts before packing, so if your build is wired into one of those, push rebuilds for you. A plain `build` script is not run automatically.

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

# Push changes to linked projects (still inside packages/ui)
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
# From the workspace root
LNPM_DEBUG=1 lnpm publish --all

# From the package you want to push
cd packages/ui
LNPM_DEBUG=1 lnpm push
```

---

## Summary

| Tool | Install Location | Publish Command (run from the workspace root) | Push Command (run from the package directory) |
|------|------------------|-----------------------------------------------|-----------------------------------------------|
| **Turborepo** | System (global) | `lnpm publish --all` | `cd packages/ui && lnpm push` |
| **Nx** | System (global) | `lnpm publish --all` | `cd libs/feature-auth && lnpm push` |
| **PNPM** | System (global) | `lnpm publish --all` | `cd packages/ui && lnpm push` |
| **NPM** | System (global) | `lnpm publish --all` | `cd packages/ui && lnpm push` |
| **Yarn** | System (global) | `lnpm publish --all` | `cd packages/ui && lnpm push` |

**Key Takeaway:** lnpm complements your monorepo tool—it doesn't replace it. Use your tool for internal dependencies and orchestration, use lnpm to rapidly iterate with external projects.

