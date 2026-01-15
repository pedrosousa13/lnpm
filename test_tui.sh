#!/bin/bash
set -x

echo "=== Testing TUI with real data ==="
cd /Users/pedrosousa/Documents/projects/lnpm

# First check what's in the database
echo "Database contents:"
timeout 3 ./lnpm debug db 2>&1 || true
echo ""

# Now try the manage command (should use TUI if terminal is interactive, or status fallback)
echo "Running manage command:"
timeout 3 ./lnpm manage 2>&1 || true

echo "=== End of test ==="
