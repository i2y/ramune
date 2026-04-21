#!/bin/sh
# Build and run the hybrid-extraction bench on both JSC and qjswasm backends.
# Requires `ramune` (or `ramune-toolchain`) installed and on PATH.
set -e

cd "$(dirname "$0")"

TOOLCHAIN="${RAMUNE_TOOLCHAIN:-ramune-toolchain}"
if ! command -v "$TOOLCHAIN" >/dev/null 2>&1; then
  # Fall back to `go run` when the toolchain binary isn't installed.
  TOOLCHAIN="go run github.com/i2y/ramune/cmd/ramune-toolchain"
fi

echo "=== Building 4 binaries (JSC / qjswasm × JS-only / hybrid) ==="
echo "--- Picker report (for the JSC hybrid build; same for qjswasm) ---"
$TOOLCHAIN compile                         -o bench-js         app.ts
$TOOLCHAIN compile --hybrid --hybrid-report -o bench-hybrid    app.ts
$TOOLCHAIN compile --tags qjswasm          -o bench-js-qjs     app.ts
$TOOLCHAIN compile --tags qjswasm --hybrid -o bench-hybrid-qjs app.ts
echo ""

echo "### JSC + JIT (default) ###"
echo "--- JS-only ---"
./bench-js
echo ""
echo "--- Hybrid ---"
./bench-hybrid
echo ""
echo "### qjswasm (pure-Go, no JIT) ###"
echo "--- JS-only ---"
./bench-js-qjs
echo ""
echo "--- Hybrid ---"
./bench-hybrid-qjs
