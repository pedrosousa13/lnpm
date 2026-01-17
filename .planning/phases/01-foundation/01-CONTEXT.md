# Phase 1: Foundation - Context

**Gathered:** 2026-01-17
**Status:** Ready for planning

<domain>
## Phase Boundary

Establish quality baseline with comprehensive error handling and static analysis tooling. Fix all unchecked errors, implement error wrapping with context, eliminate string error comparisons, configure golangci-lint v2, and enforce all checks in CI.

</domain>

<decisions>
## Implementation Decisions

### CI Enforcement Policy
- **Strict timeouts**: 5-10 minutes max for CI jobs — catch infinite loops, force efficiency
- Timeout applies to all CI jobs (linting, tests, race detection)

### Error Message Conventions
- **Include function names**: Error messages include function names for easier debugging
  - Example: `publishPackage: failed to read manifest: %w`
  - Function names provide call stack context without verbose logging

### Claude's Discretion

Claude has full discretion on:

**Error wrapping approach:**
- Detail level in wrapped errors (operation name vs operation + params)
- Wrapping at every layer vs boundary wrapping only
- Choice between stdlib errors (io.EOF) vs custom sentinel errors
- Strategy for defer Close() error handling (check all vs critical paths only)

**Linter rollout strategy:**
- Pace of rule enablement (all at once vs gradual)
- Handling existing violations (fix all first vs baseline + prevent new)
- Auto-fix usage where safe vs manual fixes
- Severity levels (all errors vs errors + warnings)

**CI enforcement policy:**
- Fail fast vs collect all issues before failing
- Separate vs combined jobs for linting/race/tests
- Caching strategy for dependencies and artifacts

**Error message conventions:**
- Structured format vs free-form messages
- Target audience (developer-focused vs user-friendly vs mixed)
- Error chain printing depth (full chain vs top-level only)

</decisions>

<specifics>
## Specific Ideas

No specific requirements — standard Go best practices apply.

Focus is on establishing patterns that will be followed throughout remaining phases.

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 01-foundation*
*Context gathered: 2026-01-17*
