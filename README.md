<p align="center">
  <img src="ramune.png" alt="Ramune" width="800">
</p>

# Ramune

A JavaScript/TypeScript runtime and embeddable JS engine for Go. Dual backend: **JavaScriptCore** (JIT, macOS/Linux) via [purego](https://github.com/ebitengine/purego) and **QuickJS** (pure Go, cross-platform incl. Windows) via [modernc.org/quickjs](https://pkg.go.dev/modernc.org/quickjs) — no Cgo required for either. Type checker and formatter ([typescript-go](https://github.com/microsoft/typescript-go)), linter ([rslint](https://github.com/web-infra-dev/rslint)), bundler ([esbuild](https://github.com/evanw/esbuild)), and all Node.js polyfills are built in with zero external tool dependencies.

Named after [Ramune](https://en.wikipedia.org/wiki/Ramune), a Japanese carbonated soft drink served in a Codd-neck bottle.

```bash
ramune run server.ts          # Run TypeScript
ramune test                   # Run tests
ramune check app.ts           # Type-check
ramune fmt .                  # Format
ramune lint .                 # Lint
ramune compile app.ts -o app  # Compile to standalone binary
ramune transpile main.ts -o out  # Transpile TS to Go source
ramune typegen go:fmt go:net/http -o go.d.ts  # Generate .d.ts for Go packages
ramune skills install         # Install Agent Skills for AI agents
```

## What is Ramune?

Ramune is two things:

1. **A JS/TS runtime** like Bun or Deno, but built in Go
2. **An embeddable JS engine** for Go applications

Two backends, same API:

| | JSC (default) | QuickJS (`-tags quickjs`) |
|---|---|---|
| **Engine** | Apple JavaScriptCore via [purego](https://github.com/ebitengine/purego) | [modernc.org/quickjs](https://pkg.go.dev/modernc.org/quickjs) (pure Go) |
| **JIT** | Yes | No |
| **Platforms** | macOS, Linux | macOS, Linux, **Windows**, FreeBSD |
| **System deps** | macOS: none. Linux: libjavascriptcoregtk | None |
| **Best for** | Performance, HTTP servers | Embedding, scripting, portability |

Both are pure Go builds — no C compiler, no Cgo, just `go build`.

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

Multi-runtime (RuntimePool, worker_threads) works out of the box on x86_64. On arm64, gcc is required for cgo signal forwarding (`apt install gcc`).

### Windows / Zero-dependency (QuickJS backend)

```bash
go install -tags quickjs github.com/i2y/ramune/cmd/ramune@latest
```

The QuickJS backend uses [modernc.org/quickjs](https://pkg.go.dev/modernc.org/quickjs) (pure Go, ES2023). No shared libraries needed — works on **Windows**, macOS, Linux, and FreeBSD. Trade-off: no JIT, so CPU-bound code is slower (see [Performance](#performance)).

### Smaller binary

```bash
go install -tags nosqlite -ldflags="-s -w" github.com/i2y/ramune/cmd/ramune@latest
```

`-tags nosqlite` excludes bun:sqlite. `-ldflags="-s -w"` strips debug info. Combine with `-tags quickjs,nosqlite` for the smallest possible binary.

## Quick Start

### Run JavaScript/TypeScript

```bash
ramune run app.ts
ramune run -p lodash -p dayjs app.ts   # with npm packages
ramune run                              # reads package.json
ramune run -w server.ts                 # watch mode
ramune run --workers 4 server.ts        # multi-worker HTTP server
ramune run --env-file .env.prod app.ts  # load env file
ramune run dev                          # run package.json script
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

Mocking is supported via `jest.fn()` and `jest.spyOn()`:

```ts
test("mock", () => {
  const fn = jest.fn().mockReturnValue(42);
  expect(fn()).toBe(42);
  expect(fn).toHaveBeenCalledTimes(1);
});
```

### Compile to Standalone Binary

```bash
ramune compile server.ts -o myserver --http --minify
./myserver    # self-contained binary with embedded JS
```

The compiled binary embeds the bundled JS via `go:embed`. On macOS, it is automatically codesigned with the JIT entitlement.

Options: `--http` (Ramune.serve event loop), `--minify` (esbuild minification). Output binary is ~21MB (linter/formatter/checker are not included — only the runtime).

> **Note:** The compiled binary loads JavaScriptCore dynamically at runtime. The target machine must have JSC available (macOS: built-in, Linux: `libjavascriptcoregtk`). Use `-tags quickjs` for cross-platform builds.

### Native Extension Modules

Compile performance-critical TypeScript functions to native Go code and call them from JavaScript at full compiled speed:

```bash
ramune compile app.js --native math.ts -o myapp
```

The `--native` flag transpiles TypeScript to Go, making exported functions available via `require('native:modulename')`:

```ts
// math.ts — transpiled to Go
export function fibonacci(n: number): number {
  if (n <= 1) return n;
  return fibonacci(n - 1) + fibonacci(n - 2);
}
```

```js
// app.js — runs in JS, calls native Go code
const { fibonacci } = require('native:math');
console.log(fibonacci(35)); // runs as compiled Go, not interpreted JS
```

Multiple native files and inter-file imports are supported:

```bash
ramune compile app.js --native math.ts --native geometry.ts -o myapp
```

Native functions support rich type interop — structs, typed arrays, and class-like instances with live properties:

```ts
// counter.ts
export class Counter {
  count: number = 0;
  name: string;
  constructor(name: string) { this.name = name; }
  increment(): number { return ++this.count; }
}
export function newCounter(name: string): Counter {
  return new Counter(name);
}
```

```js
const { newCounter } = require('native:counter');
const c = newCounter("hits");
c.increment();
c.increment();
console.log(c.count); // 2 — live property, reads Go struct field
c.count = 100;        // setter, writes Go struct field
c.increment();
console.log(c.count); // 101
```

### Transpile TypeScript to Go (Experimental)

> **Note:** The TypeScript-to-Go transpiler is experimental and under active development. Generated code may require manual adjustments for complex codebases.

Transpile TypeScript source code directly to Go:

```bash
ramune transpile main.ts -o out/                     # single file
ramune transpile main.ts utils.ts -o out/ --module myapp  # multi-file project
ramune transpile main.ts --compile -o myapp           # transpile + build binary
```

The transpiler converts TypeScript types, classes, interfaces, generics, async/await, and more to idiomatic Go. See [TypeScript-to-Go Transpiler](#typescript-to-go-transpiler) for supported features and limitations.

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

### Docker Sandbox

Run scripts in isolated Docker containers:

```bash
ramune run --docker app.ts                          # run in default ubuntu:24.04
ramune run --docker --docker-image node:22 app.ts   # custom image
ramune run --docker --docker-memory 512 app.ts      # 512MB memory limit
ramune run --docker --docker-no-network app.ts      # no network access
ramune run --docker --docker-network mynet app.ts   # specific Docker network
```

The host binary is automatically mounted into the container. On macOS/Windows, a Linux binary is cross-compiled and cached. Go functions registered via `SandboxRuntime.RegisterFunc` are available inside the container (they are compiled into the binary).

### Environment Variables

`.env` and `.env.local` files are automatically loaded (like Bun/Deno). Use `--env-file` to specify a custom file:

```bash
ramune run --env-file .env.production app.ts
```

### Package.json Scripts

Run scripts defined in `package.json`:

```bash
ramune run dev     # runs "scripts.dev" from package.json
ramune run build   # runs "scripts.build"
```

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

### Receive JS Functions in Go

JS functions passed to Go callbacks are wrapped as `*JSFunc`, callable from Go:

```go
rt.RegisterFunc("forEach", func(args []any) (any, error) {
    items := args[0].([]any)
    fn := args[1].(*ramune.JSFunc)
    defer fn.Close()
    for _, item := range items {
        fn.Call(item)
    }
    return nil, nil
})
```

```js
forEach(["a", "b", "c"], function(item) { console.log(item); });
// → a, b, c
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

### Native Module (Go Library API)

`NativeModuleFromFuncs` creates a `require()`-able module from typed Go functions — no manual argument parsing needed:

```go
mod := ramune.NativeModuleFromFuncs("native:math", map[string]any{
    "add":       func(a, b float64) float64 { return a + b },
    "isPrime":   func(n float64) bool { /* ... */ },
    "fibonacci": mymath.Fibonacci,  // any typed Go function
})

rt, _ := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
rt.Eval(`require('native:math').add(3, 4)`) // 7
```

Supports struct parameters, struct returns with live properties, typed slices, error handling, and panic recovery:

```go
type Point struct {
    X float64 `json:"x"`
    Y float64 `json:"y"`
}

mod := ramune.NativeModuleFromFuncs("native:geo", map[string]any{
    "distance": func(a, b Point) float64 {
        dx, dy := a.X-b.X, a.Y-b.Y
        return math.Sqrt(dx*dx + dy*dy)
    },
})
// JS: require('native:geo').distance({x:0, y:0}, {x:3, y:4}) → 5
```

When a function returns a struct pointer, the JS object has live getter/setter properties and callable methods — mutations in JS are reflected in Go and vice versa.

### Use npm Packages

```go
rt, _ := ramune.New(
    ramune.NodeCompat(),
    ramune.Dependencies("lodash@4"),
)
val, _ := rt.Eval(`lodash.chunk([1,2,3,4,5,6], 2)`)
```

Subpath imports are supported (e.g., `"react-dom/server"`). Use `PreloadJS` to inject polyfills that bundled packages may require:

```go
rt, _ := ramune.New(
    ramune.NodeCompat(),
    ramune.PreloadJS(`globalThis.MessageChannel = class { constructor() { this.port1 = {}; this.port2 = {}; } };`),
    ramune.Dependencies("react@18", "react-dom@18", "react-dom/server"),
)
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
| `Ramune.plugin({setup})` | Supported (onLoad filters, virtual modules) |
| `Request` / `Response` | Polyfilled with ReadableStream body |
| `Ramune.build({entrypoints, outdir, ...})` | Supported (esbuild backend, minify, splitting, sourcemap) |
| `bun:sqlite` | Supported (transactions, WAL, prepared stmt cache, pure Go) |
| `Bun.*` | Alias for `Ramune.*` (partial Bun compatibility) |

### Console Output

`console.log`/`error`/`warn` work out of the box in both CLI and library mode. Output goes to `os.Stdout`/`os.Stderr` by default. Use `WithStdout`/`WithStderr` to redirect:

```go
var buf bytes.Buffer
rt, _ := ramune.New(ramune.WithStdout(&buf))
rt.Exec(`console.log("captured")`)
fmt.Println(buf.String()) // "captured\n"
```

### GC Configuration

Ramune provides tunable GC settings for high-throughput HTTP servers:

```go
rt, _ := ramune.New(ramune.NodeCompat(), ramune.WithGC(ramune.GCConfig{
    GCInterval: 2000,   // manual JSC GC every N requests
    GCPercent:  100,    // Go GC target % (GOGC)
}))
```

For most use cases (CLI, scripting, SDK), defaults work fine.

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

### Docker Sandbox (Library API)

Execute untrusted JS in Docker containers with Go function passthrough:

```go
rt := ramune.NewSandboxRuntime(ramune.NodeCompat())

// Go functions are available inside the container
rt.RegisterFunc("multiply", func(args []any) (any, error) {
    return args[0].(float64) * args[1].(float64), nil
})

// Must be called first — handles re-exec as sandbox worker
if ramune.HandleSandboxWorker(rt) {
    return
}

result, err := rt.SandboxRun("script.js", ramune.SandboxConfig{
    Image:     "ubuntu:24.04",
    MemoryMB:  512,
    NoNetwork: true,
    Timeout:   30 * time.Second,
})
fmt.Println(result.Stdout)
```

`SandboxEval` evaluates code strings instead of files. `SandboxAvailable()` checks if Docker is reachable.

### Docker API (dockerode)

Access Docker from JavaScript via the `DockerModule()` option:

```go
rt, _ := ramune.New(ramune.NodeCompat(), ramune.DockerModule())
```

```js
const Docker = require('dockerode');
const docker = new Docker();
await docker.ping();

const container = await docker.createContainer({ Image: 'alpine:latest', Cmd: ['echo', 'hello'] });
await container.start();
const { StatusCode } = await container.wait();
```

Supports: `ping`, `pull`, `createContainer`, `start/stop/remove/wait/inspect/logs`, `createNetwork/removeNetwork`.

## Performance

### JSC backend (default)

Benchmarks on Apple M4 Max (macOS, JIT enabled):

| Test | Ramune | Bun | Node.js |
|------|--------|-----|---------|
| Hello World startup | **14.2ms** | 7.1ms | 18.0ms |
| Fibonacci(35) | **46.2ms** | 40.0ms | 64.7ms |
| JSON 10K objects | **17.6ms** | 9.7ms | 22.9ms |
| Crypto SHA256 x1000 | **19.8ms** | 11.0ms | 20.4ms |
| File I/O x100 | **20.7ms** | 13.3ms | 24.2ms |
| HTTP req/s (single) | **101K** | 156K | 112K |

### QuickJS backend (`-tags quickjs`)

Same machine, no JIT:

| Test | Ramune (QuickJS) | Ramune (JSC) | Bun | Node.js |
|------|-----------------|--------------|-----|---------|
| Hello World startup | 19.0ms | **14.2ms** | 6.4ms | 17.1ms |
| Fibonacci(35) | 3,089ms | **46.2ms** | 39.3ms | 63.5ms |
| JSON 10K objects | 65.1ms | **17.6ms** | 9.0ms | 22.0ms |
| Crypto SHA256 x1000 | 26.8ms | **19.8ms** | 10.3ms | 19.5ms |
| File I/O x100 | 25.8ms | **20.7ms** | 12.4ms | 22.7ms |
| HTTP req/s (single) | 66K | **101K** | 165K | 111K |

QuickJS has no JIT compiler, so CPU-bound code (Fibonacci, JSON) is significantly slower. I/O-bound workloads (crypto, file, HTTP) are closer because the heavy lifting happens in Go. The QuickJS backend is best suited for embedding, scripting, Windows support, and environments where zero external dependencies matter more than raw JS execution speed.

### Multi-Runtime Pool

Ramune runs multiple JS VMs in parallel on separate OS threads (Bun/Node are single-threaded):

| Workers | req/s | Scaling |
|---------|-------|---------|
| 1 | 44K | 1.0x |
| 2 | 65K | 1.48x |
| 3 | 68K | 1.56x |

Measured with a JSON generate/filter/map handler (200 objects per request).

### vs Go JS Runtimes

| Test | Ramune (JSC+JIT) | Ramune (QuickJS) | goja | otto |
|------|-----------------|-----------------|------|------|
| Fibonacci(35) | **31ms** | 3,122ms | 1,989ms | 26,413ms |
| JSON 10K objects | **0.9ms** | 30ms | 11ms | 27ms |

JSC with JIT is the fastest by a wide margin. QuickJS (pure Go interpreter) is comparable to goja for JSON workloads and slower for CPU-heavy code. otto is the slowest across all tests.

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
| path | 100% | | zlib | 75% (gzip, deflate, brotli) |
| fs | 90% (async + sync + watch) | | os | 85% |
| child_process | 80% | | events | 85% |
| crypto | 85% (+ crypto.subtle) | | url | 80% |
| stream | 70% | | Buffer | 90% |
| http/https | 70% | | assert | 80% |
| net/tls | 70% (+ net.createServer) | | dns | basic |
| worker_threads | 75% (+ SharedArrayBuffer) | | readline | 70% |
| vm | 70% | | querystring | 80% |
| timers/promises | 70% | | perf_hooks | basic |
| util | 80% (types, promisify, format, debuglog) | | process | 85% (stdin, signals, exit, env, tty) |
| tty | 70% (isatty, WriteStream) | | dgram | 70% (UDP) |
| async_hooks | basic (AsyncLocalStorage) | | module | basic (createRequire) |

## Web Platform APIs

| API | Status |
|-----|--------|
| `fetch` | Supported (Go net/http backend) |
| `ReadableStream` / `WritableStream` / `TransformStream` | Supported (pipeTo, pipeThrough, tee, async iterator) |
| `crypto.subtle` | Supported (digest, sign/verify, encrypt/decrypt, importKey/exportKey, deriveBits/deriveKey) |
| `crypto.getRandomValues` / `randomUUID` | Supported |
| `Blob` / `File` | Supported |
| `FormData` | Supported |
| `Headers` / `Request` / `Response` | Supported (ReadableStream body) |
| `TextEncoder` / `TextDecoder` | Supported (UTF-8) |
| `AbortController` / `AbortSignal` | Supported |
| `EventTarget` / `Event` / `CustomEvent` | Supported (addEventListener, once, handleEvent) |
| `URL` / `URLSearchParams` | Supported |
| `WebSocket` | Supported (server-side via Ramune.serve) |
| `performance.now` / `mark` / `measure` | Supported |
| `SharedArrayBuffer` / `Atomics` | Supported (Go []byte backed, wait/notify, worker transfer) |
| `structuredClone` | Supported (circular refs, Map, Set, Date, RegExp, TypedArray) |
| `setTimeout` / `setInterval` | Supported |
| `navigator` | Supported (userAgent, platform, hardwareConcurrency) |
| `console.time` / `table` / `trace` | Supported |

Ramune also supports `package.json` `"exports"` field resolution (conditional exports with `require`/`import`/`default` and subpath exports).

## TypeScript-to-Go Transpiler (Experimental)

> **Warning:** This feature is experimental and under active development. While many TypeScript patterns are supported, complex real-world codebases (e.g., frameworks with advanced generics, method overloads, or nullable string patterns) may produce code that requires manual fixes. Contributions and bug reports are welcome.

Ramune includes a built-in TypeScript-to-Go transpiler. It converts TypeScript source code to idiomatic Go, supporting a wide range of TypeScript features.

### Supported TypeScript Features

| Category | Features |
|----------|----------|
| **Types** | `number` → `float64`, `string`, `boolean`, `void`, `null`/`undefined` → `nil`, `any`, `unknown` → `any` |
| **Collections** | `T[]` → `[]T`, `Map<K,V>` → `map[K]V`, `Set<T>` → `map[T]struct{}` |
| **Nullable** | `T \| null`, `T \| undefined` → `*T` (pointer) |
| **Generics** | Functions and classes with type parameters and constraints |
| **Enums** | String enums → Go const block, numeric enums → iota |
| **Discriminated unions** | `type Shape = Circle \| Square` with narrowing via `if (s.kind === "circle")` |
| **Union of literals** | `"a" \| "b" \| "c"` → `string` |
| **Promises** | `Promise<T>` → `*promise.Promise[T]`, `async`/`await` supported |
| **Classes** | Fields, constructors, methods, inheritance (`extends`), static fields/methods, abstract classes, getter/setter accessors |
| **Interfaces** | Converted to Go struct types |
| **Destructuring** | Object `{a, b}` and array `[x, y]` patterns, default values (`{a = 1}`) |
| **Spread** | `[...arr, elem]`, `{...obj, key: val}` |
| **Template literals** | `` `Hello ${name}` `` → `fmt.Sprintf` |
| **Operators** | `typeof`, `instanceof`, `in`, `delete`, `?.` (optional chaining), `??` (nullish coalescing), `**` (exponentiation), `>>>`, `&&=`, `\|\|=`, `??=` |
| **Array methods** | `map`, `filter`, `reduce`, `find`, `forEach`, `includes`, `some`, `every`, `push`, `pop`, `slice`, `splice`, `concat`, `join`, etc. |
| **String methods** | `split`, `includes`, `startsWith`, `endsWith`, `trim`, `replace`, `replaceAll`, `repeat`, `padStart`, `padEnd`, etc. |
| **Loops** | `for`, `for...of`, `for...in`, `for await...of`, `while`, `do...while`, labeled `break`/`continue` |
| **Error handling** | `try`/`catch`/`finally` |
| **Modules** | Relative imports, Node.js built-in modules, npm packages (uuid, lodash, zod), Go packages via `go:` prefix, `export default`, re-exports (`export { x } from './mod'`) |
| **Go interop** | `go:` prefix imports any Go package, multi-return destructuring (`const [resp, err] = http.Get(url)`), auto go.mod for third-party modules |
| **Type resolution** | Conditional types, mapped types (`Record<K,V>` → `map[K]V`), intersection types, utility types via checker |

### Type Mapping

| TypeScript | Go |
|---|---|
| `number` | `float64` |
| `string` | `string` |
| `boolean` | `bool` |
| `T[]` | `[]T` |
| `T \| null` | `*T` |
| `Map<K, V>` | `map[K]V` |
| `Set<T>` | `map[T]struct{}` |
| `Promise<T>` | `*promise.Promise[T]` |
| `any` / `unknown` | `any` |
| Enum | Named type with const block |
| Class | Struct with methods |
| Interface | Struct type |

### Native Extension Module Workflow

The `--native` flag in `ramune compile` transpiles TypeScript to Go and exposes exported functions as `require()`-able JavaScript modules:

```
TypeScript → Go transpiler → Go source → NativeModuleFromFuncs → require('native:name')
```

**Supported native function signatures:**
- Primitive parameters and returns: `number`, `string`, `boolean`
- Struct parameters: JS objects auto-converted to Go structs via field name matching
- Struct returns: Go structs returned as JS objects with live getter/setter properties
- Typed slice parameters: `number[]` → `[]float64`, etc.
- Typed slice/map returns: `[]int` → JS array, `map[string]int` → JS object, nested supported
- Error returns: `(T, error)` — errors become JS exceptions
- Pointer returns: `*Struct` — returned with methods and mutable fields
- Panics caught and converted to JS exceptions

**Not supported as native exports:**
- Generic functions (must be wrapped in a non-generic function)
- Functions with channel parameters
- Functions with interface{} parameters other than `any`

### Go Package Imports (`go:` prefix)

Import any Go standard library or third-party package directly in TypeScript:

```typescript
import { Println, Sprintf } from "go:fmt"
import * as http from "go:net/http"
import { Default } from "go:github.com/gin-gonic/gin"

const [resp, err] = http.Get("http://example.com")  // Go multi-return
Println(Sprintf("status: %d", resp.StatusCode))
```

Generate TypeScript type definitions for Go packages to enable type checking:

```bash
ramune typegen go:fmt go:net/http -o go.d.ts
```

This generates `declare module "go:fmt" { ... }` with full function signatures, interfaces, and type mappings.

### Transpiler Limitations

- **No decorators** — complex metaprogramming, not yet supported
- **No generators / `yield`** — Go has no generator concept; use channels or callbacks instead
- **No dynamic `import()`** — use `require()` (which is supported)
- **No Symbol or Proxy** — no Go equivalent
- **Generic function exports** — cannot be exposed as native module exports; wrap in a non-generic function
- **Numeric precision** — all numbers are `float64` (no BigInt-like arbitrary precision)
- **`any`-typed property access** — uses `jsrt.GetField()` runtime reflection (struct fields and map keys)

## Known Limitations

- **N-API / Native addons**: Not supported. Packages that require `.node` native binaries (e.g., `bcrypt`, `sharp`, `better-sqlite3`) will not work. Use pure JS alternatives instead.
- **HTTP self-fetch**: Ramune.serve() handlers cannot fetch their own server (same JS context deadlock).
- **Windows**: JSC backend not available. Use `-tags quickjs` for Windows support.
- **Linux multi-runtime (JSC)**: Architecture-dependent signal handling. On arm64, `CGO_ENABLED=1` and gcc are required for multi-runtime (cgo's signal forwarding is needed for JSC's GC). On x86_64, multi-runtime works without cgo (`CGO_ENABLED=0`).
- **Multi-worker scaling (JSC)**: Scaling flattens around 4+ workers on macOS due to JSC shared-cache threading constraints. Linux (libjavascriptcoregtk) may differ.
- **QuickJS backend**: No JIT — CPU-bound JS is ~67x slower than JSC. Error stack traces not available. Best for embedding/scripting, not compute-heavy workloads.
- **Native module instance lifecycle**: Struct instances returned to JS are not automatically freed when the JS object is garbage collected. Instances are cleaned up when `Runtime.Close()` is called. For long-running servers creating many short-lived struct instances, this may cause increased memory usage.

## Requirements

| | JSC backend (default) | QuickJS backend (`-tags quickjs`) |
|---|---|---|
| **Go** | 1.26+ | 1.26+ |
| **Platforms** | macOS, Linux | macOS, Linux, Windows, FreeBSD |
| **System deps** | macOS: none. Linux: `apt install libjavascriptcoregtk-4.1-dev` | None |

All tools are built in — no external dependencies needed for `check`, `fmt`, `lint`, or TypeScript transpilation. npm packages are fetched directly from the npm registry — no npm or bun CLI required.

## Agent Skills

Ramune ships with an [Agent Skill](https://agentskills.io/) that teaches AI agents (Claude Code, GitHub Copilot, etc.) how to use Ramune:

```bash
ramune skills install   # install to ~/.agents/skills/ and .claude/skills/
```

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

Ramune includes code from the following projects:

| Project | License | Usage | Inclusion |
|---------|---------|-------|-----------|
| [microsoft/typescript-go](https://github.com/microsoft/typescript-go) | Apache-2.0 | Type checker, formatter (TS 7.0-dev) | Source copy (`internal/tsgo/`) |
| [web-infra-dev/rslint](https://github.com/web-infra-dev/rslint) | MIT | Linter | Source copy (`internal/rslint/`) |
| [evanw/esbuild](https://github.com/evanw/esbuild) | MIT | TypeScript transpilation, bundling | Go module dependency |

License texts for source-copied projects are in `internal/tsgo/LICENSE` and `internal/rslint/LICENSE`.

The Ramune logo includes the Go Gopher, originally designed by [Renée French](https://reneefrench.blogspot.com/), licensed under [Creative Commons Attribution 4.0](https://creativecommons.org/licenses/by/4.0/).
