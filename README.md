<p align="center">
  <img src="ramune.png" alt="Ramune" width="800">
</p>

# Ramune

> **Build your own Workers-style platform in Go. Cloudflare-Workers-compatible out of the box.**

*Three ways to use it: a self-hosted Workers-style platform foundation, a standalone JS/TS runtime (`run` / `test` / `check` / `fmt` / `lint` / `repl` / `compile`), or an embeddable JS engine for any Go program (`ramune.New()`).*

Ramune is a Workers-style runtime you embed and extend. Author
handlers as ES modules (`export default { fetch, scheduled }`),
design your own `env` in Go - Redis, Postgres, S3, SMTP, NATS,
your own gRPC service, anything - and ship it as your platform.
Code written against Cloudflare Workers runs unchanged, because
Ramune ships a Workers-compatible surface by default. No Cgo, no
Docker required, no Node.

```ts
// worker.ts
export default {
  async fetch(request, env, ctx) {
    return Response.json({ hello: "world" });
  },
};
```

```bash
ramune serve worker.ts                 # dev / production serve
ramune compile worker.ts -o myworker   # or compile to a single binary you can scp
```

**Scope.** Ships today: `fetch`, `env.KV`, `env.SECRETS`,
`ctx.waitUntil`, `scheduled`, cron, WinterCG basics. `env.DB` is a
D1-compatible API subset (`prepare`/`bind`/`all`/`first`/`run`/`exec`;
`batch` and `raw` not yet implemented, `.all()` meta fields not
populated). "D1-compatible" and "Workers-KV-like" describe API shape
only — the defaults are a single-node local SQLite file, not
Cloudflare's edge-replicated D1 or globally eventually-consistent
Workers KV. Swap in Postgres / Planetscale / Redis / DynamoDB via
`DBBackend` / `KVBackend` if you need distributed scaling. Not shipped,
but implementable as user-supplied `env.*` bindings (see
[`workers/BINDINGS.md`](workers/BINDINGS.md)): Durable Objects,
Queues, R2, AI Gateway, Service Bindings, Hyperdrive.

## Why Ramune?

- **Design your own `env`.** The env you ship to handlers is yours,
  not a fixed vendor surface. `env.KV` / `env.DB` are Go interfaces
  (`KVBackend`, `DBBackend`); swap for Redis, Postgres, DynamoDB,
  in-memory, anything by implementing them:

  ```go
  type redisKV struct{ c *redis.Client }

  func (r *redisKV) Get(ns, k string) (string, bool, error) {
      v, err := r.c.Get(ctx, ns+":"+k).Result()
      if err == redis.Nil { return "", false, nil }
      return v, err == nil, err
  }
  // ...Put, Delete, List

  workers.Register(rt, "worker.ts", src, workers.WithKVBackend(&redisKV{c: myRedis}))
  ```

  Invent new bindings (`env.QUEUE`, `env.EMAIL`, `env.AI`, `env.R2`,
  anything your product needs) by registering a Go callback plus a
  tiny JS facade via `WithExtraEnvJS`. Handler code uses `env.FOO`
  naturally. Walkthrough with the full pattern:
  [`workers/BINDINGS.md`](workers/BINDINGS.md). Runnable example:
  [`examples/workers/custom-binding/`](examples/workers/custom-binding/).
- **Run Workers-style handlers on your own hardware.** Ramune ships a
  Workers-compatible default surface (within the scope above), so
  `export default { fetch, scheduled }` code written against
  Cloudflare runs unchanged on your VM, bare metal, or air-gapped
  box: `ctx.waitUntil`, `env.KV`, `env.DB`, `env.SECRETS`, streaming,
  and cron are all there out of the box.
- **Single-binary deploy.** `ramune compile worker.ts -o myworker`
  bundles handler + runtime into one Go executable. No Kubernetes,
  no Wrangler, no Dockerfile required; `scp ./myworker prod:` and
  run. (qjswasm path is fully self-contained; JSC path still resolves
  the system JSC at run time, see next bullet.)
- **No Cgo at build; honest about runtime.** `go build`
  cross-compiles to any `GOOS`/`GOARCH` without a C toolchain. JSC
  backend loads JavaScriptCore dynamically via
  [`purego`](https://github.com/ebitengine/purego): zero install on
  macOS (JSC ships with the OS), `libjavascriptcoregtk-4.1` required
  on Linux (a bundled JSCOnly build is on the roadmap). qjswasm
  backend is pure Go with zero runtime dependencies (QuickJS-NG
  compiled to WebAssembly, embedded into the Go binary and driven
  by wazero) and runs on `FROM scratch` Docker, at the cost of JIT
  performance.
- **Full Go interop.** Workers are authored in TypeScript; everything
  underneath is Go. Call any Go library, including the stdlib and your
  existing services.
- **Also a complete JS/TS runtime and embeddable library.** `ramune
  run` / `test` / `check` / `fmt` / `lint` / `repl` / `compile` with
  tsgo + rslint + esbuild built in. `ramune.New()` gives you a JS
  engine for any Go program.

Tri-backend: **JavaScriptCore** (JIT, macOS/Linux) via [purego](https://github.com/ebitengine/purego), **qjswasm** (pure Go, cross-platform incl. Windows — QuickJS-NG compiled to WebAssembly and driven by wazero) via [fastschema/qjs](https://github.com/fastschema/qjs), and **goja** (pure Go, reflect-based, ~94% ECMAScript) via [github.com/dop251/goja](https://github.com/dop251/goja) — no Cgo required for any of them. Type checker and formatter ([typescript-go](https://github.com/microsoft/typescript-go)), linter ([rslint](https://github.com/web-infra-dev/rslint)), bundler ([esbuild](https://github.com/evanw/esbuild)), and all Node.js polyfills are built in with zero external tool dependencies.

Named after [Ramune](https://en.wikipedia.org/wiki/Ramune), a Japanese carbonated soft drink served in a Codd-neck bottle.

```bash
ramune serve worker.ts        # Serve Workers-style module
ramune run server.ts          # Run TypeScript (classic)
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

Ramune is **three** things, with one narrative:

1. **A foundation for Workers-style platforms** - `ramune serve
   worker.ts` runs Workers-style handlers out of the box (SQLite
   defaults for `env.DB` / `env.KV`, Cloudflare-compatible so CF
   code runs as-is), and the same runtime is the substrate you
   build your own platform on - your `env`, your backends, your
   bindings. Core scope: [WinterCG](https://wintercg.org/) + fetch
   + `env.DB` / `env.KV` / `env.SECRETS` + `ctx.waitUntil` + cron.
   Durable Objects / Queues / R2 / AI Gateway are **not shipped**;
   they are implemented as user-supplied `env.*` bindings so the
   core stays small and you aren't blocked on us.
2. **A JS/TS runtime** like Bun or Deno, but built in Go — `ramune
   run`, `test`, `check`, `fmt`, `lint`, `repl`, `compile`.
3. **An embeddable JS engine** for Go applications — `ramune.New()` in
   any Go program.

**One runtime, three surfaces.** Same Go-native core, three entry points: embed it in a Go program, run TypeScript from the CLI, or serve Workers-style handlers on it.

Three backends, same API:

| | JSC (default) | qjswasm (`-tags qjswasm`) | goja (`-tags goja`) |
|---|---|---|---|
| **Engine** | Apple JavaScriptCore via [purego](https://github.com/ebitengine/purego) | [fastschema/qjs](https://github.com/fastschema/qjs) — QuickJS-NG on [wazero](https://github.com/tetratelabs/wazero) (pure Go) | [dop251/goja](https://github.com/dop251/goja) (pure Go, reflect-based) |
| **JIT** | Yes | No | No |
| **Platforms** | macOS, Linux | macOS, Linux, **Windows**, FreeBSD | macOS, Linux, **Windows**, FreeBSD |
| **System deps** | macOS: none. Linux: libjavascriptcoregtk | None | None |
| **Spec coverage** | ES2023 | ES2023 | ES2023 effective (goja native ~ES2017; esbuild lowers newer syntax transparently on Eval) |
| **Best for** | Performance, HTTP servers | Embedding, scripting, portability | Pure-Go embedding, Windows-native, no cgo signal forwarding |

All three are pure Go at build time: `go build` needs no C toolchain. Runtime deps differ: JSC resolves the system JavaScriptCore (zero install on macOS, `libjavascriptcoregtk-4.1` on Linux); qjswasm and goja have none.

**For AI coding agents:** `ramune skills install` adds an [Agent Skill](https://agentskills.io/) that teaches Claude Code, GitHub Copilot, and similar tools how to use Ramune's APIs and CLI.

## Install

### macOS

JavaScriptCore is built into macOS — no extra dependencies.

```bash
go install github.com/i2y/ramune/cmd/ramune@latest
go install github.com/i2y/ramune/cmd/ramune-toolchain@latest  # for check / fmt / lint / compile / transpile / typegen
ramune setup-jit   # enable JIT (~10x faster, recommended)
```

### Linux

```bash
sudo apt install libjavascriptcoregtk-4.1-dev   # JSC runtime (required)
go install github.com/i2y/ramune/cmd/ramune@latest
go install github.com/i2y/ramune/cmd/ramune-toolchain@latest  # for check / fmt / lint / compile / transpile / typegen
```

Multi-runtime (RuntimePool, worker_threads) works out of the box on x86_64. On arm64, gcc is required for cgo signal forwarding (`apt install gcc`).

### Windows / Zero-dependency (qjswasm backend)

```bash
go install -tags qjswasm github.com/i2y/ramune/cmd/ramune@latest
go install -tags qjswasm github.com/i2y/ramune/cmd/ramune-toolchain@latest  # optional: check / fmt / lint / compile
```

The qjswasm backend uses [fastschema/qjs](https://github.com/fastschema/qjs) — QuickJS-NG compiled to WebAssembly and driven by [wazero](https://github.com/tetratelabs/wazero)'s compiler-mode JIT (AOT WASM→native). Pure Go, ES2023, no shared libraries — works on **Windows**, macOS, Linux, and FreeBSD. Trade-off: no JS JIT, so CPU-bound code is slower than JSC (see [Performance](#performance)).

### Goja backend (`-tags goja`, pure Go, reflect-based)

```bash
go install -tags goja github.com/i2y/ramune/cmd/ramune@latest
```

The goja backend wraps [dop251/goja](https://github.com/dop251/goja) unchanged, so it's a **drop-in for existing goja users**: scripts and Go interop code that run on goja directly run on Ramune with `-tags goja` with no behavioral change, and can later switch to `-tags qjswasm` or the default JSC build to gain throughput without touching the handler code. goja is a reflection-based Go JS interpreter with ~94% ECMAScript coverage. Appropriate when you want **pure-Go embedding on Windows** without any shared libraries and want to avoid the cgo signal-forwarding requirement that JSC needs on Linux/arm64. Modern JS syntax that goja's parser rejects (private class fields, top-level await, `Object.hasOwn`, logical assignment, etc.) is transparently lowered to ES2017 via esbuild on first-encounter parse failure in `Runtime.Eval` / `Runtime.Exec` — both CLI and library paths see the same effective ES2023 surface, and the lowered result is cached so repeated source is amortized.

### Smaller binary

```bash
go install -tags nosqlite -ldflags="-s -w" github.com/i2y/ramune/cmd/ramune@latest
```

The main `ramune` binary above is ~30MB and holds the runtime; `ramune-toolchain` (~60MB) is a separate development-only binary for `check` / `fmt` / `lint` / `compile` / `transpile` / `typegen`. If you only need `ramune run` / `serve` / `eval` / `repl` / `test`, you can skip installing `ramune-toolchain` entirely.

`-tags nosqlite` excludes bun:sqlite. `-ldflags="-s -w"` strips debug info. Combine with `-tags qjswasm,nosqlite` for the smallest possible binary.

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

Options: `--http` (Ramune.serve event loop), `--minify` (esbuild minification). Output binary is ~28MB (linter/formatter/checker are not included — only the runtime).

> **Note:** The compiled binary loads JavaScriptCore dynamically at runtime. The target machine must have JSC available (macOS: built-in, Linux: `libjavascriptcoregtk`). Use `-tags qjswasm` for cross-platform builds.

### Native Extension Modules (Experimental)

> **Note:** This workflow uses the [TypeScript-to-Go transpiler](./TRANSPILER.md) under the hood and inherits its experimental status. Simple typed functions (primitives, structs, typed slices) work reliably; generics, decorators, and deep class inheritance may produce Go code that needs manual fixes.

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

The transpiler converts TypeScript types, classes, interfaces, generics, async/await, and more to idiomatic Go. See [`TRANSPILER.md`](./TRANSPILER.md) for supported features and limitations.

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

### Workers-style Modules (`ramune serve`)

The flagship command. Serve a Cloudflare-Workers-style ES-module
handler — export a default object with `fetch(request, env, ctx)` and
the CLI wires it up:

```ts
// worker.ts
export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext) {
    const url = new URL(request.url);
    return Response.json({ hello: url.searchParams.get("name") ?? "world" });
  },
} satisfies WorkersHandler;
```

```bash
ramune serve worker.ts                    # listens on :3000
ramune serve --port 8080 worker.ts        # custom port
ramune serve --workers 4 worker.ts        # N runtimes, round-robined
ramune serve --sqlite :memory:  worker.ts # non-persistent env.DB/env.KV
```

`ctx.waitUntil(promise)` keeps the executor alive after the response
goes out. `env.SECRETS` reads `RAMUNE_SECRET_*` env vars. `env.DB`
(D1-compatible API subset, see Scope above) and `env.KV`
(Workers-KV-like API) are backed by a single-node local SQLite file at
`.ramune/data.db` by default — this matches the D1/Workers-KV handler
surface, not their distributed storage profile. For production
horizontal scaling, implement `DBBackend` / `KVBackend` against your
actual database (Postgres, Redis, etc.) and bind via `WithDBBackend` /
`WithKVBackend`. Works with Hono directly (`export default app`).

An optional `ramune.toml` next to the entry declares dependencies,
permissions, and named KV bindings:

```toml
[dependencies]
hono = "*"

[permissions]
net = "granted"
read = "denied"

[[kv_namespaces]]
binding = "SESSIONS"
namespace = "sessions"
```

See [`examples/workers/`](examples/workers/) for hello, SSE, Hono, and
a full HTML guestbook. Type declarations live in
[`workers/workers.d.ts`](workers/workers.d.ts). For non-SQLite
backends (Redis, Postgres, …) and user-defined bindings
(`env.QUEUE`, `env.EMAIL`, …), see the embed API below and the
[custom binding guide](workers/BINDINGS.md).

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

### Workers-style Modules (`ramune/workers`)

The Go embed API for Workers-style handlers — returns an `http.Handler`
that slots into any Go HTTP server (`net/http`, chi, Echo, gRPC
gateway, …):

```go
import "github.com/i2y/ramune/workers"

rt, _ := ramune.New(ramune.NodeCompat(), ramune.WithFetch())
defer rt.Close()

src, _ := os.ReadFile("worker.ts")
handler, err := workers.Register(rt, "worker.ts", string(src),
    workers.WithSQLite(".ramune/data.db"),
    workers.WithWaitUntilTimeout(30*time.Second),
)
http.ListenAndServe(":3000", handler)
```

`ctx.waitUntil(promise)` is honoured — the HTTP response ships
immediately while the executor drains pending promises. For multi-VM
setups, `workers.Prepare` runs esbuild once and `workers.AttachPrepared`
binds to each Runtime.

**Swap env.KV / env.DB for anything.** The built-in SQLite path is
built on `KVBackend` / `DBBackend` Go interfaces; implement them with
Redis, Postgres, DynamoDB, or an in-memory map:

```go
// type KVBackend interface { Get/Put/Delete/List ... }
// type DBBackend interface { Query/Exec ... }
workers.Register(rt, "w.ts", src,
    workers.WithKVBackend(myRedisKV),
    workers.WithDBBackend(myPostgres),
)
```

**Invent your own bindings.** Anything a CF Workers binding does —
`env.QUEUE.send`, `env.EMAIL.send`, `env.R2.put`, `env.AI.run`,
`env.DURABLE.get` — is a few lines of Go. Register a callback with
`RegisterFunc`, inject a small JS facade via `WithExtraEnvJS`, and
handler code uses `env.FOO` naturally:

```go
rt.RegisterFunc("__env_email_send", func(args []any) (any, error) {
    // wire up SMTP / SendGrid / SES here
    return nil, myMailer.Send(opts)
})

handler, _ := workers.Register(rt, "w.ts", src,
    workers.WithExtraEnvJS(`
        globalThis.__extraEnvBindings = function(env) {
            env.EMAIL = { send: opts => __env_email_send(opts) };
        };
    `),
)
```

Full walkthrough with TypeScript types and the composition pattern
for stacking multiple bindings:
[`workers/BINDINGS.md`](workers/BINDINGS.md). Runnable example:
[`examples/workers/custom-binding/`](examples/workers/custom-binding/).

`workers.LoadRamuneTOML` parses the same `ramune.toml` schema the CLI
uses. The CLI-side wrapper is `ramune serve` (above).

### Multi-core Parallelism

Unlike Bun/Node (single-threaded), Ramune runs multiple JSC VMs in parallel on separate OS threads:

```go
pool, _ := ramune.NewPool(4, ramune.NodeCompat())
defer pool.Close()

pool.Eval("Math.PI * 2")                        // round-robin to one VM
pool.Broadcast("globalThis.config = {debug: true}")  // run on every VM

// Multi-worker HTTP server
pool.ListenAndServe(":3000", `
    globalThis.__poolHandle = function(req) {
        return { status: 200, body: "Hello from worker!" };
    };
`)
```

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
| `Ramune.serve({port, fetch, websocket})` | Supported (Go net/http backend, see [Performance](#performance)) |
| `Ramune.file(path)` | Supported (text, json, exists, size) |
| `Ramune.write(path, data)` | Supported |
| `Ramune.password.hash/verify` | Supported (bcrypt) |
| `Ramune.sleep(ms)` | Supported |
| `Ramune.plugin({setup})` | Supported (onLoad filters, virtual modules) |
| `Request` / `Response` | Polyfilled with ReadableStream body |
| `Ramune.build({entrypoints, outdir, ...})` | Supported (esbuild backend, minify, splitting, sourcemap) |
| `bun:sqlite` | Supported (transactions, WAL, prepared stmt cache, pure Go) |
| `Bun.*` | Alias for `Ramune.*` (partial Bun compatibility) |

### WebView (Desktop)

Open native desktop webview windows from JavaScript (macOS, via [glaze](https://github.com/nicois/glaze) + purego):

```go
// Go setup — must run on main thread (macOS requirement)
ramune.InitWebViewMain()
rt, _ := ramune.New(ramune.NodeCompat())
done := make(chan struct{})
go func() {
    rt.Exec(`
        var wv = new Ramune.WebView({ title: "My App", width: 800, height: 600 });
        wv.navigate("https://example.com");
        // or: wv.setHtml("<h1>Hello</h1>");
    `)
    rt.RunEventLoop()
    close(done)
}()
ramune.DrainWebViewMain(done)
```

API: `navigate(url)`, `setHtml(html)`, `eval(js)`, `setTitle(title)`, `setSize(w, h)`, `init(js)`, `destroy()`, `onclose(fn)`.

### WebView (Headless / Bun.WebView)

Headless browser automation via Chrome DevTools Protocol, compatible with [Bun.WebView](https://bun.sh/docs/api/webview):

```js
const wv = new Bun.WebView({ headless: true });
await wv.navigate("https://example.com");
console.log(await wv.evaluate("document.title")); // "Example Domain"
const screenshot = await wv.screenshot(); // PNG buffer
await wv.click(100, 200);
await wv.type("hello");
wv.close();
```

Requires Chrome or Chromium installed (set `CHROME_PATH` to override detection).

API: `navigate(url)`, `evaluate(expr)`, `screenshot(opts)`, `click(x, y)`, `type(text)`, `press(key)`, `scroll(dx, dy)`, `resize(w, h)`, `back()`, `forward()`, `reload()`, `cdp(method, params)`, `close()`.
Properties: `url`, `title`, `loading`.

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

**TL;DR.** Use JSC for raw single-worker throughput and latency-sensitive setups (1-3 workers). Use qjswasm for zero-dependency `FROM scratch` Docker, Windows-native, and many-worker deployments — it outperforms the previous modernc/quickjs backend by 1.7-2.3× at every worker count and scales 5.72× to 6 workers (vs modernc's 4.14×). Use goja for the smallest pure-Go embedding when the ~1 MB `qjs.wasm` embed is unwelcome. All three share the same Ramune API and the same Workers-style handler surface.

### JSC backend (default)

Relative performance on Apple M4 Max with JIT enabled. Absolute numbers shift with Go toolchain, hardware, and thermal state; run `make bench` for numbers on your machine.

| Workload | Ramune vs Node.js | Ramune vs Bun |
|---|---|---|
| Hello World startup | ~1.1x faster | ~2.4x slower |
| Fibonacci(35) CPU | ~1.3x faster | ~1.2x slower |
| JSON 10K objects | ~1.2x faster | ~2x slower |
| Crypto SHA256 x1000 | comparable | ~2x slower |
| File I/O x100 | comparable | ~1.7x slower |
| HTTP req/s (single) | ~1.1x slower | ~1.7x slower |

Single-runtime HTTP is below Bun but competitive with Node.js. For throughput see [Multi-Runtime Pool](#multi-runtime-pool) below: Ramune runs N JS VMs in parallel on separate OS threads, where Bun and Node stay single-threaded.

Raw throughput is only one axis. Ramune's edge over Bun is Go-native embedding (`ramune.New()` in any Go program), Workers-style compatibility out of the box, and multi-core scaling within one process; Bun offers none of these.

### qjswasm backend (`-tags qjswasm`)

Same machine, no JS JIT. QuickJS-NG (compiled to WASM, run on wazero's compiler-mode JIT) is slower than JSC on CPU-heavy benchmarks (Fibonacci, JSON); I/O-bound workloads stay close because the heavy lifting happens in Go. Best suited for embedding, scripting, Windows, and `FROM scratch` Docker deployments where zero runtime deps matter more than raw JS execution speed.

| Workload (Apple M4 Max) | qjswasm | Ratio vs JSC |
|---|---|---|
| Fibonacci(35) | 1.99 B ns/op | ~58x slower |
| JSON 10K objects | 19.6 M ns/op | ~20x slower |

### Goja backend (`-tags goja`)

Pure-Go reflection-based interpreter. Smaller footprint (no ~1 MB `qjs.wasm` embed) and smallest pure-Go build, but slower on CPU-heavy JS than qjswasm at 4+ workers.

### Multi-Runtime Pool

Ramune runs multiple JS VMs in parallel on separate OS threads (Bun/Node are single-threaded). Measured on Apple M4 Max with `bench/pool/pool_bench.go` (JSON generate/filter/map handler, 200 objects per request, `wrk -t4 -c100 -d10s`). Numbers below are the **median of 3 runs per backend**; reproduce with `go build [-tags qjswasm|-tags goja] -o pool bench/pool/pool_bench.go && ./pool 6`.

#### JSC (default)

| Workers | req/s | Scaling |
|---------|-------|---------|
| 1 | 40,511 | 1.0x |
| 2 | 54,500 | 1.35x |
| 3 | 58,401 | 1.44x |
| 4 | 59,706 | 1.47x |
| 5 | 60,913 | 1.50x |
| 6 | 62,407 | 1.54x |

JSC wins on absolute throughput by a wide margin thanks to the JIT. Multi-worker scaling is shallow but monotonic — the single-worker JIT throughput is already close to saturating what the handler can generate, so additional workers add modest headroom (~54% over 1-worker at 6 workers). For latency-sensitive workloads, 1-3 workers is usually optimal; past 3 workers the curve is close to flat.

#### qjswasm (`-tags qjswasm`)

| Workers | req/s | Scaling |
|---------|-------|---------|
| 1 | 2,348 | 1.0x |
| 2 | 4,666 | 1.99x |
| 3 | 6,782 | 2.89x |
| 4 | 9,152 | 3.90x |
| 5 | 11,331 | 4.83x |
| 6 | 13,435 | 5.72x |

Monotonic out to 6 workers (and still linear). QuickJS-NG compiled to WASM and driven by wazero sidesteps the global-allocator contention that hobbled the previous modernc/quickjs backend — the wasm linear memory is per-runtime with no shared-allocator mutex to fight over.

#### goja (`-tags goja`)

| Workers | req/s | Scaling |
|---------|-------|---------|
| 1 | 3,518 | 1.0x |
| 2 | 5,833 | 1.66x |
| 3 | 7,404 | 2.10x |
| 4 | 8,374 | 2.38x |
| 5 | 9,351 | 2.66x |
| 6 | 10,173 | 2.89x |

Pure-Go reflection interpreter. Faster than qjswasm at 1-3 workers (lower setup cost, no wazero compile) but qjswasm pulls ahead from 4 workers on.

**Backend selection by shape.** JSC wins by a wide margin on absolute throughput at every worker count (~40k single-worker, ~62k at 6 workers). qjswasm has the best *multiplicative* scaling (5.72× at 6 workers) and the highest absolute throughput among pure-Go backends past 3 workers. goja is the simplest pure-Go option and is the fastest pure-Go at 1-3 workers (no wasm compile cost). Pick the backend that matches your target shape.

### vs Go JS Runtimes

Ramune with JSC+JIT vs other Go-embedded JS runtimes (absolute ms per workload, lower is better):

| Test | Ramune (JSC+JIT) | Ramune (qjswasm) | Ramune (goja) | otto |
|------|-----------------|------------------|---------------|------|
| Fibonacci(35) | **35 ms** | 1,987 ms | 2,400 ms | 26,413 ms |
| JSON 10K objects | **0.98 ms** | 19.6 ms | 12.3 ms | 27 ms |

JSC with JIT is the fastest Go-embedded JS runtime by 1-2 orders of magnitude on CPU-heavy code. qjswasm (QuickJS-NG on wazero's AOT WASM→native JIT) is faster than goja on CPU-heavy integer code and slightly slower on pure-JSON workloads. otto is the slowest across all tests.

Run `make bench-go` to reproduce.

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
| stream | 85% (class extends, asyncIterator, backpressure, cork/uncork) | | Buffer | 90% |
| http/https | 80% (createServer with streaming response) | | assert | 80% |
| http2 | 75% (connect, createServer, trailers, multiplexing) | | dns | basic |
| net/tls | 70% (+ net.createServer) | | readline | 70% |
| worker_threads | 80% (+ SharedArrayBuffer, Atomics.waitAsync) | | querystring | 80% |
| vm | 70% | | perf_hooks | basic |
| timers/promises | 70% | | process | 85% (stdin, signals, exit, env, tty) |
| util | 80% (types, promisify, format, debuglog) | | tty | 70% (isatty, WriteStream) |
| dgram | 70% (UDP) | | async_hooks | basic (AsyncLocalStorage) |
| module | createRequire, file-based require with ESM-to-CJS | | | |

### Stream Classes

Stream classes (`Readable`, `Writable`, `Duplex`, `Transform`, `PassThrough`) are ES6 classes that support `class extends`:

```js
const { Transform } = require('stream');

class Upper extends Transform {
  _transform(chunk, encoding, cb) {
    this.push(String(chunk).toUpperCase());
    cb();
  }
}
```

Key features: `Symbol.asyncIterator` (`for await...of`), backpressure-aware `pipe()`, `cork()`/`uncork()`, `unshift()`, `unpipe()`, `objectMode`, `readableFlowing`/`readableEnded`/`writableEnded`/`writableFinished` properties, `stream.pipeline()`, `stream.finished()`.

`http.IncomingMessage` extends `Readable`, so request bodies support `pipe()`, `read()`, and `for await...of`.

### HTTP/2

```js
const http2 = require('http2');

// Client
const session = http2.connect('http://localhost:8080');
session.on('connect', () => {
  const req = session.request({ ':method': 'POST', ':path': '/api' });
  req.on('response', (headers) => { /* ... */ });
  req.on('data', (chunk) => { /* ... */ });
  req.on('trailers', (trailers) => { /* grpc-status, grpc-message */ });
  req.end(body);
});

// Server (cleartext h2c)
const server = http2.createServer((stream, headers) => {
  stream.respond({ ':status': 200, 'content-type': 'text/plain' });
  stream.end('hello');
});
server.listen(3000);
```

Supports `http2.connect()`, `createServer()`, `createSecureServer()`, stream multiplexing, trailers (for gRPC), and `http2.constants`.

## Web Platform APIs

Ramune implements the [WinterTC Minimum Common Web API](https://min-common-api.proposal.wintertc.org/) (ECMA-429), the standard API surface shared across non-browser JS runtimes (Deno, Cloudflare Workers, Bun, Node.js). The implementation is Go-side, so the Web API surface is consistent across all three backends (JSC, qjswasm, goja); only `WebAssembly` is JSC-only, and all other APIs below behave identically regardless of the backend.

| API | Status |
|-----|--------|
| `fetch` / `Headers` / `Request` / `Response` | Supported (Go net/http backend, ReadableStream body) |
| `ReadableStream` / `WritableStream` / `TransformStream` | Supported (pipeTo, pipeThrough, tee, BYOB, async iterator) |
| `ReadableStreamBYOBReader` / `ReadableByteStreamController` | Supported |
| `CompressionStream` / `DecompressionStream` | Supported (gzip, deflate, deflate-raw, brotli) |
| `TextEncoder` / `TextDecoder` | Supported (UTF-8) |
| `TextEncoderStream` / `TextDecoderStream` | Supported |
| `crypto.subtle` | Supported (digest, sign/verify, encrypt/decrypt, importKey/exportKey, deriveBits/deriveKey) |
| `crypto.getRandomValues` / `randomUUID` | Supported |
| `Blob` / `File` / `FormData` | Supported (stream, bytes, slice, MIME normalization) |
| `AbortController` / `AbortSignal` | Supported (timeout, abort, any) |
| `EventTarget` / `Event` / `CustomEvent` | Supported (addEventListener, once, signal, handleEvent) |
| `ErrorEvent` / `PromiseRejectionEvent` / `MessageEvent` | Supported |
| `MessageChannel` / `MessagePort` | Supported |
| `DOMException` | Supported (all legacy error codes) |
| `URL` / `URLSearchParams` / `URLPattern` | Supported |
| `WebSocket` | Supported (server-side via Ramune.serve) |
| `Performance` / `performance.now` | Supported (mark, measure, timeOrigin) |
| `SharedArrayBuffer` / `Atomics` | Supported (Go []byte backed, wait/notify/waitAsync) |
| `structuredClone` | Supported (circular refs, Map, Set, Date, RegExp, TypedArray) |
| `atob` / `btoa` | Supported |
| `setTimeout` / `setInterval` / `queueMicrotask` | Supported |
| `navigator.userAgent` | Supported |
| `reportError` / `onerror` / `onunhandledrejection` | Supported |
| `CountQueuingStrategy` / `ByteLengthQueuingStrategy` | Supported |
| `WebAssembly` | Supported (JSC backend; compile, instantiate, validate, streaming) |
| `console` | Supported (log, error, warn, info, debug, time, table, trace) |

### WPT Conformance

Ramune's Web API implementations are validated against the [Web Platform Tests](https://github.com/web-platform-tests/wpt) (WPT) suite:

```bash
make test-wpt   # run WPT conformance tests
```

Pass rates measure coverage against the full WPT subtest corpus, which includes browser-only edge cases irrelevant to a non-browser runtime (layout, DOM mutation, cross-window postMessage, etc.). Mainstream Workers patterns like `fetch`, `Response.json`, basic streaming readers/writers, and compression round-trips work reliably in practice; the unmet percentages are dominated by those browser-specific subtests and a handful of obscure stream-controller races. Run `make test-wpt` for the exact failing subtests per category.

Most categories pass at identical rates across all three backends because the Web APIs are Go-side polyfills:

| Category | Pass Rate |
|----------|-----------|
| timers | 100% |
| atob/btoa | 99% |
| hr-time | 86% |
| FileAPI/blob | 82% |
| microtask-queuing | 80% |
| dom/abort | 61% |

Backend-sensitive categories (differ because the underlying JS engine affects behavior or the polyfill interacts with engine-specific scheduling):

| Category | JSC | qjswasm | goja |
|----------|-----|---------|------|
| compression | 63% | 55% | 53% |
| streams | 53% | 53% | 51% |
| dom/events | 46% | 53%† | 53%† |
| webmessaging | 33% | 18% | 33% |

†qjswasm and goja skip `AddEventListenerOptions-signal.any.js` which hangs on both engines; goja additionally skips `gb18030-decoder.any.js` which triggers a goja parser panic on `\u{10FFFF}` string literals. Skip list lives in `wpt_test.go`.

WPT checkout is required (`test/wpt/`). See `make test-wpt` output for setup instructions.

### File-based `require()` with ESM Support

`require()` loads files from the filesystem with automatic ESM-to-CJS conversion:

```js
const { hello } = require('./lib.mjs');     // ESM -> CJS transform
const data = require('./config.json');       // JSON parsing
const utils = require('./utils.ts');         // TypeScript stripping
const pkg = require('some-package');         // node_modules resolution
```

ESM detection: `.mjs` extension, `package.json` `"type": "module"`, or `import`/`export` keywords. TypeScript ESM files (`.ts` with `import`/`export`) are processed in a single esbuild pass. Modules are cached by resolved absolute path. Per-module `require` functions ensure correct relative path resolution in nested imports.

Ramune also supports `package.json` `"exports"` field resolution (conditional exports with `require`/`import`/`default` and subpath exports).

## TypeScript-to-Go Transpiler (Experimental)

Ramune ships a built-in TypeScript-to-Go transpiler that converts TS source to idiomatic Go: `number` → `float64`, classes → structs with methods, `Promise<T>` → `*promise.Promise[T]`, generics, enums, discriminated unions. A `go:` prefix lets you import any Go package from TypeScript, and `ramune compile --native` turns TS modules into `require('native:name')`-able Go native modules.

Status is experimental; generated code may need manual fixes for complex codebases. Full feature list, type mapping, native module workflow, `go:` imports, and limitations: [`TRANSPILER.md`](./TRANSPILER.md).

## Known Limitations

- **N-API / Native addons**: Not supported. Packages that require `.node` native binaries (e.g., `bcrypt`, `sharp`, `better-sqlite3`) will not work. Use pure JS alternatives instead.
- **HTTP self-fetch**: Ramune.serve() handlers cannot fetch their own server (same JS context deadlock).
- **Windows**: JSC backend not available. Use `-tags qjswasm` for Windows support.
- **Linux multi-runtime (JSC)**: Architecture-dependent signal handling. On arm64, `CGO_ENABLED=1` and gcc are required for multi-runtime (cgo's signal forwarding is needed for JSC's GC). On x86_64, multi-runtime works without cgo (`CGO_ENABLED=0`).
- **Multi-worker scaling (JSC)**: Scaling flattens around 3-4 workers on macOS due to JSC JIT contention and purego FFI overhead. Linux (libjavascriptcoregtk) may differ.
- **qjswasm backend**: No JS JIT (wazero AOT-compiles the WASM but QuickJS-NG itself runs as an interpreter inside). CPU-bound JS is slower than JSC (see [Performance](#qjswasm-backend--tags-qjswasm)). Error stack traces not yet round-tripped. `ResourceLimits.MaxExecutionTime` silently ignored until the C-shim interrupt handler is wired (memory/stack/GC limits work).
- **goja backend**: No WebAssembly (JSC-only). Post-ES2017 syntax (private class fields, top-level await, etc.) is auto-lowered to ES2017 via esbuild at `Runtime.Eval`/`Exec` time on first parse failure and cached, so user code rarely hits goja's native parser limits in practice. Some subsystems are stubbed (WebSocket upgrade path). No JS error stack traces exposed to Go.
- **Native module instance lifecycle**: Struct instances returned to JS are not automatically freed when the JS object is garbage collected. Instances are cleaned up when `Runtime.Close()` is called. For long-running servers creating many short-lived struct instances, this may cause increased memory usage.

## Requirements

| | JSC backend (default) | qjswasm backend (`-tags qjswasm`) | goja backend (`-tags goja`) |
|---|---|---|---|
| **Go** | 1.26+ | 1.26+ | 1.26+ |
| **Platforms** | macOS, Linux | macOS, Linux, Windows, FreeBSD | macOS, Linux, Windows, FreeBSD |
| **System deps** | macOS: none. Linux: `apt install libjavascriptcoregtk-4.1-dev` | None | None |

All tools are built in — no external dependencies needed for `check`, `fmt`, `lint`, or TypeScript transpilation. npm packages are fetched directly from the npm registry — no npm or bun CLI required.

## Developing Ramune

Make targets for working on Ramune itself (contributor setup):

```bash
make ci          # fmt + build + vet + test
make test-wpt    # WPT conformance tests (requires test/wpt checkout)
make build-cli   # build with JIT entitlement (macOS)
make bench       # CLI benchmarks vs Bun/Node (hyperfine + wrk)
make bench-go    # compare vs goja / otto (Go-embedded runtimes)
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
| [dop251/goja](https://github.com/dop251/goja) | MIT | goja backend (`-tags goja`) | Go module dependency |
| [fastschema/qjs](https://github.com/fastschema/qjs) | MIT | qjswasm backend (`-tags qjswasm`) — QuickJS-NG on wazero, fork adds a `DisableFS` option for sandboxed use | Source copy (`third_party/qjs/`) via `replace` directive |
| [QuickJS-NG](https://github.com/quickjs-ng/quickjs) | MIT | Baked into the prebuilt `third_party/qjs/qjs.wasm` that the qjswasm backend embeds; license text at `third_party/qjs/qjswasm/quickjs/LICENSE` | Compiled-binary inclusion |
| [tetratelabs/wazero](https://github.com/tetratelabs/wazero) | Apache-2.0 | WebAssembly runtime that drives `qjs.wasm` for the qjswasm backend | Go module dependency |
| [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) | BSD-3-Clause | Pure-Go SQLite for `bun:sqlite` and the Workers-style `env.DB` default | Go module dependency |
| [evanw/esbuild](https://github.com/evanw/esbuild) | MIT | TypeScript transpilation, bundling | Go module dependency |

License texts for source-copied projects are in `internal/tsgo/LICENSE`, `internal/rslint/LICENSE`, `internal/rslint/tsgo_pinned/LICENSE` (a separate tsgo copy pinned to rslint's version for its shim bindings), and `third_party/qjs/LICENSE` + `third_party/qjs/qjswasm/quickjs/LICENSE` (see `third_party/qjs/NOTICES.md`).

The Ramune logo includes the Go Gopher, originally designed by [Renée French](https://reneefrench.blogspot.com/), licensed under [Creative Commons Attribution 4.0](https://creativecommons.org/licenses/by/4.0/).
