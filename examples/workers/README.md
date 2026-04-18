# Workers-style examples

Each `.ts` file exports a default object with an optional `route` plus
a `fetch` / `scheduled` function — the Cloudflare Workers ES-module
handler shape. Run any of them with:

```shell
ramune serve <file>.ts
```

| File | Demonstrates |
|------|--------------|
| `hello.ts` | Minimal fetch handler, `Response.json`, URL query params. |
| `waituntil.ts` | `ctx.waitUntil` — response returns immediately, background promise keeps running. |
| `sse.ts` | Streaming response via `ReadableStream` (Server-Sent Events). |
| `hono-hello.ts` | Using Hono as the fetch handler (requires the npm dep declared in `ramune.toml`). |

The `workers.d.ts` file in `../../workers/workers.d.ts` declares
global `WorkersHandler`, `Env`, `ExecutionContext`, etc. Copy it into
your project or reference it via a triple-slash directive to get
type-checked in your editor.

## env.DB / env.KV

These bindings are opt-in on the Go side via `workers.WithSQLite(path)`.
`ramune serve` creates `.ramune/data.db` by default — pass
`--no-sqlite` to disable, or `--sqlite ./mydb.db` to override.

Secrets come from environment variables prefixed with `RAMUNE_SECRET_`:

```shell
export RAMUNE_SECRET_OPENAI_KEY=sk-...
ramune serve my-worker.ts
```
