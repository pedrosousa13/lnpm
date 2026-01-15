#!/bin/bash

echo "=== Testing lnpm database and TUI ==="
echo ""

# Test 1: Check if database file exists
echo "1. Checking database file..."
if [ -f ~/.lnpm/lnpm.db ]; then
    echo "   ✓ Database exists"
    ls -lh ~/.lnpm/lnpm.db
else
    echo "   ✗ Database does not exist at ~/.lnpm/lnpm.db"
fi
echo ""

# Test 2: Run debug db command
echo "2. Running 'lnpm debug db'..."
timeout 5 ./lnpm debug db 2>&1 | head -30
echo ""

# Test 3: Run debug size command
echo "3. Running 'lnpm debug size'..."
timeout 5 ./lnpm debug size 2>&1
echo ""

# Test 4: Check logs for TUI data loading
echo "4. Testing TUI initialization (with timeout)..."
timeout 3 ./lnpm manage 2>&1 | head -20 || true
echo ""

echo "=== Test complete ==="
