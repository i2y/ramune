#!/bin/bash
# Test all transpile examples: transpile → compile → run

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
# Build CLI once
go build -C "$REPO_DIR" -o "$TMPDIR/ramune" ./cmd/ramune/ 2>/dev/null
RAMUNE="$TMPDIR/ramune"
TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

PASS=0
FAIL=0

run_single() {
  local name="$1"
  local dir="$SCRIPT_DIR/$name"
  local outdir="$TMPDIR/$name"
  local ts_files=$(ls "$dir"/*.ts 2>/dev/null)
  local js_files=$(ls "$dir"/*.js 2>/dev/null)

  if [ -z "$ts_files" ] && [ -z "$js_files" ]; then
    return
  fi

  printf "  %-25s" "$name"

  # Native extension examples: use compile --native
  if [[ "$name" == native-* ]]; then
    local native_flags=""
    for tsf in $ts_files; do
      native_flags="$native_flags --native $tsf"
    done
    if $RAMUNE compile $native_flags -o "$TMPDIR/${name}_app" "$dir/app.js" 2>/dev/null; then
      if "$TMPDIR/${name}_app" > "$TMPDIR/${name}.out" 2>&1; then
        echo "✅"
        PASS=$((PASS + 1))
      else
        echo "❌ (run failed)"
        cat "$TMPDIR/${name}.out" 2>/dev/null
        FAIL=$((FAIL + 1))
      fi
    else
      echo "❌ (compile failed)"
      FAIL=$((FAIL + 1))
    fi
    return
  fi

  # Count TS files
  local count=$(echo $ts_files | wc -w | tr -d ' ')

  # Transpile
  if [ "$count" -gt 1 ]; then
    $RAMUNE transpile -o "$outdir" -module demo $ts_files 2>/dev/null
  else
    $RAMUNE transpile -o "$outdir" $ts_files 2>/dev/null
  fi

  if [ $? -ne 0 ]; then
    echo "❌ (transpile failed)"
    FAIL=$((FAIL + 1))
    return
  fi

  # Add replace directive and build
  echo "replace github.com/i2y/ramune => $REPO_DIR" >> "$outdir/go.mod"
  (cd "$outdir" && go mod tidy 2>/dev/null && go build -o "$TMPDIR/${name}_app" . 2>/dev/null)

  if [ $? -ne 0 ]; then
    echo "❌ (go build failed)"
    FAIL=$((FAIL + 1))
    return
  fi

  # Run
  if "$TMPDIR/${name}_app" > "$TMPDIR/${name}.out" 2>&1; then
    echo "✅"
    PASS=$((PASS + 1))
  else
    echo "❌ (run failed)"
    FAIL=$((FAIL + 1))
  fi
}

echo "Testing transpile examples..."
echo ""

for dir in "$SCRIPT_DIR"/transpile*/ "$SCRIPT_DIR"/native*/; do
  [ -d "$dir" ] || continue
  name=$(basename "$dir")
  run_single "$name"
done

echo ""
echo "Results: $PASS passed, $FAIL failed"

if [ $FAIL -gt 0 ]; then
  exit 1
fi
