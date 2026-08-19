# Turborepo + lnpm Example

Quick example showing lnpm with Turborepo.

## Structure

```
my-turborepo/
├── package.json         # Root with lnpm installed
├── turbo.json          # Turborepo config
├── packages/
│   └── ui/
│       ├── package.json
│       ├── src/
│       └── dist/       # Built output
└── apps/
    └── web/
        └── package.json
```

## Setup

**1. Root package.json:**

```json
{
  "name": "my-turborepo",
  "version": "0.0.0",
  "private": true,
  "workspaces": [
    "apps/*",
    "packages/*"
  ],
  "scripts": {
    "dev": "turbo run dev",
    "build": "turbo run build",
    "lnpm:pub": "lnpm publish --all",
    "lnpm:push": "lnpm push"
  },
  "devDependencies": {
    "turbo": "latest"
  }
}
```

**2. Package config (packages/ui/package.json):**

```json
{
  "name": "@my/ui",
  "version": "1.0.0",
  "main": "./dist/index.js",
  "scripts": {
    "build": "tsc",
    "dev": "tsc --watch"
  }
}
```

**3. Turborepo config (turbo.json):**

```json
{
  "$schema": "https://turbo.build/schema.json",
  "pipeline": {
    "build": {
      "dependsOn": ["^build"],
      "outputs": ["dist/**"]
    },
    "dev": {
      "cache": false,
      "persistent": true
    }
  }
}
```

## Usage

### Initial setup

```bash
# Install dependencies
npm install

# Build all packages
npm run build

# Publish to lnpm store
npm run lnpm:pub
```

### Link to external project

```bash
# In your external app
cd ~/projects/my-app
lnpm add @my/ui

# Now my-app's node_modules/@my/ui links to the built package
```

### Development workflow

**Option 1: Build-tool watch + push (recommended)**

Run Turborepo's own watch mode to rebuild on change, then push when you're ready:

```bash
cd ~/projects/my-turborepo

# Terminal 1: watch and rebuild on changes
turbo run build --filter=@my/ui --watch

# Terminal 2: push built output to linked projects
lnpm push
```

What happens:
1. You edit `packages/ui/src/button.tsx`
2. Turborepo's watch rebuilds `@my/ui` (using cache if possible)
3. You run `lnpm push`, which links the built output to `~/projects/my-app/node_modules/@my/ui`
4. Your app picks up the change

**Option 2: Manual build + push**

```bash
# Make changes
vim packages/ui/src/button.tsx

# Build with Turborepo
npm run build

# Push to linked projects
lnpm push
```

## Tips

**1. Use Turborepo's filtering:**

```bash
# Build only affected packages
turbo run build --filter=@my/ui

# Build with dependencies
turbo run build --filter=@my/ui...
```

**2. Combine Turborepo watch with push:**

```bash
# Terminal 1: rebuild on change
turbo run build --filter=@my/ui --watch

# Terminal 2: push built output when ready
lnpm push
```

**3. Multiple packages:**

Publish all at once, push as you iterate:

```bash
# Publish everything
npm run lnpm:pub

# Link multiple packages in external app
cd ~/projects/my-app
lnpm add @my/ui
lnpm add @my/components
lnpm add @my/utils

# Rebuild a specific package, then push
cd ~/projects/my-turborepo
turbo run build --filter=@my/ui
lnpm push
```

## Troubleshooting

**Issue: "turbo: command not found"**

Install Turborepo:
```bash
npm install -D turbo
```

**Issue: Builds not running**

Test the build command first:
```bash
turbo run build --filter=@my/ui
```

Then run it (optionally in watch) before pushing:
```bash
turbo run build --filter=@my/ui --watch
```

**Issue: Changes not picked up after a push**

`lnpm push` packs whatever is on disk in the directory you run it from, so git state is irrelevant — there is nothing to stage or commit first. Check these two instead.

Run push from the package directory, not the monorepo root. Pushing from the root publishes the root package, and it exits successfully while your library goes nowhere:
```bash
$ cd ~/projects/my-turborepo
$ lnpm push
Package my-turborepo not published yet, publishing...
Publishing my-turborepo@0.0.0 (6 files)...
✓ Published my-turborepo@0.0.0
  Hash: 37e17a1b
  Files: 6
  Size: 590 B
  Store: /home/you/.lnpm/store/my-turborepo/37e17a1bf0121ca4
```

Run it from the package instead:
```bash
$ cd packages/ui
$ lnpm push
Pushing @my/ui@1.0.0...
Updating 1 linked projects...
  ✓ /home/you/projects/my-app

Pushed to 1/1 projects
```

And build before you push. `@my/ui` points `main` at `./dist/index.js`, so the app loads the built output — editing `src/` alone ships the previous build and push still reports `Pushed to 1/1 projects`:
```bash
turbo run build --filter=@my/ui && lnpm push
```

lnpm does run your `prepublishOnly`, `prepare` and `prepack` scripts before packing, so moving the build into one of those makes push rebuild for you. The plain `build` script above is not run automatically.

