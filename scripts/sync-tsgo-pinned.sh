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

# Packages the rslint shim links into: the exact transitive closure of the
# shim packages' tsgo imports, computed with
#   go list -deps <shim import roots>  (run inside third_party/rslint/typescript-go)
# Recompute after moving the rslint pin.
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
    diagnosticwriter
    evaluator
    format
    glob
    jsnum
    json
    jsonrpc
    locale
    ls
    ls/autoimport
    ls/change
    ls/lsconv
    ls/lsutil
    lsp/lsproto
    module
    modulespecifiers
    nodebuilder
    outputpaths
    packagejson
    parser
    printer
    project
    project/ata
    project/background
    project/dirty
    project/logging
    pseudochecker
    scanner
    semver
    sourcemap
    stringutil
    symlinks
    tracing
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
    vfs/vfsmatch
    vfs/wrapvfs
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

# Rewrite imports: microsoft/typescript-go/internal/ -> i2y/ramune/internal/rslint/tsgo_pinned/
# No formatcodeoptions patch here: unlike the primary tree, this one vendors
# the full ls/lsutil and lsp/lsproto packages (the shim's project stack needs
# them), so the upstream file compiles as-is.
find "$DST" -name '*.go' -exec sed -i '' "s|\"$OLD_MODULE/|\"$NEW_MODULE/|g" {} +

cp "$ROOT_DIR/third_party/rslint/typescript-go/LICENSE" "$DST/LICENSE"

echo "Synced $(find "$DST" -name '*.go' | wc -l | tr -d ' ') Go files to $DST"
