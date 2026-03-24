<p align="center">
  <img src="ramune.png" alt="Ramune" width="800">
</p>

# Ramune

A JavaScript/TypeScript runtime for Go — powered by JavaScriptCore, no Cgo required. Pure Go except for JSC: type checker, formatter, linter, bundler, and all Node.js polyfills are implemented in Go with zero external tool dependencies.

Named after [Ramune](https://en.wikipedia.org/wiki/Ramune), a Japanese carbonated soft drink served in a Codd-neck bottle.

```bash
ramune run server.ts          # Run TypeScript
ramune test                   # Run tests
ramune check app.ts           # Type-check
ramune fmt .                  # Format
ramune lint .                 # Lint
ramune compile app.ts -o app  # Compile to standalone binary
```

## What is Ramune?

Ramune is two things:

1. **A JS/TS runtime** like Bun or Deno, but built in Go
2. **An embeddable JS engine** for Go applications

It loads Apple's JavaScriptCore dynamically via [purego](https://github.com/ebitengine/purego) — no C compiler, no Cgo, just `go build`.

## Install

### macOS

JavaScriptCore is built into macOS — no extra dependencies.

```bash
go install github.com/i2y/ramune/cmd/ramune@latest
ramune setup-jit   # enable JIT (~10x faster, recommended)
```

### Linux

```bash
sudo apt install libjavascriptcoregtk-4.1-dev   # JSC runtime (required)
go install github.com/i2y/ramune/cmd/ramune@latest
```

Multi-runtime (RuntimePool, worker_threads) is automatically enabled when gcc is installed. Single runtime works without gcc. To force a pure Go build: `CGO_ENABLED=0 go install ...`

### Minimal install (12MB)

```bash
go install -tags nosqlite -ldflags="-s -w" github.com/i2y/ramune/cmd/ramune@latest
```

| Build | Size | Excludes |
|-------|------|----------|
| Default | 22MB | — |
| `-tags nosqlite` | 17MB | bun:sqlite |
| `-tags nosqlite -ldflags="-s -w"` | 12MB | bun:sqlite + debug info |

## Quick Start

### Run JavaScript/TypeScript

```bash
ramune run app.ts
ramune run -p lodash -p dayjs app.ts   # with npm packages
ramune run                              # reads package.json
ramune run -w server.ts                 # watch mode
ramune run --workers 4 server.ts        # multi-worker HTTP server
```

### Evaluate Expressions

```bash
ramune eval "1 + 2"
ramune eval "require('crypto').randomUUID()"
ramune eval "const x: number = 42; x"   # TypeScript works
```

### REPL

```bash
ramune repl
```

Packages from `package.json` are automatically available:

```bash
ramune add lodash
ramune repl
> lodash.chunk([1,2,3,4,5,6], 2)
[[1,2],[3,4],[5,6]]
```

Features: history, tab completion, TypeScript, colors, multiline.

### Test Runner

```bash
ramune test
```

Finds `*.test.ts`, `*.spec.js`, etc. Jest/Bun-compatible API:

```ts
describe("math", () => {
  test("addition", () => {
    expect(1 + 2).toBe(3);
  });
});
```

### Compile to Standalone Binary

```bash
ramune compile server.ts -o myserver --http --minify
./myserver    # self-contained binary with embedded JS
```

The compiled binary embeds the bundled JS via `go:embed`. On macOS, it is automatically codesigned with the JIT entitlement.

Options: `--http` (Ramune.serve event loop), `--minify` (esbuild minification).

> **Note:** The compiled binary loads JavaScriptCore dynamically at runtime. The target machine must have JSC available (macOS: built-in, Linux: `libjavascriptcoregtk`).

### Type Checking

```bash
ramune check app.ts              # check files
ramune check src/                # check directory
ramune run --check app.ts        # check then run
```

Uses [typescript-go](https://github.com/microsoft/typescript-go) (TypeScript 7.0-dev, backward-compatible with TS 5.x) built into Ramune — no external tools required.

### Format & Lint

```bash
ramune fmt .                     # format all JS/TS files
ramune fmt --check .             # check formatting (CI)
ramune lint .                    # lint all JS/TS files
ramune lint --fix .              # lint with auto-fix
```

The formatter uses typescript-go's built-in formatter. The linter uses [rslint](https://github.com/web-infra-dev/rslint) (Go-based, 20-40x faster than ESLint). Both are built into Ramune — no external tools required.

If `rslint.json` or `rslint.jsonc` exists, `ramune lint` uses that configuration. Otherwise, all recommended rules are enabled by default.

> **Note:** TypeScript transpilation (`ramune run app.ts`) uses [esbuild](https://esbuild.github.io/) which is also built into Ramune.

### Package Manager

```bash
ramune init                      # create package.json
ramune add lodash dayjs          # add dependencies
ramune remove lodash             # remove
ramune install                   # install all
```

### Build (esbuild)

```bash
ramune build app.ts --outdir=dist --bundle --minify
```

### Permissions (Sandbox)

```bash
ramune run app.ts                              # default: all allowed
ramune run --sandbox app.ts                    # deny all
ramune run --sandbox --allow-read=/tmp app.ts  # selective access
```

Flags: `--allow-read`, `--allow-write`, `--allow-net`, `--allow-env`, `--allow-run`.

## Embed in Go

Ramune is also a Go library. Embed JavaScript in your Go application and expose any Go library to JS — database drivers, image processing, gRPC clients, ML inference, etc.

```go
package main

import (
    "fmt"
    "log"

    "github.com/i2y/ramune"
)

func main() {
    rt, err := ramune.New()
    if err != nil {
        log.Fatal(err)
    }
    defer rt.Close()

    val, _ := rt.Eval("1 + 2")
    defer val.Close()
    fmt.Println(val.Float64()) // 3
}
```

### Call Go from JavaScript

```go
rt.RegisterFunc("greet", func(args []any) (any, error) {
    return fmt.Sprintf("Hello, %s!", args[0]), nil
})

val, _ := rt.Eval(`greet("World")`) // "Hello, World!"
```

Go functions registered via `RegisterFunc` can safely access `Value` methods (Attr, Call, SetAttr, etc.) — no deadlock. For typed callbacks, use `Register` with generics:

```go
ramune.Register(rt, "add", func(a, b float64) float64 {
    return a + b
})
```

### Struct Binding

Expose Go structs to JavaScript:

```go
type User struct {
    Name string `js:"name"`
    Age  int    `js:"age"`
}
func (u *User) Greet() string { return "Hello, " + u.Name }

rt.Bind("user", &User{Name: "Alice", Age: 30})
// JS: user.name → "Alice", user.greet() → "Hello, Alice"
```

### Plugin System

Register custom modules available via `require()`:

```go
rt, _ := ramune.New(ramune.NodeCompat(), ramune.WithModule(ramune.Module{
    Name: "mydb",
    Exports: map[string]ramune.GoFunc{
        "query": func(args []any) (any, error) {
            return db.Query(args[0].(string))
        },
    },
}))
// JS: const db = require('mydb'); db.query("SELECT 1")
```

### Use npm Packages

```go
rt, _ := ramune.New(
    ramune.NodeCompat(),
    ramune.Dependencies("lodash@4"),
)
val, _ := rt.Eval(`lodash.chunk([1,2,3,4,5,6], 2)`)
```

### Async / Promises

```go
val, _ := rt.EvalAsync(`
    new Promise(resolve => setTimeout(() => resolve(42), 100))
`)
```

### HTTP Server

```go
rt, _ := ramune.New(ramune.NodeCompat())

rt.Exec(`
    Ramune.serve({
        port: 3000,
        fetch(req) {
            return new Response("Hello!");
        }
    })
`)
rt.RunEventLoop()
```

Works with [Hono](https://hono.dev/) and other frameworks. Async handlers with `setTimeout`/`await` are supported:

```js
app.get('/slow', async (c) => {
    await new Promise(r => setTimeout(r, 100));
    return c.json({ ok: true });
});
```

### Multi-core Parallelism

Unlike Bun/Node (single-threaded), Ramune runs multiple JSC VMs in parallel on separate OS threads:

```go
pool, _ := ramune.NewPool(4, ramune.NodeCompat())
defer pool.Close()

pool.Eval("computeHeavy()")                     // round-robin dispatch
pool.Broadcast("globalThis.config = {debug: true}")  // run on all

// Multi-worker HTTP server
pool.ListenAndServe(":3000", `
    globalThis.__poolHandle = function(req) {
        return { status: 200, body: "Hello from worker!" };
    };
`)
```

With CPU-heavy handlers, 3 workers achieve **2.7x throughput** vs single-threaded Bun.

Worker threads are also supported:

```js
const { Worker } = require('worker_threads');
const w = new Worker('./worker.js', { workerData: { n: 42 } });
w.on('message', msg => console.log(msg));
```

### Ramune APIs & Bun Compatibility

Ramune provides its own API namespace. `Bun.*` is available as an alias for backward compatibility with existing Bun code, though compatibility is partial and will be improved over time.

| API | Status |
|-----|--------|
| `Ramune.serve({port, fetch, websocket})` | Supported (Go net/http backend, 101K req/s) |
| `Ramune.file(path)` | Supported (text, json, exists, size) |
| `Ramune.write(path, data)` | Supported |
| `Ramune.password.hash/verify` | Supported (bcrypt) |
| `Ramune.sleep(ms)` | Supported |
| `Request` / `Response` | Polyfilled (enables Hono etc.) |
| `bun:sqlite` | Supported (pure Go, modernc.org/sqlite) |
| `Bun.*` | Alias for `Ramune.*` (partial Bun compatibility) |

### GC Configuration

Go's garbage collector can interfere with JavaScriptCore under high load. Ramune provides tunable GC settings:

```go
rt, _ := ramune.New(ramune.NodeCompat(), ramune.WithGC(ramune.GCConfig{
    DisableAutoGC: true,   // disable Go's auto GC during HTTP serving
    GCInterval:    2000,   // manual GC every N requests
    GCPercent:     100,    // Go GC target %
}))
```

For most use cases (CLI, scripting, SDK), defaults work fine. Tuning is only needed for high-throughput HTTP servers.

### Permissions (Library API)

```go
rt, _ := ramune.New(
    ramune.NodeCompat(),
    ramune.WithPermissions(&ramune.Permissions{
        Read:      ramune.PermGranted,
        ReadPaths: []string{"/tmp", "/var/data"},
        Write:     ramune.PermDenied,
        Net:       ramune.PermDenied,
    }),
)
```

## Performance

Benchmarks on Apple M4 Max (macOS, JIT enabled):

| Test | Ramune | Bun | Node.js |
|------|--------|-----|---------|
| Hello World startup | **10.5ms** | 6.2ms | 17.4ms |
| Fibonacci(35) | **42.9ms** | 39.4ms | 63.5ms |
| JSON 10K objects | **14.2ms** | 8.9ms | 22.2ms |
| Crypto SHA256 x1000 | **16.6ms** | 10.3ms | 19.6ms |
| File I/O x100 | **17.2ms** | 11.9ms | 23.0ms |
| HTTP req/s (single) | **101K** | 170K | 114K |
| HTTP req/s (3 workers) | **87 req/s** | 31 req/s | 22 req/s |

The last row shows CPU-heavy handlers (fib(35) per request) — Ramune's multi-runtime pool scales linearly while Bun/Node are single-threaded.

### vs Go JS Runtimes

| Test | Ramune (JSC+JIT) | goja | otto |
|------|-----------------|------|------|
| Fibonacci(35) | **43ms** | 1,945ms (45x slower) | 23,760ms (559x slower) |
| JSON 10K objects | **1.5ms** | 12ms (8x slower) | 26ms (17x slower) |

Ramune uses Apple's JavaScriptCore with JIT compilation. goja and otto are pure Go interpreters.

Run `make bench` to reproduce.

### JIT Setup

On macOS, JIT requires a code signing entitlement:

```bash
# After go install:
ramune setup-jit

# Or when building from source:
make build-cli
```

Linux does not need JIT setup.


## Node.js Compatibility

| Module | Coverage | | Module | Coverage |
|--------|----------|-|--------|----------|
| path | 100% | | zlib | 70% |
| fs | 85% | | os | 85% |
| child_process | 80% | | events | 85% |
| crypto | 80% | | url | 80% |
| stream | 70% | | Buffer | 60% |
| http/https | 70% | | assert | 80% |
| net/tls | 60% | | dns | basic |
| worker_threads | 70% | | readline | 70% |

## Known Limitations

- **N-API / Native addons**: Not supported. Packages that require `.node` native binaries (e.g., `bcrypt`, `sharp`, `better-sqlite3`) will not work. Use pure JS alternatives instead.
- **HTTP self-fetch**: Ramune.serve() handlers cannot fetch their own server (same JSC context deadlock).
- **Windows**: No JavaScriptCore available.
- **Linux multi-runtime**: Requires `CGO_ENABLED=1` and a C compiler. Cgo's signal forwarding is needed for JSC's GC to coexist with Go's runtime. Without cgo, only single-runtime works on Linux.
- **Multi-worker limit**: 2-3 workers recommended for sustained high-throughput; 4+ may trigger JSC JIT contention.

## Requirements

| Dependency | Required | Purpose |
|---|---|---|
| **Go 1.26+** | Yes | Build and install |
| **macOS** or **Linux** | Yes | macOS: JSC built-in. Linux: `apt install libjavascriptcoregtk-4.1-dev` |

All tools are built in — no external dependencies needed for `check`, `fmt`, `lint`, or TypeScript transpilation. npm packages are fetched directly from the npm registry — no npm or bun CLI required.

## Development

```bash
make ci          # fmt + build + vet + test
make build-cli   # build with JIT entitlement (macOS)
make bench       # benchmark vs Bun/Node
make sync        # sync typescript-go & rslint from submodules
```

## License

MIT

### Third-Party Licenses

Ramune embeds code from the following projects:

| Project | License | Usage |
|---------|---------|-------|
| [microsoft/typescript-go](https://github.com/microsoft/typescript-go) | Apache-2.0 | Type checker, formatter (TS 7.0-dev) |
| [web-infra-dev/rslint](https://github.com/web-infra-dev/rslint) | MIT | Linter |
| [evanw/esbuild](https://github.com/evanw/esbuild) | MIT | TypeScript transpilation, bundling |

Full license texts are included in `internal/tsgo/LICENSE` and `internal/rslint/LICENSE`.

The Ramune logo includes the Go Gopher, originally designed by [Renée French](https://reneefrench.blogspot.com/), licensed under [Creative Commons Attribution 4.0](https://creativecommons.org/licenses/by/4.0/).
