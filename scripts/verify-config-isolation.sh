#!/bin/bash
# Config isolation check: prove no test reads the machine's own lnpm config (#371)
# Run from project root: ./scripts/verify-config-isolation.sh [package-pattern]
#
# Builds a throwaway HOME holding a poisoned ~/.lnpm/config.yaml, runs the test
# suite with HOME pointed at it, and reports two things:
#
#   1. how many of the config's hooks executed, counted by the sentinel files
#      their commands write
#   2. where strace is available, how many times the poisoned config file was
#      opened, counted at the syscall
#
# Either being non-zero means a test read a config file outside a temp
# directory, and the script exits 1.
#
# Neither catches a leaked setting that changes behaviour without running a
# hook, so if the suite fails while both counts are zero the same pattern is run
# again against a HOME holding no config at all, wrapped identically. Passing
# there and failing under the poisoned config is a leak too. This is what
# carries the check on a machine with no strace, where the sentinel count is the
# only direct measurement available.
#
# strace is probed before being relied on: where it cannot attach, the script
# says so and drops to the sentinel count, rather than reporting a tracing
# failure as a leak in the tree.
#
# The poisoned HOME is a fresh mktemp directory, removed on exit. LNPM_STORE is
# cleared so that a test which fails to set it lands in the poisoned config's
# store_path, also under that directory. Nothing here touches a real store or a
# real HOME.

set -u

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

PATTERN="${1:-./...}"

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}  lnpm Config Isolation Check${NC}"
echo -e "${BLUE}========================================${NC}"
echo

# Get script directory and project root
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

POISON_HOME=$(mktemp -d)
SENTINELS="$POISON_HOME/sentinels"
mkdir -p "$POISON_HOME/.lnpm" "$SENTINELS" "$POISON_HOME/poison-store"

cleanup() {
    rm -rf "$POISON_HOME"
}
trap cleanup EXIT

# Settings a leaking test would visibly obey, plus a hook per publish phase.
# The hooks only touch a file: the point is to detect execution, not to do
# anything with it.
cat > "$POISON_HOME/.lnpm/config.yaml" <<YAML
store_path: $POISON_HOME/poison-store
link_mode: copy
manage_gitignore: false
follow_symlinked_node_modules: true
hooks:
  pre_publish: "touch $SENTINELS/pre_publish"
  post_publish: "touch $SENTINELS/post_publish"
  post_add: "touch $SENTINELS/post_add"
  skip_prepare: true
YAML

echo -e "Poisoned HOME:  ${GREEN}$POISON_HOME${NC}"
echo -e "Test pattern:   ${GREEN}$PATTERN${NC}"
echo
echo -e "${YELLOW}--- planted config ---${NC}"
cat "$POISON_HOME/.lnpm/config.yaml"
echo

# The go toolchain keeps its caches under HOME, so poisoning HOME without
# pinning these turns the run into a full re-download and rebuild.
GOPATH_REAL=$(go env GOPATH)
GOMODCACHE_REAL=$(go env GOMODCACHE)
GOCACHE_REAL=$(go env GOCACHE)

STRACE_HITS="$POISON_HOME/strace-hits.txt"
: > "$STRACE_HITS"

echo -e "${YELLOW}--- running the suite under the poisoned HOME ---${NC}"
cd "$PROJECT_ROOT"

# umask 022 matches how the suite is run elsewhere; several tests assert on
# created file modes. The HOME to use is the first argument, so that the control
# run below differs from the poisoned one in nothing but the config file.
run_suite() {
    local home="$1"
    shift
    (
        umask 022
        env HOME="$home" \
            LNPM_STORE= \
            GOPATH="$GOPATH_REAL" \
            GOMODCACHE="$GOMODCACHE_REAL" \
            GOCACHE="$GOCACHE_REAL" \
            "$@"
    )
}

# strace being installed does not mean it can attach: ptrace is denied to
# containers without CAP_SYS_PTRACE, and kernel.yama.ptrace_scope restricts it
# on ordinary desktops. An strace that cannot attach fails the run it wraps,
# which would otherwise be reported below as a leak in the tree. Probe it on
# something trivial first and fall back rather than blame the wrong thing.
STRACE_AVAILABLE=0
if ! command -v strace &> /dev/null; then
    echo -e "${YELLOW}strace not found, reporting the sentinel count only${NC}"
elif strace -f -qq -e trace=openat -o /dev/null go version > /dev/null 2>&1; then
    STRACE_AVAILABLE=1
    echo -e "strace found and able to attach, counting opens of the poisoned config as well"
else
    echo -e "${YELLOW}strace found but unable to attach here, so it would fail every run${NC}"
    echo -e "${YELLOW}it wrapped. Reporting the sentinel count only.${NC}"
fi
echo

# run_pattern runs the suite for HOME=$1, sending any open of the poisoned
# config to $2. Both the poisoned run and the control run go through it, so they
# are wrapped identically: if strace slows a package past go test's timeout, or
# fails outright, it does so for both and the comparison below stays honest.
run_pattern() {
    local home="$1" hits="$2"
    if [ "$STRACE_AVAILABLE" -eq 1 ]; then
        run_suite "$home" strace -f -qq -e trace=openat,open \
            -o "|grep -F '$POISON_HOME/.lnpm/config.yaml' > $hits" \
            go test "$PATTERN" -count=1
    else
        run_suite "$home" go test "$PATTERN" -count=1
    fi
}

run_pattern "$POISON_HOME" "$STRACE_HITS"
SUITE_STATUS=$?

echo
echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}  Results${NC}"
echo -e "${BLUE}========================================${NC}"
echo

SENTINEL_COUNT=$(find "$SENTINELS" -type f | wc -l | tr -d ' ')
echo -e "Suite exit status:  $SUITE_STATUS"
echo -e "Hooks executed:     $SENTINEL_COUNT"
if [ "$SENTINEL_COUNT" -gt 0 ]; then
    find "$SENTINELS" -type f -exec basename {} \; | sed 's/^/  - /'
fi

OPEN_COUNT=0
if [ "$STRACE_AVAILABLE" -eq 1 ]; then
    OPEN_COUNT=$(wc -l < "$STRACE_HITS" | tr -d ' ')
    echo -e "Config file opens:  $OPEN_COUNT"
    if [ "$OPEN_COUNT" -gt 0 ]; then
        sed 's/^/  /' "$STRACE_HITS"
    fi
else
    echo -e "Config file opens:  ${YELLOW}not measured (no strace)${NC}"
fi
echo

if [ "$SENTINEL_COUNT" -gt 0 ] || [ "$OPEN_COUNT" -gt 0 ]; then
    echo -e "${RED}LEAK: a test read the config at \$HOME/.lnpm/config.yaml${NC}"
    echo -e "A package that reaches internal/config needs a TestMain calling"
    echo -e "testenv.Run. TestConfigIsolationCoversEveryPackage in internal/testenv"
    echo -e "names the packages that are missing one."
    exit 1
fi

if [ "$SUITE_STATUS" -eq 0 ]; then
    echo -e "${GREEN}CLEAN: suite green, no hook executed, no config read${NC}"
    exit 0
fi

# The suite failed and nothing was caught reading the file. That is not proof of
# innocence: a leaked setting changes behaviour without running a hook, and
# without strace nothing here would have seen it. skip_prepare in the planted
# config is exactly that shape. So run the same pattern again against a HOME
# holding no config at all, and let the two results say which it was.
echo -e "${YELLOW}Suite failed and no read was caught. Re-running against a clean HOME${NC}"
echo -e "${YELLOW}to tell a leaked setting apart from an ordinary failure.${NC}"
echo

CONTROL_HOME="$POISON_HOME/control-home"
mkdir -p "$CONTROL_HOME"
run_pattern "$CONTROL_HOME" "$POISON_HOME/control-hits.txt" > /dev/null 2>&1
CONTROL_STATUS=$?

echo -e "Poisoned HOME:  exit $SUITE_STATUS"
echo -e "Clean HOME:     exit $CONTROL_STATUS"
echo

if [ "$CONTROL_STATUS" -eq 0 ]; then
    echo -e "${RED}LEAK: the suite passes with an empty config and fails with a${NC}"
    echo -e "${RED}populated one, so a test is reading \$HOME/.lnpm/config.yaml${NC}"
    echo -e "${RED}and obeying what it finds.${NC}"
    echo -e "A package that reaches internal/config needs a TestMain calling"
    echo -e "testenv.Run. TestConfigIsolationCoversEveryPackage in internal/testenv"
    echo -e "names the packages that are missing one."
    exit 1
fi

echo -e "${RED}The suite fails either way, so this is an ordinary test failure${NC}"
echo -e "${RED}rather than a config leak. Run the suite normally to see it.${NC}"
exit 1
