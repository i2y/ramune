---
name: ramune
description: Use Ramune — a JavaScript/TypeScript runtime powered by JavaScriptCore or QuickJS-NG-on-wazero (no Cgo). Use this skill when the user wants to run JS/TS code, build JS/TS applications, embed a JS engine in Go, transpile TypeScript to Go, create native extension modules, use Ramune CLI, or work with JavaScriptCore from Go. Triggers on "ramune", "run javascript", "run typescript", "JS runtime", "embed JS in Go", "JavaScriptCore", "transpile typescript to go", "native module".
---

# ramune

Ramune is a JS/TS runtime and embeddable JS engine for Go. Tri-backend: JavaScriptCore (JIT, macOS/Linux) via purego, qjswasm (pure Go, cross-platform incl. Windows — QuickJS-NG compiled to WebAssembly and driven by wazero) via fastschema/qjs, and goja (pure Go, reflect-based, ~94% ECMAScript) via dop251/goja — no Cgo required for any of them.

## CLI

```bash
ramune eval "1 + 2"
ramune eval "const x: number = 42; x"
ramune eval "require('crypto').randomUUID()"
```

### Run a File

```bash
ramune run script.ts
ramune run -p lodash -p dayjs app.ts   # with npm packages
```

### ESM (import/export)

```bash
echo 'import { join } from "path"; console.log(join("/a", "b"))' > test.mjs
ramune run test.mjs
```

### Multi-worker HTTP

```bash
ramune run --workers 4 server.ts   # 4 parallel JSC VMs
```

### Compile to Binary

```bash
ramune compile app.ts -o myapp --http
./myapp
```

### Native Extension Modules

Compile TypeScript to native Go code and call from JS at full compiled speed:

```bash
ramune compile app.js --native math.ts -o myapp
ramune compile app.js --native math.ts --native geometry.ts -o myapp
```

Exported functions become available via `require('native:modulename')`. Supports structs with live properties, typed arrays, error handling, and class-like instances.

### Transpile TypeScript to Go (Experimental)

> **Note:** Experimental — under active development. Generated code may require manual adjustments for complex codebases.

```bash
ramune transpile main.ts -o out/                         # single file
ramune transpile main.ts utils.ts -o out/ --module myapp # multi-file project
ramune transpile main.ts --compile -o myapp              # transpile + build binary
```

Converts TypeScript types, classes (static, abstract, getter/setter), interfaces, generics, async/await, enums, discriminated unions, typeof/instanceof/in/delete, optional chaining, nullish coalescing, exponentiation, destructuring with defaults, for await...of, export default, re-exports, conditional/mapped types, and more to idiomatic Go.

### Test

```bash
ramune test                    # run all *.test.ts, *.spec.js, etc.
ramune test --coverage         # with coverage
```

## Go Library

```go
rt, _ := ramune.New(ramune.NodeCompat(), ramune.WithFetch())
defer rt.Close()

val, _ := rt.Eval(`1 + 2`)
defer val.Close()
fmt.Println(val.Float64()) // 3

// Register Go functions callable from JS
rt.RegisterFunc("inspect", func(args []any) (any, error) {
    global := rt.GlobalObject()
    defer global.Close()
    return global.Attr("myVar").String(), nil
})

// JS functions passed to Go are wrapped as *JSFunc
rt.RegisterFunc("map", func(args []any) (any, error) {
    fn := args[0].(*ramune.JSFunc)
    defer fn.Close()
    result, _ := fn.Call(42.0) // invoke JS function from Go
    return result, nil
})
```

### Native Module from Typed Functions

Create `require()`-able modules from typed Go functions (no manual argument parsing):

```go
mod := ramune.NativeModuleFromFuncs("native:math", map[string]any{
    "add":       func(a, b float64) float64 { return a + b },
    "fibonacci": mymath.Fibonacci,
})
rt, _ := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
rt.Eval(`require('native:math').add(3, 4)`) // 7
```

Supports struct parameters (JS objects auto-converted), struct returns with live getter/setter properties and methods, typed slices/maps (both parameters and returns), error handling, and panic recovery.

### Async / Promises

```go
val, _ := rt.EvalAsync(`
    fetch("https://httpbin.org/get").then(r => r.json())
`)
```

### Multi-core Pool

```go
pool, _ := ramune.NewPool(4, ramune.NodeCompat())
defer pool.Close()
pool.Eval("computeHeavy()")  // round-robin across 4 JSC VMs
```

### Reading Results

```go
f, _ := val.Float64()       // numbers
s, _ := val.GoString()      // strings
b, _ := val.Bool()          // booleans
m, _ := val.ToMap()         // objects -> map[string]any
arr, _ := val.ToSlice()     // arrays -> []any
bytes, _ := val.Bytes()     // TypedArray -> []byte
```

### HTTP Server (Bun-compatible)

```javascript
Ramune.serve({
  port: 3000,
  fetch(req) {
    return new Response("Hello from Ramune!");
  },
});
```

## Node.js Compatibility

Built-in support for: fs, path, crypto, stream (with ES6 class extends, asyncIterator, backpressure), zlib, os, http/https, http2, net/tls, dgram (UDP), child_process, worker_threads (with SharedArrayBuffer, Atomics.waitAsync), events, Buffer, url, assert, readline, vm, dns, querystring, util, process, tty, timers/promises, perf_hooks, async_hooks (AsyncLocalStorage), module (createRequire).

Web APIs: fetch, ReadableStream/WritableStream/TransformStream, crypto.subtle, Blob/File, FormData, Headers/Request/Response (Hono and other frameworks work out of the box), TextEncoder/TextDecoder, AbortController, URL/URLSearchParams, WebSocket (server-side), performance, structuredClone.
