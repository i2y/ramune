#!/usr/bin/env bash
# Simulates a downstream consumer depending on Ramune via go.mod.
# A local-path replace points at this working tree, so Go resolves
# Ramune's source from here but the downstream's own go.mod is
# authoritative for dependency resolution — any `replace` directives
# inside Ramune's go.mod are IGNORED, exactly matching how real
# downstream builds see Ramune via `go get github.com/i2y/ramune@vX.Y.Z`.
#
# This catches replace-leak bugs like v0.13.1, where
# `replace github.com/fastschema/qjs => ./third_party/qjs` was invisible
# to downstream consumers and qjswasm builds failed with
# `opt.DisableFS undefined`.
set -euo pipefail

here=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${RUNNER_TEMP:-/tmp}/ramune-downstream-XXXXXX")
trap 'rm -rf "$tmp"' EXIT

cat > "$tmp/go.mod" <<EOF
module downstream-repro

go 1.26

require github.com/i2y/ramune v0.0.0

replace github.com/i2y/ramune => $here
EOF

cat > "$tmp/main.go" <<'EOF'
package main

import "github.com/i2y/ramune"

func main() {
	rt, err := ramune.New()
	if err != nil {
		panic(err)
	}
	rt.Close()
}
EOF

cd "$tmp"
echo "--- downstream simulation in $tmp (ramune=$here) ---"
go mod tidy

for tags in "" "qjswasm" "goja"; do
	if [ -z "$tags" ]; then
		echo "--- go build (default JSC backend) ---"
		go build ./...
	else
		echo "--- go build -tags $tags ---"
		go build "-tags=$tags" ./...
	fi
done

echo "--- downstream build OK for all backends"
