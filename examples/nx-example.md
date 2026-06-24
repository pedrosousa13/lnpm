# Nx + lnpm Example

Quick example showing lnpm with Nx monorepo.

## Structure

```
my-nx-workspace/
├── package.json         # Root with lnpm installed
├── nx.json             # Nx config
├── libs/
│   ├── feature-auth/
│   │   ├── project.json
│   │   ├── src/
│   │   └── dist/
│   └── ui/
│       ├── project.json
│       └── src/
└── apps/
    └── web/
        └── project.json
```

## Setup

**1. Root package.json:**

```json
{
  "name": "my-nx-workspace",
  "version": "1.0.0",
  "private": true,
  "scripts": {
    "lnpm:pub": "lnpm publish --all",
    "lnpm:push": "lnpm push"
  },
  "devDependencies": {
    "nx": "latest",
    "@nx/js": "latest"
  }
}
```

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

### Working with git

```bash
# Make changes
vim libs/ui/src/button.tsx

# Stage (no commit needed!)
git add libs/ui/

# Push detects staged changes
cd libs/ui
lnpm push
```

## Advanced Usage

### Publish only buildable libraries

If you have non-buildable libs (just TypeScript, no build step), publish only buildable ones:

```bash
# From root - manually specify
cd libs/feature-auth && lnpm publish
cd libs/ui && lnpm publish
cd libs/data-access && lnpm publish
```

Or create a script:

```json
{
  "scripts": {
    "lnpm:pub:buildable": "nx run-many --target=build --all && lnpm publish --all"
  }
}
```

### Iterate on multiple libraries

```bash
# Rebuild several libs (optionally with nx watch), then push each
nx run-many --target=build --projects=feature-auth,ui

cd libs/feature-auth && lnpm push
cd libs/ui && lnpm push
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

**Issue: Changes not pushed**

Make sure you're in the library directory or use git staging:
```bash
cd libs/feature-auth
lnpm push

# Or from root with git
git add libs/feature-auth/
cd libs/feature-auth
lnpm push
```

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
# Publish all workspace libs
npm run lnpm:pub

# Link to external project
cd ~/my-app && lnpm add @my-org/feature-auth

# Build then push (from lib directory)
nx build feature-auth
cd libs/feature-auth
lnpm push

# Use custom Nx target (builds + pushes)
nx run feature-auth:lnpm-push

# Build affected only
nx affected --target=build
lnpm publish --all
```

