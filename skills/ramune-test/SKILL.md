---
name: ramune-test
description: Build, test, and debug the Ramune project. Use this skill when the user wants to run tests, check builds, run CI, debug failures, or verify changes. Triggers on "run tests", "does it build", "make ci", "test ramune", "bench", or any mention of building/testing this project.
---

# ramune-test

Build, lint, and test Ramune — a JS/TS runtime and embeddable JS engine for Go.

## Project Structure

- **Root module** (`github.com/i2y/ramune`) — core JSC bindings, multi-runtime pool, CLI

## Commands

```bash
make ci            # fmt + build + vet + test (both modules)
make build-cli     # build CLI with JIT entitlement (macOS)
make bench         # benchmark suite (Ramune vs Bun vs Node)
make test          # tests only
make fmt           # format code
```

### Specific Tests

```bash
go test -v -count=1 -run "^TestName$" -timeout 30s
```

## Test Files (175 tests)

| File | Covers |
|------|--------|
| `jsc_test.go` | Eval, Exec, Values, objects, arrays, TypedArray, errors, multi-runtime, concurrent eval |
| `callback_test.go` | RegisterFunc, NodeCompat (fs, path, crypto, stream, http, zlib), dispatcher limit |
| `eventloop_test.go` | setTimeout, setInterval, EvalAsync, Promises |
| `context_test.go` | EvalWithContext, EvalAsyncWithContext, cancellation |
| `fetch_test.go` | HTTP GET/POST, headers, JSON |
| `asyncspawn_test.go` | Async child_process.spawn, stdout, exit codes |
| `asyncnet_test.go` | TCP/TLS client sockets |
| `worker_test.go` | Worker threads, isMainThread, postMessage |
| `websocket_test.go` | WebSocket echo, non-upgrade requests |
| `sqlite_test.go` | bun:sqlite CRUD, params, prepared statements |
| `pool_test.go` | RuntimePool eval, broadcast, concurrent, HTTP server |
| `module_test.go` | Plugin system: WithModule, LoadModule, Init hook |
| `bind_test.go` | Struct binding: fields, methods, setters |
| `register_test.go` | Typed registration: generics, type conversion |
| `permissions_test.go` | Sandbox deny/allow read, write, run |
| `nodecompat_fs_test.go` | fs.promises, readFile callback |
| `dns_test.go` | DNS lookup |
| `http_createserver_test.go` | http.createServer Node.js API |
| `buncompat_test.go` | Ramune.file, Ramune.write, Ramune.serve API |
| `internal/registry/semver_test.go` | Semver parsing, range matching, bestMatch |
| `bundle_test.go` | npm package bundling, caching |
| `benchmark_test.go` | Performance benchmarks |

## Notes

- Go vet uses `-unsafeptr=false` (purego requires unsafe pointer ops)
- Tests skip gracefully when JSC is unavailable
- Each test gets its own Runtime (no singleton restriction)
- Multi-runtime tests verify parallel JSC execution
- Package management tests use npm registry directly (no npm/bun CLI needed)
- Internal package `internal/registry` has its own test suite
