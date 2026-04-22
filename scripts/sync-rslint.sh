#!/bin/bash
set -euo pipefail

# Sync rslint packages for ramune lint.
# Copies linter packages from third_party/rslint/internal/
# and shim packages from third_party/rslint/shim/
# into internal/rslint/ with import path rewriting.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
RSLINT_SRC="$ROOT_DIR/third_party/rslint"
DST="$ROOT_DIR/internal/rslint"

echo "Syncing rslint packages..."

# Clean only rslint-managed subdirectories. The sibling tsgo_pinned/ directory
# is managed by sync-tsgo-pinned.sh and must survive this sync.
mkdir -p "$DST"
for sub in config linter rule rules utils plugins shim LICENSE; do
    rm -rf "$DST/$sub"
done

# --- Copy rslint internal packages ---
copy_pkg() {
    local src_dir="$1"
    local dst_dir="$2"
    if [ ! -d "$src_dir" ]; then
        return
    fi
    mkdir -p "$dst_dir"
    for f in "$src_dir"/*.go; do
        [ -f "$f" ] || continue
        case "$(basename "$f")" in
            *_test.go) continue ;;
        esac
        cp "$f" "$dst_dir/"
    done
}

# Recursively copy a package and all its subdirectories
copy_pkg_recursive() {
    local src_base="$1"
    local dst_base="$2"
    local pkg="$3"
    copy_pkg "$src_base/$pkg" "$dst_base/$pkg"
    if [ -d "$src_base/$pkg" ]; then
        for entry in "$src_base/$pkg"/*/; do
            [ -d "$entry" ] || continue
            local sub="$(basename "$entry")"
            copy_pkg_recursive "$src_base" "$dst_base" "$pkg/$sub"
        done
    fi
}

# rslint internal packages needed by linter
for pkg in config linter rule rules utils; do
    copy_pkg_recursive "$RSLINT_SRC/internal" "$DST" "$pkg"
done

# rslint plugins
for pkg in plugins/import plugins/jest plugins/promise plugins/react plugins/typescript; do
    copy_pkg_recursive "$RSLINT_SRC/internal" "$DST" "$pkg"
done

# --- Copy shim packages used by linter ---
SHIM_PKGS=(
    ast
    bundled
    checker
    compiler
    core
    evaluator
    scanner
    tsoptions
    tspath
    vfs
    vfs/osvfs
)

for pkg in "${SHIM_PKGS[@]}"; do
    copy_pkg "$RSLINT_SRC/shim/$pkg" "$DST/shim/$pkg"
done

# --- Rewrite import paths ---
# 1. rslint internal: github.com/web-infra-dev/rslint/internal/ -> github.com/i2y/ramune/internal/rslint/
# 2. typescript-go shim: github.com/microsoft/typescript-go/shim/ -> github.com/i2y/ramune/internal/rslint/shim/
# 3. typescript-go internal (in shims): github.com/microsoft/typescript-go/internal/ -> github.com/i2y/ramune/internal/rslint/tsgo_pinned/
#    (rslint shim linknames must match the exact tsgo commit rslint pins; that
#     version is synced separately into tsgo_pinned/ by sync-tsgo-pinned.sh.
#     The main internal/tsgo/ tracks upstream tsgo independently and is used
#     only by ramune's own code, not by the shim.)
find "$DST" -name '*.go' -exec sed -i '' \
    -e 's|"github.com/web-infra-dev/rslint/internal/|"github.com/i2y/ramune/internal/rslint/|g' \
    -e 's|"github.com/microsoft/typescript-go/shim/|"github.com/i2y/ramune/internal/rslint/shim/|g' \
    -e 's|"github.com/microsoft/typescript-go/internal/|"github.com/i2y/ramune/internal/rslint/tsgo_pinned/|g' \
    {} +

# --- Rewrite go:linkname targets in shim packages ---
find "$DST/shim" -name '*.go' -exec sed -i '' \
    's|github.com/microsoft/typescript-go/internal/|github.com/i2y/ramune/internal/rslint/tsgo_pinned/|g' \
    {} +

# Copy LICENSE
cp "$RSLINT_SRC/LICENSE" "$DST/LICENSE"

echo "Synced $(find "$DST" -name '*.go' | wc -l | tr -d ' ') Go files to $DST"
