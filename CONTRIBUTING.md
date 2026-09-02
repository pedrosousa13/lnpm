# Contributing

## What you need

Go, and Node on `PATH` for the tests that shell out to a real runtime. Nothing else.

The Go version lives in `go.mod`. `mise.toml` pins the same version so `mise install` gives you
a matching toolchain, but CI does not read `mise.toml`. It reads `go.mod` through `setup-go`'s
`go-version-file`. Move one and move the other.

```bash
git clone https://github.com/pedrosousa13/lnpm.git
cd lnpm
make deps
make build
```

`make build` writes `./lnpm` in the repository root. `make install-local` copies that binary to
`~/.local/bin`, which is how you drive a real project with your own build.

The Makefile is GNU make. On Windows the same work is `go mod download`, `go build ./cmd/lnpm`,
`go test ./...` and `go vet ./...`.

## The targets

| Target | What it runs |
| --- | --- |
| `make build` | `go build` into `./lnpm`, with version, commit and date stamped in |
| `make test` | `go test -v ./...`, which is every tier below |
| `make test-coverage` | the same, plus `coverage.html` |
| `make lint` | `golangci-lint run ./...`, or `go vet ./...` when golangci-lint is not installed |
| `make fmt` | `go fmt ./...`, plus `goimports` when it is installed |
| `make release` | cross-compiles all five release targets into `bin/` |
| `make hooks-enable` | points `core.hooksPath` at `.githooks` |

`make help` lists the rest. The benchmark targets (`make bench`, `make bench-compare`) are there
when a change is meant to move a number.

Two of these hide a fallback worth knowing about. `make lint` falls back to `go vet`, which is a
far smaller check than the one CI runs, so a clean `make lint` on a machine without
golangci-lint says very little. CI pins golangci-lint at v2.12.2; install that version locally
and the two agree. `make fmt` skips `goimports` the same way.

The git hooks are opt in. `make hooks-enable` gives you a pre-commit hook that lints staged
changes and a pre-push hook that runs the suite and the linter.

## Test tiers

Three of them, and they prove different things.

**Unit tests sit beside the code**, in `internal/**`, `pkg/**` and `cmd/**`. They are in-package
and they test one thing.

**`tests/` is the integration tier.** These call the `cli.RunX` entry points in process against
a temporary store and temporary projects. This is where most behaviour is pinned, and it is
where a bug report usually turns into a failing test.

**`tests/e2e/` is the end-to-end tier.** `TestMain` compiles the real binary once and the tests
run node against realistic monorepo layouts, proving that a consumer app actually resolves the
linked package through the `node_modules` symlink. Without node on `PATH` every test in the
package skips, except under `CI=true`, where a missing node is fatal instead. That asymmetry is
deliberate. A green e2e run on a machine with no node has proved nothing, and CI must not be
able to go green that way.

### Config isolation

lnpm reads `~/.lnpm/config.yaml`, and a test that reaches the loader will read the machine's
real one unless something stops it. `internal/testenv` is that something. A test package that
touches config needs:

```go
func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
```

Per binary, not per test. A single test that wants settings of its own has more to do than
redirect `LNPM_CONFIG`, because the loader memoises behind a `sync.Once` for the life of the
binary. The redirect has to be paired with `config.ResetForTesting()`. The doc comment on
`testenv.Run` spells out which call sites need the pairing and which do not, and it is worth
reading before you copy an existing test.

`internal/config` cannot import `testenv`, which would be a cycle. Its own `TestMain` calls
`config.IsolateForTesting` directly.

### Platform notes

CI runs every tier with `-race` on Linux, macOS and Windows. A data race usually surfaces there
before it surfaces locally.

Some symlink tests skip on Windows. lnpm uses NTFS junctions there, which are absolute, not
relative symlinks. Those skips are expected.

Before pushing anything that touches file paths or syscalls, check it still builds everywhere:

```bash
GOOS=linux go build ./...
GOOS=darwin go build ./...
GOOS=windows go build ./...
```

## The pull request title

The title has to be a conventional commit subject, and this is not a style preference.

Merges here are squashed. The pull request title becomes the commit subject on `main`, and
release-please parses that subject to build the changelog and pick the version bump. A title
outside the conventional form is not rejected by release-please. It is silently dropped, so the
change ships with no changelog entry and contributes nothing to the version. That is why
`.github/workflows/pr-title.yaml` is a required check.

Accepted types are `feat`, `fix`, `docs`, `chore`, `test`, `ci`, `style`, `refactor`, `perf` and
`revert`. A breaking change takes a `!`, as in `feat!:`, and the body explains what breaks.

Do not edit `CHANGELOG.md` or a version number in an ordinary pull request. release-please owns
both and keeps its own release pull request open. `docs/releasing.md` describes that flow,
including the one step that needs a person.

## When a change needs an ADR

`docs/adr/` holds decisions, not changes. Most pull requests need nothing there. Write an ADR
when one of these is true:

- The change establishes a rule that later code has to keep. ADR 0005 is one, the manifest is
  unexcludable and a pack without one fails.
- You rejected an obvious alternative for reasons the diff cannot show. ADR 0009 is one, 4.0.0
  invalidates pre-4.0.0 state rather than rewriting it, and most of the document is why the
  in-place migration was not built.
- You are accepting a risk rather than removing it. ADR 0008 is one.

The filename is the next number and the title in kebab case. The title itself is the decision
written as a declarative sentence, not a topic, so a reader scanning `ls docs/adr` learns what
was decided without opening anything. Commit it as `docs(adr): record that ...`.

A fix anyone would have made the same way needs no ADR.

## Evidence

`docs/agents/verification-discipline.md` is the standard this repository holds claims to, and
every rule in it was earned by a wrong claim that reached a commit. Two of them matter for
almost every pull request.

A comment is a reviewable assertion. Comments here carry load, and a reader relies on them the
way they rely on the code, so if a comment says X happens, run X. Write down what you actually
established. "Read from the source, not executed" is the honest form when the claim covers a
platform you cannot run.

A test must go red for the reason you think. A new test passing proves nothing on its own.
Remove the fix, watch the test fail, and read the failure. Tests have passed here while never
reaching the code path, while held up by an unrelated barrier, and while the package did not
compile at all.

## What CI runs

Seven required checks on every pull request.

`ci.yaml` gives five. Test, Test (Windows) and Test (macOS) each run `go test -v -race` over
`./internal/... ./pkg/... ./cmd/...` and then over `./tests/...`. Lint runs golangci-lint
v2.12.2. Build runs `make release` and uploads the binaries, and it waits on the other four.

`pr-title.yaml` gives the sixth, Conventional Commit.

`security-versions.yaml` gives the seventh, Check supported versions. It compares `SECURITY.md`'s
supported-versions table against the version being released, and fails when a major release has
left the table behind. It runs on every pull request, not only the release one, and passes silently
when the table already matches.

The Test job also runs `make test-changelog-section` before it sets Go up at all. That target
drives the shell script the release workflow uses to cut release notes out of `CHANGELOG.md`.
It is not a Go test, so `go test ./...` does not cover it.

The whole matrix runs on documentation-only pull requests too. `ci.yaml` deliberately carries no
`paths-ignore` on `pull_request`, because these are required checks and a required check that
never reports leaves a pull request blocked forever rather than passing it. The header comment
in that file records the release pull request that proved it.


## Reporting things

Bugs and feature requests go through the issue forms under `.github/ISSUE_TEMPLATE/`. The bug
form asks for `lnpm doctor` output, which is the command that exists to answer most of the
questions a triager would otherwise have to ask.

Security reports do not go in the issue tracker. `SECURITY.md` says where they go.
