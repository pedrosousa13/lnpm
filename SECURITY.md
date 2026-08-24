# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 2.x     | :white_check_mark: |
| 1.x     | :x:                |

This table is not generated. The maintainer updates it by hand when the
supported line changes, which in practice means at a major release. Whether
release tooling should own it instead is undecided, and is tracked in
[#389](https://github.com/pedrosousa13/lnpm/issues/389).

## Security Considerations

### Lifecycle Scripts

lnpm runs `package.json` lifecycle scripts (`prepare`, `prepublishOnly`, `prepack`) during `publish` and `push`, similar to npm. Users should:

- Only `publish`/`push` packages whose scripts they trust
- Be aware that scripts run with the same permissions as the user
- Use `--skip-hooks` to skip these scripts when needed

### File System Access

lnpm operates within these directories:

- **Store**: `~/.lnpm/` (configurable via `LNPM_STORE` or config)
- **Project**: Current working directory and its `.lnpm/` subdirectory
- **node_modules**: Symlinks created in project's `node_modules/`

A package name is untrusted input: it comes from a `package.json` or an
`lnpm.lock` that is checked into the repository, so whoever wrote the repository
chose it. Against that, lnpm:

- Validates the name before building a path it will **write to or delete**, at
  each boundary that does so — `Store.Store`, the linker's
  `Link`/`LinkSource`/`Unlink`, packing, and `retreat`'s pass over `lnpm.lock`.
  A name that is absolute, holds a `.` or `..` segment, holds a backslash or a
  NUL, or has more than the one `/` a scope allows, is rejected there.
- Requires `.lnpm` — and, for a scoped package, `.lnpm/{scope}` — to be a real
  directory, so a repository cannot commit either as a symlink and redirect
  every write and delete underneath it.
- Requires the same of `node_modules` — and, for a scoped package,
  `node_modules/{scope}` — before creating the link into `.lnpm` and before
  removing it again, in the linker and in `retreat` alike, through one shared
  predicate. That check is overridable, and `.lnpm`'s is not: relocating
  `node_modules` to another volume or out of a synced folder is a setup people
  run, so `follow_symlinked_node_modules: true` in the config file turns it off.
  Leaving it on is what stops a committed link from aiming lnpm's directory
  creation and its deletes outside the project.

Note that `filepath.Join()` is not itself a defence: it cleans the path it
builds, so `..` segments in a name survive into the result. The validation
above is what stops them.

Known limits, which this section deliberately does not claim otherwise about:

- The checks are not atomic. A path validated as safe can be replaced between
  the check and the use.
- The `node_modules` guard is overridable and `.lnpm`'s is not, so a project
  that sets `follow_symlinked_node_modules` gets the old behaviour back,
  redirect included. That is the trade relocated `node_modules` setups are
  worth, not an oversight. The key is named for the symlink it exists for but
  switches the whole check off, so a regular file or a device at either path is
  accepted under it too.
- The guard covers `node_modules` and `node_modules/{scope}`. The entry beneath
  them is removed with calls that do not follow a link at their last component,
  so a package's own `node_modules/{package}` needs no equivalent check.
- Read paths are not covered. `Store.GetFiles` walks the store path it builds
  from a name, and the link-status queries `pull` runs only `Lstat` it, and
  neither validates the name first. They read rather than write, so they cannot
  destroy anything, but a name chosen by the repository can still steer where
  they look.

### Database

lnpm uses bbolt, an embedded key-value database:
- Database file: `~/.lnpm/lnpm.db`
- File permissions: `0600` (owner read/write only)
- No network access or SQL injection vectors

### Hard Links

Hard links share the same inode as the source file:
- Changes to linked files affect all links
- This is intentional for the sync functionality
- Hard links cannot cross filesystem boundaries (lnpm falls back to copy)

## Reporting a Vulnerability

Please report security vulnerabilities privately. GitHub's private vulnerability
reporting is the preferred channel:

**[Report a vulnerability](https://github.com/pedrosousa13/lnpm/security/advisories/new)**

You can also reach the same form from the repository's
[Security tab](https://github.com/pedrosousa13/lnpm/security) via the
**Report a vulnerability** button. The report is visible only to the maintainers
until an advisory is published.

When reporting:

1. **DO NOT** open a public issue
2. Include:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact
   - Suggested fix (if any)

We will respond within 48 hours and work with you to understand and address the issue.

## Best Practices for Users

1. **Review before publish**: Run `lnpm retreat` before publishing to npm
2. **Trusted sources only**: Only `lnpm add` packages you've published yourself
3. **Lifecycle scripts**: Be cautious publishing packages with untrusted lifecycle scripts; use `--skip-hooks` if needed
4. **Permissions**: Keep `~/.lnpm/` permissions restricted
