#!/bin/bash
# Benchmark: Ramune vs Bun vs Node.js
# Prerequisites: hyperfine, wrk, node, bun
#
# Usage:
#   make bench          # from project root
#   ./bench/run.sh      # directly

cd "$(dirname "$0")"

RAMUNE="../ramune"
HYBRID_FIB="./fib_hybrid"
JSC_FIB="./fib_jsc"

# Build if needed.
if [ ! -f "$RAMUNE" ]; then
    echo "Building ramune..."
    cd .. && make build-cli && cd bench
fi

# Pre-build the four binaries needed for the CPU and startup-decomp
# sections: {fib, hello} × {jsc, hybrid}. Skip a build when the binary
# is fresher than its source so repeat runs don't pay the full toolchain
# cost (~10-30s × 4 = ~minute uncached).
HYBRID_HELLO="./hello_hybrid"
JSC_HELLO="./hello_jsc"
echo "Building hybrid-AOT and JSC binaries (fib + hello for startup baseline)..."
build_if_stale() {
    local flags="$1" out="$2" src="$3"
    [ -f "$out" ] && [ "$out" -nt "$src" ] && return 0
    "$RAMUNE" compile $flags -o "$out" "$src" >/dev/null 2>&1
}
build_if_stale ""        "$JSC_FIB"      fib.ts   || JSC_FIB=""
build_if_stale "--hybrid" "$HYBRID_FIB"  fib.ts   || HYBRID_FIB=""
build_if_stale ""        "$JSC_HELLO"    hello.ts || JSC_HELLO=""
build_if_stale "--hybrid" "$HYBRID_HELLO" hello.ts || HYBRID_HELLO=""

# Check dependencies.
for cmd in hyperfine node bun; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "Missing: $cmd (install to run full benchmark)"
    fi
done

echo "================================================"
echo " Ramune Benchmark Suite"
echo " $(date)"
echo " $(uname -m) / $(sw_vers -productVersion 2>/dev/null || uname -r)"
echo "================================================"
echo ""

# --- CLI benchmarks ---
echo "=== 1. Hello World (startup) ==="
hyperfine --warmup 2 --min-runs 10 \
    "$RAMUNE run hello.js" \
    "node hello.js" \
    "bun run hello.js" \
    2>&1 | grep -E "Time|Summary|times"

echo ""
echo "=== 1b. Compiled-binary startup (pure runtime cost, subtract from CPU section for compute-only) ==="
startup_cmds=()
[ -f "$JSC_HELLO" ]    && startup_cmds+=("$JSC_HELLO")
[ -f "$HYBRID_HELLO" ] && startup_cmds+=("$HYBRID_HELLO")
[ ${#startup_cmds[@]} -gt 0 ] && hyperfine --warmup 3 --min-runs 30 "${startup_cmds[@]}" 2>&1 | grep -E "Time|Summary|times"

echo ""
echo "=== 2. Fibonacci(40) (CPU) ==="
fib_cmds=("$RAMUNE run fib.js" "node fib.js" "bun run fib.js")
[ -f "$JSC_FIB" ]    && fib_cmds+=("$JSC_FIB")
[ -f "$HYBRID_FIB" ] && fib_cmds+=("$HYBRID_FIB")
hyperfine --warmup 1 --min-runs 5 "${fib_cmds[@]}" 2>&1 | grep -E "Time|Summary|times"

echo ""
echo "=== 3. JSON 10K objects ==="
hyperfine --warmup 2 --min-runs 5 \
    "$RAMUNE run json.js" \
    "node json.js" \
    "bun run json.js" \
    2>&1 | grep -E "Time|Summary|times"

echo ""
echo "=== 4. Crypto SHA256 x1000 ==="
hyperfine --warmup 1 --min-runs 5 \
    "$RAMUNE run crypto.js" \
    "node crypto.js" \
    "bun run crypto.js" \
    2>&1 | grep -E "Time|Summary|times"

echo ""
echo "=== 5. File I/O x100 ==="
hyperfine --warmup 1 --min-runs 5 \
    "$RAMUNE run fileio.js" \
    "node fileio.js" \
    "bun run fileio.js" \
    2>&1 | grep -E "Time|Summary|times"

# --- HTTP benchmarks ---
if command -v wrk &>/dev/null; then
    echo ""
    echo "=== 6. HTTP Server (50 connections, 10s) ==="

    # Kill any leftover servers and free ports.
    lsof -ti:3001,3002,3003 2>/dev/null | xargs kill 2>/dev/null || true
    sleep 2

    # Helper: wait for port to be ready.
    wait_port() {
        for i in $(seq 1 20); do
            if curl -s -o /dev/null "http://localhost:$1/" 2>/dev/null; then return 0; fi
            sleep 0.25
        done
        echo "  (port $1 not ready)"
        return 1
    }

    echo ""
    echo "--- Ramune ---"
    $RAMUNE run http_ramune.js &
    RAMUNE_PID=$!
    wait_port 3001
    wrk -t2 -c50 -d10s http://localhost:3001/ 2>&1 | grep "Requests/sec" || true
    kill $RAMUNE_PID 2>/dev/null; wait $RAMUNE_PID 2>/dev/null || true
    sleep 2

    echo "--- Node.js ---"
    node http_node.js &
    NODE_PID=$!
    wait_port 3002
    wrk -t2 -c50 -d10s http://localhost:3002/ 2>&1 | grep "Requests/sec" || true
    kill $NODE_PID 2>/dev/null; wait $NODE_PID 2>/dev/null || true
    sleep 2

    echo "--- Bun ---"
    bun run http_bun.js &
    BUN_PID=$!
    wait_port 3003
    wrk -t2 -c50 -d10s http://localhost:3003/ 2>&1 | grep "Requests/sec" || true
    kill $BUN_PID 2>/dev/null; wait $BUN_PID 2>/dev/null || true
else
    echo ""
    echo "(Skipping HTTP benchmark: wrk not installed)"
fi

echo ""
echo "================================================"
echo " Done"
echo "================================================"
