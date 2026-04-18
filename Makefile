.PHONY: all build build-cli build-toolchain build-quickjs build-goja build-qjswasm build-wasm-shim install-wasi-sdk bootstrap-qjswasm fmt fmt-check vet test test-quickjs test-goja test-qjswasm test-wpt ci bench bench-go clean sync-tsgo sync-tsgo-pinned sync-rslint sync

all: ci

build:
	go build ./... 2>&1 | grep -v 'no non-test Go files' || true

# Build the CLI binary with JIT entitlement (macOS).
# JSC's JIT compiler requires this entitlement for full performance.
VERSION ?= 0.12.1

build-cli: build-toolchain
	go build -ldflags "-X main.version=$(VERSION)" -o ramune ./cmd/ramune
	codesign --force --sign - --entitlements entitlements.plist ramune 2>/dev/null || true

# ramune-toolchain is a separate binary containing check / fmt / lint /
# transpile / typegen / compile. Keeping its tsgo + rslint + gotranspiler
# footprint out of the main ramune CLI saves ~7-10ms of startup on every run.
# ramune dispatches to this binary via exec for those subcommands.
build-toolchain:
	go build -ldflags "-X main.version=$(VERSION)" -o ramune-toolchain ./cmd/ramune-toolchain
	codesign --force --sign - --entitlements entitlements.plist ramune-toolchain 2>/dev/null || true

build-quickjs:
	go build -tags quickjs -ldflags "-X main.version=$(VERSION)" -o ramune-qjs ./cmd/ramune

build-goja:
	go build -tags goja -ldflags "-X main.version=$(VERSION)" -o ramune-goja ./cmd/ramune

# ----------------------------------------------------------------------
# wasi-sdk / qjswasm bootstrap
# ----------------------------------------------------------------------

# Pinned wasi-sdk release. The GitHub tag is wasi-sdk-<MAJOR>; the asset
# filename includes the full VERSION (e.g. 27.0). When bumping, update both.
WASI_SDK_VERSION := 27.0
WASI_SDK_MAJOR   := 27

# Host detection for wasi-sdk asset naming. uname -s returns Darwin on
# macOS but wasi-sdk asset names use "macos"; remap here. Linux stays
# as-is, Windows is installed manually.
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)
ifeq ($(UNAME_S),Darwin)
  WASI_OS := macos
else ifeq ($(UNAME_S),Linux)
  WASI_OS := linux
else
  WASI_OS := $(shell echo $(UNAME_S) | tr '[:upper:]' '[:lower:]')
endif
ifeq ($(UNAME_M),aarch64)
  WASI_ARCH := arm64
else ifeq ($(UNAME_M),x86_64)
  WASI_ARCH := x86_64
else
  WASI_ARCH := $(UNAME_M)
endif

# The tarball extracts to this directory name (not a shortened one).
WASI_SDK_DEFAULT_DIR := $(HOME)/wasi-sdk-$(WASI_SDK_VERSION)-$(WASI_ARCH)-$(WASI_OS)
WASI_SDK_URL := https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-$(WASI_SDK_MAJOR)/wasi-sdk-$(WASI_SDK_VERSION)-$(WASI_ARCH)-$(WASI_OS).tar.gz

# Resolve which wasi-sdk to use, in preference order:
#   1. user-supplied $WASI_SDK_PATH
#   2. /opt/wasi-sdk (system-wide install)
#   3. $HOME/wasi-sdk-<ver>-<arch>-<os> (auto-installed under $HOME)
ifneq ($(WASI_SDK_PATH),)
  RESOLVED_WASI_SDK := $(WASI_SDK_PATH)
else ifneq ($(wildcard /opt/wasi-sdk/bin/clang),)
  RESOLVED_WASI_SDK := /opt/wasi-sdk
else
  RESOLVED_WASI_SDK := $(WASI_SDK_DEFAULT_DIR)
endif

# Download wasi-sdk 27 under $HOME (no sudo). Idempotent — if the clang
# binary already exists at $HOME/wasi-sdk-...-/bin/clang the rule is a no-op.
install-wasi-sdk: $(WASI_SDK_DEFAULT_DIR)/bin/clang

$(WASI_SDK_DEFAULT_DIR)/bin/clang:
	@echo "==> Downloading wasi-sdk $(WASI_SDK_VERSION) for $(WASI_ARCH)/$(UNAME_S)"
	@echo "    $(WASI_SDK_URL)"
	curl -L --fail $(WASI_SDK_URL) | tar xz -C $(HOME)
	@test -x $@ || { echo "ERROR: clang not at $@ after extract"; exit 1; }
	@echo "==> wasi-sdk installed at $(WASI_SDK_DEFAULT_DIR)"

# Build the QuickJS-NG WebAssembly binary. If no wasi-sdk is found via the
# resolution above, auto-install to $HOME first.
build-wasm-shim:
	@if [ ! -x "$(RESOLVED_WASI_SDK)/bin/clang" ]; then \
	    echo "==> No wasi-sdk at $(RESOLVED_WASI_SDK), auto-installing"; \
	    $(MAKE) install-wasi-sdk; \
	fi
	WASI_SDK_PATH="$(RESOLVED_WASI_SDK)" ./scripts/build-quickjs-wasm.sh

# One-shot: install SDK if needed, build wasm, build CLI, run the M1 tests.
bootstrap-qjswasm: build-wasm-shim build-qjswasm test-qjswasm
	@echo "==> qjswasm bootstrap complete"

# Build the qjswasm-tagged CLI. The wasm binary is embedded via //go:embed;
# if it is still the 8-byte stub, Runtime.New() errors out cleanly pointing
# users at build-wasm-shim.
build-qjswasm:
	go build -tags qjswasm -ldflags "-X main.version=$(VERSION)" -o ramune-qjsw ./cmd/ramune

fmt:
	find . -name '*.go' -not -path './third_party/*' -not -path './internal/tsgo/*' -not -path './internal/rslint/*' | xargs gofmt -w

fmt-check:
	@find . -name '*.go' -not -path './third_party/*' -not -path './internal/tsgo/*' -not -path './internal/rslint/*' | xargs gofmt -w && test -z "$$(git diff --name-only)" || (echo "gofmt produced changes:"; git diff --name-only; exit 1)

vet:
	go vet -unsafeptr=false $$(go list ./... | grep -v '/internal/tsgo/' | grep -v '/internal/rslint/')

test:
	go test -run "^TestDependencies" -count=1 -timeout 120s .
	go test -run "^TestWebSocket" -count=1 -timeout 60s .
	go test -run "^TestHTTPCreateServer" -count=1 -timeout 60s .
	go test -run "^TestWorker" -count=1 -timeout 60s .
	go test -run "^Test[^DWHP]|^TestPool|^TestPerm|^TestProcess" -count=1 -timeout 120s .

test-quickjs:
	go test -tags quickjs -run "^TestDependencies" -count=1 -timeout 120s .
	go test -tags quickjs -run "^TestHTTPCreateServer" -count=1 -timeout 60s .
	go test -tags quickjs -run "^TestWorker" -count=1 -timeout 60s .
	go test -tags quickjs -run "^Test[^DWHP]|^TestPool|^TestPerm|^TestProcess" -count=1 -timeout 120s .

test-goja:
	go test -tags goja -count=1 -timeout 120s .

test-qjswasm:
	go test -tags qjswasm -count=1 -timeout 180s .

test-verbose:
	go test -v -count=1 -timeout 120s ./...

test-wpt:
	@test -d test/wpt/resources || { echo "WPT checkout not found. Run:"; echo "  git clone --depth 1 --filter=blob:none --sparse https://github.com/web-platform-tests/wpt.git test/wpt"; echo "  cd test/wpt && git sparse-checkout set resources compression encoding url urlpattern streams WebCryptoAPI webidl webmessaging FileAPI hr-time console html/webappapis/timers html/webappapis/atob html/webappapis/microtask-queuing html/webappapis/structured-clone dom/abort dom/events fetch/api"; exit 1; }
	go test -run "^TestWPT$$" -count=1 -timeout 300s -v .

ci: fmt build vet test

bench: build-cli
	./bench/run.sh

bench-go:
	@echo "=== Go JS Runtime Comparison (Ramune vs goja vs otto) ==="
	go test -c -o /tmp/ramune_bench_test ./bench/
	codesign --force --sign - --entitlements entitlements.plist /tmp/ramune_bench_test 2>/dev/null || true
	/tmp/ramune_bench_test -test.bench=Benchmark -test.benchtime=3s -test.timeout=300s -test.v

sync-tsgo:
	git submodule update --init --depth 1 third_party/typescript-go
	./scripts/sync-tsgo.sh

# Syncs rslint's pinned tsgo submodule into internal/rslint/tsgo_pinned/.
# This tree is used only by rslint's shim go:linkname bindings, kept separate
# from internal/tsgo/ so ramune's own code can track upstream tsgo freely.
sync-tsgo-pinned:
	git submodule update --init --depth 1 third_party/rslint
	git -C third_party/rslint submodule update --init --depth 1 typescript-go
	./scripts/sync-tsgo-pinned.sh

sync-rslint:
	git submodule update --init --depth 1 third_party/rslint
	./scripts/sync-rslint.sh

sync: sync-tsgo sync-tsgo-pinned sync-rslint
	go mod tidy

clean:
	go clean -cache -testcache
