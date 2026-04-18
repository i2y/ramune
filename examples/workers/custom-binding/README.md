# Custom `env` binding: `env.EMAIL`

Demonstrates how a Go embedder adds a binding that Ramune itself does
not ship. The same pattern works for queues, object stores, caches,
AI providers — any `env.FOO` you want.

## Run

```bash
go run .
# in another terminal:
curl -X POST http://localhost:3333/notify \
    -H "Content-Type: application/json" \
    -d '{"user":"alice@example.com","body":"welcome"}'
```

The Go server prints each `EMAIL.send` call on stdout:

```
[EMAIL] to=alice@example.com subject="Hello from Ramune" body="welcome"
```

## What to look at

- `main.go` — the Go embedder.
  - `installEmailBinding` registers `__env_email_send` via
    `rt.RegisterFunc`. That's the actual Go → JS bridge.
  - `emailFacadeJS` is passed to `workers.WithExtraEnvJS`; it attaches
    `env.EMAIL = { send(opts) { ... } }` via the
    `globalThis.__extraEnvBindings(env)` composition hook.
  - `workers.Register` wires the worker up; no Ramune changes needed.
- `worker.ts` — the Workers-style handler uses `env.EMAIL.send(...)`
  exactly like it would use `env.KV.get(...)`.
- `workers-env.d.ts` — declares the `Env.EMAIL` type so the editor
  and `ramune check` see the binding.

## Reuse as a Go package

The `installEmailBinding` + `emailFacadeJS` pair is everything a
reusable `ramune-<x>` package needs to expose. Keep the IIFE composition
around `prev` in `emailFacadeJS` so callers can stack bindings from
multiple packages without collisions:

```go
mailbinding.Install(rt, smtp)
queuebinding.Install(rt, nats)
blobbinding.Install(rt, s3)

extraJS := mailbinding.FacadeJS + queuebinding.FacadeJS + blobbinding.FacadeJS
workers.Register(rt, ..., workers.WithExtraEnvJS(extraJS))
```

See [`workers/BINDINGS.md`](../../../workers/BINDINGS.md) for the full
guide.
