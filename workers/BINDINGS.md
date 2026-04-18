# Custom `env` bindings

Workers-style modules receive an `env` object with first-class
bindings: `env.SECRETS`, `env.DB`, `env.KV`, plus anything declared in
`ramune.toml`. This document shows how to **add your own bindings**
from Go — `env.QUEUE` backed by NATS, `env.R2` backed by S3,
`env.EMAIL` backed by SMTP, whatever you need.

The pattern is stable across ramune versions: register Go callbacks,
inject a JS facade, optionally augment the TypeScript types.

## Why extend `env`?

Cloudflare Workers exposes bindings (Durable Objects, Queues, R2, AI
Gateway, Service Bindings, Hyperdrive, …) that Ramune does not ship.
You don't need to wait for us to add them: write a binding once in Go
and every Workers-style module on that Runtime sees it on `env`.

Three moving parts:

1. **Go-side callback**(s) — plain `RegisterFunc` entries that do the
   actual work (HTTP call, DB query, file write, …).
2. **JS facade** — a small function that returns the object the
   handler sees on `env.FOO`. Exposed via `WithExtraEnvJS` and the
   `globalThis.__extraEnvBindings(env)` hook.
3. **TypeScript augmentation** — extend the `Env` interface so user
   code type-checks.

## Minimal example: `env.EMAIL`

A write-only binding that accepts `{to, subject, body}` and hands it
to a Go-side mailer. The worker code reads like Workers native:

```ts
await env.EMAIL.send({
  to: "user@example.com",
  subject: "Hi",
  body: "Hello from ramune",
});
```

### 1. Register Go callbacks

```go
package mailbinding

import (
    "encoding/json"
    "fmt"

    "github.com/i2y/ramune"
)

// Sender is whatever actually delivers the mail (SMTP, SendGrid, SES…).
type Sender interface {
    Send(to, subject, body string) error
}

// Install wires the binding into rt. Call before workers.Register.
func Install(rt *ramune.Runtime, s Sender) error {
    return rt.RegisterFunc("__env_email_send", func(args []any) (any, error) {
        if len(args) < 1 {
            return nil, fmt.Errorf("EMAIL.send: opts required")
        }
        b, _ := json.Marshal(args[0])
        var opts struct {
            To, Subject, Body string
        }
        if err := json.Unmarshal(b, &opts); err != nil {
            return nil, err
        }
        return nil, s.Send(opts.To, opts.Subject, opts.Body)
    })
}
```

### 2. Provide the JS facade

```go
// FacadeJS is what you pass to workers.WithExtraEnvJS.
const FacadeJS = `
globalThis.__extraEnvBindings = (function(prev) {
    return function(env) {
        if (prev) prev(env);          // chain with other bindings
        env.EMAIL = {
            send: function(opts) {
                return __env_email_send(opts);
            },
        };
    };
})(globalThis.__extraEnvBindings);
`
```

The IIFE wrapper around `prev` is the **composition pattern**: several
packages can add bindings to the same Runtime without stomping on
each other. Always include it in a reusable package.

### 3. Wire it up

```go
rt, _ := ramune.New(ramune.NodeCompat(), ramune.WithFetch())
_ = mailbinding.Install(rt, mySMTPSender)

handler, _ := workers.Register(rt, "worker.ts", src,
    workers.WithExtraEnvJS(mailbinding.FacadeJS))
http.ListenAndServe(":3000", handler)
```

### 4. Extend the TypeScript types (optional but nice)

Drop this next to your worker:

```ts
// types/email.d.ts
interface EmailOpts { to: string; subject: string; body: string }
interface EmailBinding { send(opts: EmailOpts): Promise<void> }

interface Env {
  EMAIL: EmailBinding;
}
```

`interface Env` merges across declaration files, so this just adds the
`EMAIL` key without redeclaring the built-ins (DB, KV, SECRETS).

## Variations

### Async backend

Returning a Go error rejects the JS promise automatically. If your
backend does I/O (HTTP call, DB query), the callback runs on a Ramune
goroutine while the JS side awaits.

### Binary payloads

JS strings cross the boundary as UTF-8 Go strings. For binary, prefer
base64 at the edge:

```go
rt.RegisterFunc("__env_blob_get", func(args []any) (any, error) {
    key, _ := args[0].(string)
    data, err := s3.Get(key)
    if err != nil { return nil, err }
    return base64.StdEncoding.EncodeToString(data), nil
})
```

JS side decodes with `atob` or `Uint8Array.from(atob(s), c => c.charCodeAt(0))`.

### Namespaced bindings

If your binding is stateful per-namespace (like env.KV), mirror the
`namespace(name)` method that env.KV exposes:

```js
function buildBlob(ns) {
    return {
        get: (k) => __env_blob_get(ns, k),
        put: (k, v) => __env_blob_put(ns, k, v),
        namespace: (n) => buildBlob(n),
    };
}
globalThis.__extraEnvBindings = function(env) {
    env.BLOB = buildBlob("default");
};
```

### Pluggable like WithSQLite

For a binding with well-defined operations (key/value, SQL), the
cleanest API is a Go interface + option pair — that's how `WithSQLite`
is built on `WithKVBackend` + `WithDBBackend` internally. Your
`mailbinding.Sender` interface above is the same shape: user code
supplies the implementation, your package wires the binding.

## Packaging as a reusable module

A `ramune-<foo>` package should export:

- `Install(rt *ramune.Runtime, ...) error` — registers Go callbacks.
- `FacadeJS string` — the JS facade, composed with any earlier
  `__extraEnvBindings` via the IIFE pattern.
- `Types` (optional) — a `//go:embed`-ed `.d.ts` for consumers to copy.

Usage at the call site stays two lines:

```go
foobinding.Install(rt, backend)
workers.Register(rt, ..., workers.WithExtraEnvJS(foobinding.FacadeJS))
```

Callers can stack multiple packages:

```go
mailbinding.Install(rt, mailer)
queuebinding.Install(rt, nats)
blobbinding.Install(rt, s3)

extraJS := mailbinding.FacadeJS + queuebinding.FacadeJS + blobbinding.FacadeJS
workers.Register(rt, ..., workers.WithExtraEnvJS(extraJS))
```

The IIFE `__extraEnvBindings` composition means order does not matter.

## Relationship to the built-in binds

`env.DB` and `env.KV` use the same machinery behind the scenes.
`WithSQLite(path)` is sugar that installs a `KVBackend` and a
`DBBackend`; you can skip it and implement those interfaces yourself
to back env.DB / env.KV with Postgres or Redis instead. See
`workers/backends.go` for the interface definitions.

See [`examples/workers/custom-binding/`](../examples/workers/custom-binding/)
for a runnable project that follows this pattern end to end.
