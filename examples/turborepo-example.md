# Turborepo + lnpm Example

Quick example showing lnpm with Turborepo.

## Structure

```
my-turborepo/
├── package.json              # Root workspace config (lnpm is a system binary, not a dependency)
├── turbo.json                # Turborepo config
├── packages/
│   └── ui/
│       ├── package.json
│       ├── src/
│       │   └── index.ts
│       └── dist/
│           └── index.js      # Built output, and the package's main
└── apps/
    └── web/
        └── package.json
```

Six files, and the count matters later: `lnpm push` from the root packs all of them.

## Setup

**1. Root package.json:**

```json
{
  "name": "my-turborepo",
  "version": "0.0.0",
  "private": true,
  "packageManager": "npm@11.6.2",
  "workspaces": [
    "apps/*",
    "packages/*"
  ],
  "scripts": {
    "dev": "turbo run dev",
    "build": "turbo run build",
    "lnpm:pub": "lnpm publish --all"
  },
  "devDependencies": {
    "turbo": "latest"
  }
}
```

Two fields carry weight here. `workspaces` is how `lnpm publish --all` finds your packages. `packageManager` is how Turborepo 2.x identifies the package manager. Leave it out, with no `devEngines.packageManager` block either, and every `turbo` command stops at `Could not resolve workspace`. Set it to the version you actually use.

No root `lnpm:push` script: `lnpm push` acts on the `package.json` in the directory it runs from, so from the root it would pack `my-turborepo` instead of `@my/ui`. Push from `packages/ui`, or via the `lnpm:push` script below, which Turborepo runs with that package's directory as its cwd.

**2. Package config (packages/ui/package.json):**

```json
{
  "name": "@my/ui",
  "version": "1.0.0",
  "main": "./dist/index.js",
  "scripts": {
    "build": "tsc",
    "dev": "tsc --watch",
    "lnpm:push": "lnpm push"
  }
}
```

**3. Turborepo config (turbo.json):**

```json
{
  "$schema": "https://turbo.build/schema.json",
  "tasks": {
    "build": {
      "dependsOn": ["^build"],
      "outputs": ["dist/**"]
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

Then `turbo run lnpm:push --filter=@my/ui` builds `@my/ui` and pushes it, both with `packages/ui` as the working directory.

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
# Terminal 1: watch and rebuild on changes
cd ~/projects/my-turborepo
turbo watch build --filter=@my/ui

# Terminal 2: push built output to linked projects
cd ~/projects/my-turborepo/packages/ui
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

# Push to linked projects, from the package directory
cd packages/ui
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
# Terminal 1: rebuild on change (from the workspace root)
turbo watch build --filter=@my/ui

# Terminal 2: push built output when ready (from the package)
cd packages/ui && lnpm push
```

Or run the `lnpm:push` task under watch and get both in one command:

```bash
turbo watch lnpm:push --filter=@my/ui
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

# Rebuild a specific package, then push from that package's directory
cd ~/projects/my-turborepo
turbo run build --filter=@my/ui
cd packages/ui
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
turbo watch build --filter=@my/ui
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
  Hash: d49407af
  Files: 6
  Size: 929 B
  Store: /home/you/.lnpm/store/my-turborepo/d49407af3568da7d
  Packed:
    apps/web/package.json
    package.json
    packages/ui/dist/index.js
    packages/ui/package.json
    packages/ui/src/index.ts
    turbo.json
```

That is every file in the Structure section at the top of this page — the whole workspace, swept up as one package. Read the `Packed:` list when you are not sure which package you just shipped: `@my/ui`'s files are in there, but under `packages/ui/`, as data inside `my-turborepo`, and an app that ran `lnpm add @my/ui` resolves `@my/ui` and never sees them.

Run it from the package instead:
```bash
$ cd packages/ui
$ lnpm push
Pushing @my/ui@1.0.0...
  Packed:
    dist/index.js
    package.json
    src/index.ts
Updating 1 linked projects...
  ✓ /home/you/projects/my-app (1 changed, 2 unchanged)

Pushed to 1/1 projects
```

And build before you push. `@my/ui` points `main` at `./dist/index.js`, so the app loads the built output — editing `src/` alone ships the previous build and push still reports `Pushed to 1/1 projects`:
```bash
turbo run build --filter=@my/ui
cd packages/ui && lnpm push
```

lnpm does run your `prepack` and `prepare` scripts before packing (`publish` also runs `prepublishOnly`), so moving the build into one of those makes push rebuild for you. The plain `build` script above is not run automatically.

