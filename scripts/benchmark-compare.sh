#!/bin/bash
# Benchmark comparison: lnpm vs yalc vs relative-deps
# Run from project root: ./scripts/benchmark-compare.sh

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}  lnpm Benchmark Comparison${NC}"
echo -e "${BLUE}========================================${NC}"
echo

# Create test package with 100 files
TMPDIR=$(mktemp -d)
PKG_DIR="$TMPDIR/test-pkg"
PROJECT_DIR="$TMPDIR/test-project"

mkdir -p "$PKG_DIR"
mkdir -p "$PROJECT_DIR"

# Create package
echo '{"name":"bench-pkg","version":"1.0.0"}' > "$PKG_DIR/package.json"
for i in $(seq 1 100); do
    echo "module.exports = { id: $i };" > "$PKG_DIR/file-$i.js"
done

# Create project
echo '{"name":"bench-project","version":"1.0.0"}' > "$PROJECT_DIR/package.json"

echo -e "Test package: ${GREEN}100 files${NC}"
echo -e "Temp directory: $TMPDIR"
echo

cleanup() {
    rm -rf "$TMPDIR"
}
trap cleanup EXIT

# Benchmark function - sets BENCH_RESULT global
benchmark() {
    local name="$1"
    local cmd="$2"
    local iterations=5
    local total=0

    echo -e "${YELLOW}Benchmarking: $name${NC}"

    for i in $(seq 1 $iterations); do
        start=$(gdate +%s%N 2>/dev/null || date +%s%N)
        eval "$cmd" > /dev/null 2>&1
        end=$(gdate +%s%N 2>/dev/null || date +%s%N)
        duration=$(( (end - start) / 1000000 ))
        total=$((total + duration))
        echo "  Run $i: ${duration}ms"
    done

    BENCH_RESULT=$((total / iterations))
    echo -e "${GREEN}  Average: ${BENCH_RESULT}ms${NC}"
    echo
}

# Get script directory and project root
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LNPM_BIN="$PROJECT_ROOT/lnpm"

# Build lnpm if needed
if [ ! -f "$LNPM_BIN" ]; then
    echo -e "${YELLOW}Building lnpm...${NC}"
    (cd "$PROJECT_ROOT" && go build -o lnpm ./cmd/lnpm)
fi

# lnpm publish
echo -e "${BLUE}--- PUBLISH ---${NC}"
export LNPM_STORE="$TMPDIR/lnpm-store"
mkdir -p "$LNPM_STORE"

cd "$PKG_DIR"
benchmark "lnpm publish" "$LNPM_BIN publish --skip-hooks --skip-validation"
LNPM_PUBLISH=$BENCH_RESULT

# yalc publish (if available)
if command -v yalc &> /dev/null; then
    benchmark "yalc publish" "yalc publish --no-scripts"
    YALC_PUBLISH=$BENCH_RESULT
else
    echo -e "${RED}yalc not found, skipping${NC}"
    echo
fi

# lnpm add
echo -e "${BLUE}--- ADD ---${NC}"
cd "$PROJECT_DIR"
benchmark "lnpm add" "$LNPM_BIN add bench-pkg"
LNPM_ADD=$BENCH_RESULT

# Clean and test yalc add
rm -rf "$PROJECT_DIR/node_modules" "$PROJECT_DIR/.lnpm" "$PROJECT_DIR/lnpm.lock"
if command -v yalc &> /dev/null; then
    benchmark "yalc add" "yalc add bench-pkg"
    YALC_ADD=$BENCH_RESULT
fi

# lnpm push
echo -e "${BLUE}--- PUSH ---${NC}"
cd "$PKG_DIR"

# Re-publish with lnpm and link
rm -rf "$PROJECT_DIR/node_modules" "$PROJECT_DIR/.lnpm" "$PROJECT_DIR/lnpm.lock"
$LNPM_BIN publish --skip-hooks --skip-validation > /dev/null 2>&1
cd "$PROJECT_DIR"
$LNPM_BIN add bench-pkg > /dev/null 2>&1

cd "$PKG_DIR"
echo "module.exports = 'updated';" > file-1.js
benchmark "lnpm push" "$LNPM_BIN push --skip-hooks"
LNPM_PUSH=$BENCH_RESULT

# yalc push (if available)
if command -v yalc &> /dev/null; then
    cd "$PKG_DIR"
    echo "module.exports = 'yalc-updated';" > file-1.js
    benchmark "yalc push" "yalc push --no-scripts"
    YALC_PUSH=$BENCH_RESULT
fi

# Summary
echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}  Summary (lower is better)${NC}"
echo -e "${BLUE}========================================${NC}"
echo
echo -e "Tool          Publish    Add        Push"
echo -e "----          -------    ---        ----"
echo -e "lnpm          ${LNPM_PUBLISH:-N/A}ms       ${LNPM_ADD:-N/A}ms      ${LNPM_PUSH:-N/A}ms"
if command -v yalc &> /dev/null; then
    echo -e "yalc          ${YALC_PUBLISH:-N/A}ms       ${YALC_ADD:-N/A}ms      ${YALC_PUSH:-N/A}ms"
fi
echo
