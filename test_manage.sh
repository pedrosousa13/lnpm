#!/bin/bash

echo "Testing lnpm manage command with logging..."
echo ""
echo "Starting manage command (timeout 3 seconds)..."
timeout 3 ./lnpm manage 2>&1 | tee /tmp/lnpm_manage_log.txt || true

echo ""
echo "=== Log Output ==="
grep "\[TUI\]" /tmp/lnpm_manage_log.txt || echo "No TUI logs captured"
