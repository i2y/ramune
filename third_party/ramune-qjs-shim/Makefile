# Build QuickJS-NG + ramune_shim.c to quickjs.wasm (wasm32-wasi).
#
# Inputs:
#   WASI_SDK_PATH - wasi-sdk install location. Default: /opt/wasi-sdk.
#                   Override from the parent Makefile if installed elsewhere
#                   (e.g. $HOME/wasi-sdk-27.0). Pinned to major version 27+.
# Output:
#   quickjs.wasm (committed) - consumed by //go:embed in engine_qjswasm.go.
#
# The shim uses NaN-boxed JSValues so every value fits in one i64 across
# the wasm boundary. That means no slab/handle table in the shim; the
# JSValue *is* the handle.

WASI_SDK_PATH ?= /opt/wasi-sdk
# The GitHub release tag is `wasi-sdk-<MAJOR>` (e.g., wasi-sdk-27) while
# individual asset filenames carry the full `<MAJOR>.<MINOR>` string (e.g.,
# wasi-sdk-27.0-arm64-macos.tar.gz). Keep both in sync.
WASI_SDK_MAJOR ?= 27
WASI_SDK_VERSION ?= 27.0

CC := $(WASI_SDK_PATH)/bin/clang
SYSROOT := $(WASI_SDK_PATH)/share/wasi-sysroot

QJS_DIR := ../quickjs-ng
SHIM_DIR := .

# Minimum QuickJS-NG sources needed for eval + regex + unicode + JSON.
# Matches `qjs_sources` in third_party/quickjs-ng/CMakeLists.txt. Note
# that cutils is header-only in QuickJS-NG (inline definitions in
# cutils.h) — there is no cutils.c to compile.
QJS_SRCS := \
    $(QJS_DIR)/quickjs.c \
    $(QJS_DIR)/libregexp.c \
    $(QJS_DIR)/libunicode.c \
    $(QJS_DIR)/dtoa.c

SHIM_SRCS := $(SHIM_DIR)/ramune_shim.c

CFLAGS := --target=wasm32-wasi \
          --sysroot=$(SYSROOT) \
          -I$(QJS_DIR) \
          -I$(SHIM_DIR) \
          -D_WASI_EMULATED_PROCESS_CLOCKS \
          -D_WASI_EMULATED_SIGNAL \
          -D_GNU_SOURCE \
          -DJS_NAN_BOXING=1 \
          -DQUICKJS_NG_BUILD \
          -Dqjs_VERSION=\"ramune-$(WASI_SDK_VERSION)\" \
          -O2 -flto \
          -fno-exceptions \
          -fvisibility=hidden \
          -Wno-unknown-attributes \
          -Wno-sign-compare \
          -Wno-implicit-int-float-conversion \
          -std=c11

# Reactor module (no _start): exports functions for wazero to call.
LDFLAGS := -Wl,--no-entry \
           -Wl,--export-dynamic \
           -Wl,--initial-memory=8388608 \
           -Wl,--max-memory=268435456 \
           -Wl,--stack-first \
           -Wl,-z,stack-size=1048576 \
           -Wl,--allow-undefined \
           -lwasi-emulated-process-clocks \
           -lwasi-emulated-signal \
           -mexec-model=reactor

.PHONY: all clean check-sdk
all: quickjs.wasm

check-sdk:
	@if [ ! -x "$(CC)" ]; then \
	    echo "ERROR: wasi-sdk not found at $(WASI_SDK_PATH)"; \
	    echo ""; \
	    echo "Install wasi-sdk $(WASI_SDK_VERSION) from:"; \
	    echo "  https://github.com/WebAssembly/wasi-sdk/releases"; \
	    echo ""; \
	    echo "Typical macOS install (arm64) — no sudo:"; \
	    echo "  curl -L https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-$(WASI_SDK_MAJOR)/wasi-sdk-$(WASI_SDK_VERSION)-arm64-macos.tar.gz | tar xz -C \$$HOME"; \
	    echo "  export WASI_SDK_PATH=\$$HOME/wasi-sdk-$(WASI_SDK_VERSION)-arm64-macos"; \
	    echo ""; \
	    echo "Note: the tarball extracts to wasi-sdk-$(WASI_SDK_VERSION)-arm64-macos/,"; \
	    echo "not wasi-sdk-$(WASI_SDK_VERSION)/. Point WASI_SDK_PATH at the full name."; \
	    exit 1; \
	fi

quickjs.wasm: check-sdk $(QJS_SRCS) $(SHIM_SRCS) $(SHIM_DIR)/ramune_shim.h
	$(CC) $(CFLAGS) $(LDFLAGS) -o $@ $(QJS_SRCS) $(SHIM_SRCS)
	@echo "Built $@ ($$(stat -f%z $@ 2>/dev/null || stat -c%s $@) bytes)"

clean:
	rm -f quickjs.wasm
