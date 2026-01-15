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
  "private": true,
  "workspaces": [
    "apps/*",
    "packages/*"
  ],
  "scripts": {
    "dev": "turbo run dev",
    "build": "turbo run build",
    "lnpm:pub": "lnpm publish --all",
    "lnpm:dev": "lnpm watch --exec 'turbo run build --filter'"
  },
  "devDependencies": {
    "turbo": "latest",
    
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

**Option 1: Watch mode (recommended)**

```bash
cd ~/projects/my-turborepo

# Watch and auto-rebuild on changes
npm run lnpm:dev -- @my/ui
# or directly:
lnpm watch --exec "turbo run build --filter=@my/ui"
```

What happens:
1. You edit `packages/ui/src/button.tsx`
2. lnpm detects the change
3. Runs: `turbo run build --filter=@my/ui`
4. Turborepo builds (using cache if possible)
5. lnpm pushes to `~/projects/my-app/node_modules/@my/ui`
6. Your app hot-reloads

**Option 2: Manual push**

```bash
# Make changes
vim packages/ui/src/button.tsx

# Build with Turborepo
npm run build

# Push to linked projects
lnpm push
```

### Working with git

```bash
# Make changes
vim packages/ui/src/button.tsx

# Stage (no commit needed!)
git add packages/ui/src/button.tsx

# Push detects staged changes instantly
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

**2. Combine watch with Turborepo dev mode:**

```bash
# Terminal 1: Run Turborepo dev servers
npm run dev

# Terminal 2: Watch and push builds
lnpm watch --exec "turbo run build --filter=@my/ui"
```

**3. Multiple packages:**

Publish all at once, watch individually:

```bash
# Publish everything
npm run lnpm:pub

# Link multiple packages in external app
cd ~/projects/my-app
lnpm add @my/ui
lnpm add @my/components
lnpm add @my/utils

# Watch specific package
cd ~/projects/my-turborepo
lnpm watch --exec "turbo run build --filter=@my/ui"
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

Then use it in watch:
```bash
lnpm watch --exec "turbo run build --filter=@my/ui"
```

**Issue: Changes not pushed**

Use git staging or force:
```bash
git add .
lnpm push
# or
lnpm push --force
```

