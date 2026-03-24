#!/bin/bash
set -euo pipefail

# Sync typescript-go internal packages for ramune fmt/check.
# Copies minimal set of packages from third_party/typescript-go/internal/
# into internal/tsgo/ with import path rewriting.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
SRC="$ROOT_DIR/third_party/typescript-go/internal"
DST="$ROOT_DIR/internal/tsgo"

OLD_MODULE="github.com/microsoft/typescript-go/internal"
NEW_MODULE="github.com/i2y/ramune/internal/tsgo"

# Packages to copy (format + checker/compiler + transitive deps)
PACKAGES=(
    ast
    astnav
    binder
    bundled
    checker
    collections
    compiler
    core
    debug
    diagnostics
    evaluator
    format
    glob
    json
    jsnum
    locale
    module
    modulespecifiers
    nodebuilder
    outputpaths
    packagejson
    parser
    printer
    pseudochecker
    repo
    scanner
    semver
    sourcemap
    stringutil
    symlinks
    transformers
    transformers/declarations
    transformers/estransforms
    transformers/inliners
    transformers/jsxtransforms
    transformers/moduletransforms
    transformers/tstransforms
    tsoptions
    tspath
    vfs
    vfs/cachedvfs
    vfs/internal
    vfs/osvfs
)

# lsutil files needed by format (subset of ls/lsutil/)
LSUTIL_FILES=(
    asi.go
    children.go
    completednode.go
    formatcodeoptions.go
)

echo "Syncing typescript-go packages..."

# Clean destination
rm -rf "$DST"

# Copy packages (non-test files only)
for pkg in "${PACKAGES[@]}"; do
    src_dir="$SRC/$pkg"
    dst_dir="$DST/$pkg"
    if [ ! -d "$src_dir" ]; then
        echo "WARNING: $src_dir not found, skipping"
        continue
    fi
    mkdir -p "$dst_dir"
    # Copy non-test .go files and non-.go files (embedded data, etc.)
    for f in "$src_dir"/*; do
        [ -f "$f" ] || continue
        base="$(basename "$f")"
        case "$base" in
            *_test.go) continue ;;
        esac
        cp "$f" "$dst_dir/"
    done
done

# Copy embedded data directories
for dir in diagnostics/loc bundled/libs; do
    if [ -d "$SRC/$dir" ]; then
        cp -r "$SRC/$dir" "$DST/$dir"
    fi
done

# Copy lsutil subset
mkdir -p "$DST/ls/lsutil"
for f in "${LSUTIL_FILES[@]}"; do
    src_file="$SRC/ls/lsutil/$f"
    if [ -f "$src_file" ]; then
        cp "$src_file" "$DST/ls/lsutil/"
    else
        echo "WARNING: $src_file not found"
    fi
done

# Rewrite import paths in all copied .go files
find "$DST" -name '*.go' -exec sed -i '' "s|\"$OLD_MODULE/|\"$NEW_MODULE/|g" {} +

# Patch formatcodeoptions.go: remove printer/tsoptions/lsproto deps
FMTOPTS="$DST/ls/lsutil/formatcodeoptions.go"
if [ -f "$FMTOPTS" ]; then
    python3 "$SCRIPT_DIR/patch_formatcodeoptions.py" "$FMTOPTS"
fi

# Copy LICENSE
cp "$ROOT_DIR/third_party/typescript-go/LICENSE" "$DST/LICENSE"

echo "Synced $(find "$DST" -name '*.go' | wc -l | tr -d ' ') Go files to $DST"
echo "Run 'go mod tidy' to resolve external dependencies."
