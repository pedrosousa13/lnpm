# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.x.x   | :white_check_mark: |

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

Path traversal attacks are mitigated by:
- Using `filepath.Join()` for all path construction
- Validating package names don't contain path separators

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

Please report security vulnerabilities by:

1. **DO NOT** open a public issue
2. Email security concerns to the maintainers
3. Include:
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
