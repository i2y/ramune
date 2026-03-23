---
name: js-eval
description: Evaluate JavaScript or TypeScript code using Ramune (JavaScriptCore from Go, no Cgo). Use this skill when the user wants to run JS/TS, test expressions, use npm packages, prototype code, or verify behavior. Triggers on "run javascript", "run typescript", "evaluate JS", "test this code", "execute JS", or any request to run JS/TS code.
---

# js-eval

Evaluate JavaScript/TypeScript code using Ramune's JavaScriptCore runtime.

## CLI (quickest)

```bash
ramune eval "1 + 2"
ramune eval "const x: number = 42; x"
ramune eval "require('crypto').randomUUID()"
```

## Run a File

```bash
echo 'console.log("hello")' > /tmp/test.ts
ramune run /tmp/test.ts
```

## With npm Packages

```bash
ramune run -p lodash -p dayjs /tmp/test.js
```

## ESM (import/export)

```bash
echo 'import { join } from "path"; console.log(join("/a", "b"))' > /tmp/test.mjs
ramune run /tmp/test.mjs
```

## Multi-worker HTTP

```bash
ramune run --workers 4 server.ts   # 4 parallel JSC VMs
```

## Compile to Binary

```bash
ramune compile app.ts -o myapp --http
./myapp
```

## Go Library

```go
rt, _ := ramune.New(ramune.NodeCompat(), ramune.WithFetch())
defer rt.Close()

val, _ := rt.Eval(`1 + 2`)
defer val.Close()
fmt.Println(val.Float64()) // 3

// GoFunc can safely call Value methods
rt.RegisterFunc("inspect", func(args []any) (any, error) {
    global := rt.GlobalObject()
    defer global.Close()
    return global.Attr("myVar").String(), nil
})
```

## Async / Promises

```go
val, _ := rt.EvalAsync(`
    fetch("https://httpbin.org/get").then(r => r.json())
`)
```

## Multi-core Pool

```go
pool, _ := ramune.NewPool(4, ramune.NodeCompat())
defer pool.Close()
pool.Eval("computeHeavy()")  // round-robin across 4 JSC VMs
```

## Reading Results

```go
f, _ := val.Float64()       // numbers
s, _ := val.GoString()      // strings
b, _ := val.Bool()          // booleans
m, _ := val.ToMap()         // objects -> map[string]any
arr, _ := val.ToSlice()     // arrays -> []any
bytes, _ := val.Bytes()     // TypedArray -> []byte
```
