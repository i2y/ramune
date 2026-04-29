#!/bin/bash
# Measures the picker's extraction rate on two real-world-shaped TS
# libraries: lodash_mini.ts (array / string / numeric helpers) and
# zod_lite.ts (input validators with unions and nested struct args).
#
# Output: a markdown summary of how many functions extracted vs
# skipped and the per-reason histogram. Re-running is cheap; the
# picker report comes from a single `ramune compile --hybrid-report`
# invocation per file.
#
# Usage: cd examples/hybrid-realworld && ./measure.sh
set -e
cd "$(dirname "$0")"

RAMUNE="${RAMUNE:-../../ramune}"
if [ ! -x "$RAMUNE" ]; then
    echo "ramune binary not found at $RAMUNE — run 'make build-cli' from repo root" >&2
    exit 1
fi

measure() {
    local src="$1"
    local label="$2"
    local report
    report=$("$RAMUNE" compile --hybrid --hybrid-report -o /tmp/$label.bin "$src" 2>&1 | sed -n '/^picker:/,/extracted,/p')
    local total extracted skipped
    extracted=$(printf "%s\n" "$report" | grep -c '^  extracted ' || true)
    skipped=$(printf "%s\n" "$report" | grep -c '^  skipped '   || true)
    total=$(( extracted + skipped ))
    local pct=0
    [ "$total" -gt 0 ] && pct=$(( extracted * 100 / total ))
    echo ""
    echo "## $label ($src)"
    echo ""
    echo "| Metric | Value |"
    echo "|---|---|"
    echo "| Functions / classes seen | $total |"
    echo "| Extracted | **$extracted** ($pct%) |"
    echo "| Skipped | $skipped |"
    if [ "$skipped" -gt 0 ]; then
        echo ""
        echo "### Reject reasons"
        echo ""
        echo '```'
        printf "%s\n" "$report" | grep '^  skipped ' | awk '{
            for (i=1; i<=NF; i++) {
                if ($i ~ /^\[/) { reason = $i; break }
            }
            count[reason]++
        } END {
            for (r in count) printf "  %-28s %d\n", r, count[r]
        }' | sort
        echo '```'
        echo ""
        echo "<details><summary>Per-function detail</summary>"
        echo ""
        echo '```'
        printf "%s\n" "$report" | grep '^  skipped ' | sed 's/^  skipped    /  /'
        echo '```'
        echo "</details>"
    fi
}

echo "# Real-world picker extraction rate"
echo ""
echo "Generated $(date -u +%Y-%m-%dT%H:%M:%SZ) on $(uname -m)."
echo ""
measure lodash_mini.ts lodash_mini
measure zod_lite.ts    zod_lite
