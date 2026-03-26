.PHONY: all build build-cli build-quickjs fmt fmt-check vet test test-quickjs ci bench bench-go clean sync-tsgo sync-rslint sync

all: ci

build:
	go build ./... 2>&1 | grep -v 'no non-test Go files' || true

# Build the CLI binary with JIT entitlement (macOS).
# JSC's JIT compiler requires this entitlement for full performance.
VERSION ?= 0.3.2

build-cli:
	go build -ldflags "-X main.version=$(VERSION)" -o ramune ./cmd/ramune
	codesign --force --sign - --entitlements entitlements.plist ramune 2>/dev/null || true

build-quickjs:
	go build -tags quickjs -ldflags "-X main.version=$(VERSION)" -o ramune-qjs ./cmd/ramune

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

test-verbose:
	go test -v -count=1 -timeout 120s ./...

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

sync-rslint:
	git submodule update --init --depth 1 third_party/rslint
	./scripts/sync-rslint.sh

sync: sync-tsgo sync-rslint
	go mod tidy

clean:
	go clean -cache -testcache
