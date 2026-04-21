.PHONY: all build build-cli build-toolchain build-goja build-qjswasm fmt fmt-check vet test test-goja test-qjswasm test-qjswasm-ci test-downstream test-wpt ci bench bench-go clean sync-tsgo sync-tsgo-pinned sync-rslint sync

all: ci

build:
	go build ./... 2>&1 | grep -v 'no non-test Go files' || true

# Build the CLI binary with JIT entitlement (macOS).
# JSC's JIT compiler requires this entitlement for full performance.
VERSION ?= 0.15.0

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

build-goja:
	go build -tags goja -ldflags "-X main.version=$(VERSION)" -o ramune-goja ./cmd/ramune

# qjswasm backend wraps fastschema/qjs (QuickJS-NG compiled to WebAssembly,
# driven by wazero). The wasm binary ships inside the fastschema/qjs Go
# module so no wasi-sdk or submodule-based build is needed here.
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

test-goja:
	go test -tags goja -count=1 -timeout 120s .

test-qjswasm:
	go test -tags qjswasm -count=1 -timeout 180s .

# Partitioned qjswasm tests mirroring the JSC Linux split. Running the
# full qjswasm test binary in one shot is prone to state-pollution hangs
# (see project notes); the same partitioning that JSC uses keeps qjswasm
# test runs bounded in CI.
test-qjswasm-ci:
	go test -tags qjswasm -run "^TestDependencies" -count=1 -timeout 180s .
	go test -tags qjswasm -run "^TestWebSocket" -count=1 -timeout 120s .
	go test -tags qjswasm -run "^TestHTTPCreateServer" -count=1 -timeout 120s .
	go test -tags qjswasm -run "^TestWorker" -count=1 -timeout 120s .
	go test -tags qjswasm -run "^Test[^DWHP]|^TestPool|^TestPerm|^TestProcess" -count=1 -timeout 240s .

# Simulates a downstream consumer resolving Ramune through go.mod.
# Uses a local-path replace so the working tree is picked up, but the
# downstream's own go.mod is authoritative — Ramune's internal replace
# directives are ignored, exactly matching how real consumers see us.
# Catches v0.13.1-style leaks where a replace directive in Ramune's
# own go.mod silently hides a broken public API.
test-downstream:
	@./scripts/test-downstream.sh

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
