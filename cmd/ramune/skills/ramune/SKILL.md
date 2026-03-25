---
name: ramune
description: Use Ramune — a JavaScript/TypeScript runtime powered by JavaScriptCore via purego (no Cgo). Use this skill when the user wants to run JS/TS code, build JS/TS applications, embed a JS engine in Go, use Ramune CLI, or work with JavaScriptCore from Go. Triggers on "ramune", "run javascript", "run typescript", "JS runtime", "embed JS in Go", "JavaScriptCore".
---

# ramune

Ramune is a JS/TS runtime and embeddable JS engine for Go, powered by JavaScriptCore via purego — no Cgo required.

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
```

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

Built-in support for: fs, path, crypto, stream, zlib, os, http/https, net/tls, child_process, worker_threads, events, Buffer, url, assert, readline, vm, dns, querystring, util, process, timers/promises, perf_hooks.
