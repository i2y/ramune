#!/bin/bash
# Build JSC and qjswasm (JS-only vs hybrid) Hono servers and wrk-benchmark
# the extractable routes against the JS floor.
#
# Requires: ramune / ramune-toolchain on PATH, wrk, curl.
set -e
cd "$(dirname "$0")"

TOOLCHAIN="${RAMUNE_TOOLCHAIN:-ramune-toolchain}"
if ! command -v "$TOOLCHAIN" >/dev/null 2>&1; then
  TOOLCHAIN="go run github.com/i2y/ramune/cmd/ramune-toolchain"
fi
RAMUNE="${RAMUNE:-ramune}"
if ! command -v "$RAMUNE" >/dev/null 2>&1; then
  RAMUNE="go run github.com/i2y/ramune/cmd/ramune"
fi

if [ ! -d node_modules/hono ]; then
  echo "=== Installing hono ==="
  $RAMUNE install
fi
echo ""

echo "=== Picker report ==="
$TOOLCHAIN compile --http --hybrid --hybrid-report -o server-hybrid app.ts

echo ""
echo "=== Building remaining 3 binaries ==="
$TOOLCHAIN compile --http                         -o server-js         app.ts
$TOOLCHAIN compile --http --tags qjswasm          -o server-js-qjs     app.ts
$TOOLCHAIN compile --http --tags qjswasm --hybrid -o server-hybrid-qjs app.ts

echo ""

run_wrk() {
  local binary=$1 path=$2 dur=${3:-5s}
  ./"$binary" > /tmp/ramune-hybrid-hono.log 2>&1 &
  local spid=$!
  sleep 3
  for i in $(seq 5); do curl -s "http://localhost:3001$path" > /dev/null; done
  local res=$(wrk -t2 -c50 "-d$dur" "http://localhost:3001$path" 2>&1 | grep "Requests/sec" | awk '{print $2}')
  kill $spid 2>/dev/null || true
  wait 2>/dev/null || true
  echo "$res"
}

run_backend() {
  local label=$1 js=$2 hy=$3
  echo "### $label ###"
  printf '%-22s %-12s %-12s %-8s\n' "endpoint" "JS req/s" "Hybrid req/s" "ratio"
  echo "------------------------------------------------------------"
  for path in /fib/30 /fib/20 /primes/10000 /primes/1000 /health; do
    dur=5s
    [ "$path" = "/fib/30" ] && dur=3s
    [ "$path" = "/primes/10000" ] && dur=3s
    jv=$(run_wrk "$js" "$path" "$dur")
    hv=$(run_wrk "$hy" "$path" "$dur")
    ratio=$(awk -v j="$jv" -v h="$hv" 'BEGIN{if(j>0) printf "%.2f", h/j; else printf "N/A"}')
    printf '%-22s %-12s %-12s %-8s\n' "$path" "$jv" "$hv" "$ratio"
  done
  echo ""
}

run_backend "JSC + JIT (default)"       server-js     server-hybrid
run_backend "qjswasm (pure-Go, no JIT)" server-js-qjs server-hybrid-qjs
