#!/usr/bin/env bash
#
# Build quickjs.wasm for the qjswasm backend.
# Invoked by `make build-wasm-shim`.
#
# Requirements:
#   - wasi-sdk 27 installed at /opt/wasi-sdk or at $WASI_SDK_PATH
#   - QuickJS-NG submodule populated
#
# See third_party/ramune-qjs-shim/README.md for the full install guide.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SHIM_DIR="$ROOT/third_party/ramune-qjs-shim"
QJS_DIR="$ROOT/third_party/quickjs-ng"

echo "==> Building qjswasm shim"
echo "    root:  $ROOT"
echo "    shim:  $SHIM_DIR"
echo "    qjs:   $QJS_DIR"
echo "    sdk:   ${WASI_SDK_PATH:-/opt/wasi-sdk}"

if [ ! -f "$QJS_DIR/quickjs.c" ]; then
    echo "ERROR: QuickJS-NG submodule missing or not initialized."
    echo "       Run: git submodule update --init --depth 1 third_party/quickjs-ng"
    exit 1
fi

make -C "$SHIM_DIR" ${WASI_SDK_PATH:+WASI_SDK_PATH="$WASI_SDK_PATH"} quickjs.wasm

echo ""
echo "==> Done. Built: $SHIM_DIR/quickjs.wasm"
ls -la "$SHIM_DIR/quickjs.wasm"
