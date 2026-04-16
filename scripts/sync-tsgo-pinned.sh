#!/bin/bash
set -euo pipefail

# Sync rslint's pinned typescript-go submodule into internal/rslint/tsgo_pinned/.
# This is a *separate* copy of tsgo used only by the rslint shim's go:linkname
# bindings and rslint's transitive tsgo types. The main internal/tsgo/ tree
# (synced by sync-tsgo.sh) tracks tsgo upstream independently for use by
# ramune's own code (gotranspiler, cmd/ramune).

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
SRC="$ROOT_DIR/third_party/rslint/typescript-go/internal"
DST="$ROOT_DIR/internal/rslint/tsgo_pinned"

OLD_MODULE="github.com/microsoft/typescript-go/internal"
NEW_MODULE="github.com/i2y/ramune/internal/rslint/tsgo_pinned"

# Packages the rslint shim links into. Keep minimal — only what the shim needs.
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

if [ ! -d "$SRC" ]; then
    echo "ERROR: $SRC not found. Run 'git -C third_party/rslint submodule update --init --depth 1 typescript-go' first." >&2
    exit 1
fi

echo "Syncing rslint-pinned typescript-go into $DST..."

rm -rf "$DST"

for pkg in "${PACKAGES[@]}"; do
    src_dir="$SRC/$pkg"
    dst_dir="$DST/$pkg"
    if [ ! -d "$src_dir" ]; then
        echo "WARNING: $src_dir not found, skipping"
        continue
    fi
    mkdir -p "$dst_dir"
    for f in "$src_dir"/*; do
        [ -f "$f" ] || continue
        base="$(basename "$f")"
        case "$base" in
            *_test.go) continue ;;
        esac
        cp "$f" "$dst_dir/"
    done
done

# Copy embedded data directories.
for dir in diagnostics/loc bundled/libs; do
    if [ -d "$SRC/$dir" ]; then
        cp -r "$SRC/$dir" "$DST/$dir"
    fi
done

# Copy ls/lsutil subset needed by rslint (FormatCodeSettings, GetFirstToken,
# PositionIsASICandidate, etc).
LSUTIL_FILES=(
    asi.go
    children.go
    formatcodeoptions.go
)
mkdir -p "$DST/ls/lsutil"
for f in "${LSUTIL_FILES[@]}"; do
    src_file="$SRC/ls/lsutil/$f"
    if [ -f "$src_file" ]; then
        cp "$src_file" "$DST/ls/lsutil/"
    else
        echo "WARNING: $src_file not found"
    fi
done

# Rewrite imports: microsoft/typescript-go/internal/ -> i2y/ramune/internal/rslint/tsgo_pinned/
find "$DST" -name '*.go' -exec sed -i '' "s|\"$OLD_MODULE/|\"$NEW_MODULE/|g" {} +

# Patch formatcodeoptions.go to drop printer/tsoptions/lsproto deps,
# matching what sync-tsgo.sh does for the primary tsgo tree.
FMTOPTS="$DST/ls/lsutil/formatcodeoptions.go"
if [ -f "$FMTOPTS" ]; then
    python3 "$SCRIPT_DIR/patch_formatcodeoptions.py" "$FMTOPTS"
fi

cp "$ROOT_DIR/third_party/rslint/typescript-go/LICENSE" "$DST/LICENSE"

echo "Synced $(find "$DST" -name '*.go' | wc -l | tr -d ' ') Go files to $DST"
