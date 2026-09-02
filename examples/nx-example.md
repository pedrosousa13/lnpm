# Nx + lnpm Example

Quick example showing lnpm with Nx monorepo.

## Structure

```
my-nx-workspace/
├── package.json                  # Root workspace config (lnpm is a system binary, not a dependency)
├── nx.json                       # Nx config
├── libs/
│   ├── feature-auth/
│   │   ├── package.json          # What lnpm publishes: @my-org/feature-auth
│   │   ├── project.json
│   │   ├── tsconfig.lib.json     # The build target's tsConfig
│   │   ├── src/
│   │   │   ├── index.ts
│   │   │   └── auth.service.ts
│   │   └── dist/
│   │       └── index.js          # Built output, and the package's main
│   └── ui/
│       ├── package.json          # @my-org/ui, no build step
│       ├── project.json
│       └── src/
│           └── index.ts
└── apps/
    └── web/
        └── project.json
```

Twelve files, and the count matters later: `lnpm push` from the root packs all of them.

## Setup

**1. Root package.json:**

```json
{
  "name": "my-nx-workspace",
  "version": "1.0.0",
  "private": true,
  "workspaces": [
    "libs/*"
  ],
  "scripts": {
    "lnpm:pub": "lnpm publish --all"
  },
  "devDependencies": {
    "nx": "latest",
    "@nx/js": "latest"
  }
}
```

`lnpm publish --all` reads the `workspaces` field, or `pnpm-workspace.yaml`. It does not read `nx.json`. Leave the field out and every `--all` in this file fails with `no workspace found. --all requires a monorepo with workspaces configured`.

No root `lnpm:push` script: `lnpm push` acts on the `package.json` in the directory it runs from, so from the root it would pack `my-nx-workspace` instead of your library. Push from `libs/feature-auth`, or via the `lnpm-push` target below, which sets `cwd` for you.

**2. Library project.json (libs/feature-auth/project.json):**

```json
{
  "name": "feature-auth",
  "$schema": "../../node_modules/nx/schemas/project-schema.json",
  "sourceRoot": "libs/feature-auth/src",
  "projectType": "library",
  "targets": {
    "build": {
      "executor": "@nx/js:tsc",
      "outputs": ["{options.outputPath}"],
      "options": {
        "outputPath": "dist/libs/feature-auth",
        "main": "libs/feature-auth/src/index.ts",
        "tsConfig": "libs/feature-auth/tsconfig.lib.json"
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

**3. Library package.json (libs/feature-auth/package.json):**

```json
{
  "name": "@my-org/feature-auth",
  "version": "1.0.0",
  "main": "./dist/index.js"
}
```

**4. Nx config (nx.json):**

```json
{
  "$schema": "./node_modules/nx/schemas/nx-schema.json",
  "targetDefaults": {
    "build": {
      "cache": true,
      "dependsOn": ["^build"]
    },
    "lnpm-push": {
      "dependsOn": ["build"]
    }
  }
}
```

## Usage

### Initial setup

```bash
# Install dependencies
npm install

# Build all libs
nx run-many --target=build --all

# Publish to lnpm store
npm run lnpm:pub
```

### Link to external project

```bash
# In your external app
cd ~/projects/my-app
lnpm add @my-org/feature-auth
lnpm add @my-org/ui

# Now node_modules has your libs linked
```

### Development workflow

**Option 1: Nx watch + push (recommended)**

Use Nx's own watch to rebuild on change, then push when ready:

```bash
cd ~/projects/my-nx-workspace

# Terminal 1: rebuild the library on change
nx watch --projects=feature-auth -- nx build feature-auth

# Terminal 2: push built output to linked projects
cd libs/feature-auth
lnpm push
```

What happens:
1. You edit `libs/feature-auth/src/auth.service.ts`
2. Nx's watch rebuilds `feature-auth` with computation caching
3. You run `lnpm push`, which links the built output to the external app
4. The app picks up the change

**Option 2: Manual push**

```bash
# Make changes
vim libs/feature-auth/src/auth.service.ts

# Build with Nx
nx build feature-auth

# Push to linked projects
cd libs/feature-auth
lnpm push
```

**Option 3: Use Nx task pipeline**

```bash
# Run custom lnpm-push target (builds + pushes)
nx run feature-auth:lnpm-push
```

## Advanced Usage

### Publish only buildable libraries

If you have non-buildable libs (just TypeScript, no build step), publish only buildable ones:

```bash
# From the workspace root - manually specify, each in its own directory
(cd libs/feature-auth && lnpm publish)
(cd libs/ui && lnpm publish)
(cd libs/data-access && lnpm publish)
```

`--all` will not do this. It publishes every package the root `workspaces` globs match, and neither it nor `lnpm publish` takes a filter, so a lib with no build step goes to the store as it sits on disk. A script has to name the same directories:

```json
{
  "scripts": {
    "lnpm:pub:buildable": "nx run-many --target=build --all && (cd libs/feature-auth && lnpm publish) && (cd libs/ui && lnpm publish)"
  }
}
```

### Iterate on multiple libraries

```bash
# Rebuild several libs (optionally with nx watch), then push each
nx run-many --target=build --projects=feature-auth,ui

(cd libs/feature-auth && lnpm push)
(cd libs/ui && lnpm push)
```

### Use Nx affected

```bash
# Build only affected libs
nx affected --target=build

# Then publish changed
lnpm publish --all
```

### Task graph

View the dependency graph:

```bash
nx graph
```

This shows how lnpm-push depends on build.

## Nx Module Federation

If using Module Federation, lnpm works great for rapid remote development:

```bash
# Publish the remote
cd apps/remote-app
lnpm publish

# Link in host
cd ~/projects/host-app
lnpm add remote-app

# Rebuild remote, then push
cd ~/projects/my-nx-workspace
nx build remote-app
cd apps/remote-app
lnpm push
```

The host will load the updated remote module on each change.

## Tips

**1. Use Nx run-many for batch operations:**

```bash
# Build multiple targets
nx run-many --target=build --projects=feature-auth,ui

# Then publish
lnpm publish --all
```

**2. Nx caching speeds up builds:**

Nx caches build outputs. Combined with lnpm's instant linking, iteration is extremely fast.

**3. Affected command for large monorepos:**

```bash
# Only build what changed
nx affected --target=build

# Then selectively publish
cd libs/feature-auth
lnpm push
```

**4. Combine with Nx Console:**

Install Nx Console VS Code extension. Run tasks from UI, then use lnpm to push.

## Troubleshooting

**Issue: "nx: command not found"**

Install Nx:
```bash
npm install -D nx @nx/js
```

**Issue: Builds not producing output**

Check your project.json outputs configuration:
```json
{
  "targets": {
    "build": {
      "outputs": ["{options.outputPath}"]
    }
  }
}
```

**Issue: Changes not picked up after a push**

`cd` into the library directory before running `lnpm push`. Push packs whatever is on disk in the directory you run it from — git state plays no part — so from the workspace root it publishes the root package and exits successfully without touching your library:
```bash
$ cd ~/projects/my-nx-workspace
$ lnpm push
Package my-nx-workspace not published yet, publishing...
Publishing my-nx-workspace@1.0.0 (12 files)...
✓ Published my-nx-workspace@1.0.0
  Hash: beb035af
  Files: 12
  Size: 1.74 KB
  Store: /home/you/.lnpm/store/my-nx-workspace/beb035af7d44461d
  Packed:
    apps/web/project.json
    libs/feature-auth/dist/index.js
    libs/feature-auth/package.json
    libs/feature-auth/project.json
    libs/feature-auth/src/auth.service.ts
    libs/feature-auth/src/index.ts
    libs/feature-auth/tsconfig.lib.json
    libs/ui/package.json
    libs/ui/project.json
    libs/ui/src/index.ts
    nx.json
    package.json
```

That is every file in the Structure section at the top of this page — the whole workspace, swept up as one package. Read the `Packed:` list when you are not sure which project you just shipped: `libs/feature-auth/dist/index.js` is in there, but as data inside `my-nx-workspace`, and an app that ran `lnpm add @my-org/feature-auth` resolves that name and never sees it.

Run it from the library instead:
```bash
$ cd libs/feature-auth
$ lnpm push
Pushing @my-org/feature-auth@1.0.0...
  Packed:
    dist/index.js
    package.json
    project.json
    src/auth.service.ts
    src/index.ts
    tsconfig.lib.json
Updating 1 linked projects...
  ✓ /home/you/projects/my-app (1 changed, 5 unchanged)

Pushed to 1/1 projects
```

Build before you push, and make sure the build output lands where push will see it. The library's `main` is `./dist/index.js`, so the app loads `libs/feature-auth/dist` — editing `src/` and pushing ships the new sources alongside the old build, and the app keeps running the previous one. Note that the `build` target above writes to `dist/libs/feature-auth` at the workspace root, which is outside the directory push packs; point its `outputPath` at `libs/feature-auth/dist` so the output lands inside the library.

lnpm does run your `prepack` and `prepare` scripts before packing (`publish` also runs `prepublishOnly`), so wiring `nx build feature-auth` into one of those makes push rebuild for you. A plain `build` script is not run automatically — that is what the `lnpm-push` target's `dependsOn` is for.

**Issue: Nx cache conflicts**

Clear Nx cache if needed:
```bash
nx reset
```

Then rebuild and publish:
```bash
nx build feature-auth
cd libs/feature-auth
lnpm push
```

## Example Commands Summary

```bash
# Publish all workspace libs (from the workspace root)
npm run lnpm:pub

# Link to external project
(cd ~/my-app && lnpm add @my-org/feature-auth)

# Build then push (from lib directory)
nx build feature-auth
(cd libs/feature-auth && lnpm push)

# Use custom Nx target (builds + pushes)
nx run feature-auth:lnpm-push

# Build affected only
nx affected --target=build
lnpm publish --all
```

