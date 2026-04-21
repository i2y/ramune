// Hono + --hybrid demo
//
// The Hono routing, middleware, and request/response plumbing stay on the JS
// floor (they use Map, Headers, Request — all outside the picker safelist).
// The CPU-heavy handlers call into small, typed kernel functions that the
// picker extracts to native Go.
//
// Build:
//
//   ramune add hono              # once, installs node_modules/hono
//   ramune compile app.ts --http --hybrid --hybrid-report -o server
//   ./server                      # listens on :3001 (override with PORT=3030)
//
// Try:
//
//   curl http://localhost:3001/
//   curl http://localhost:3001/fib/30
//   curl http://localhost:3001/primes/10000
//   curl http://localhost:3001/health
//
// Benchmark against the JS-only build with `./run.sh`.

import { Hono } from "hono";

// === Extractable hot kernels ===
// The picker walks THIS file (the compile entry) and extracts each of these
// to a native Go function. At runtime, calls from the Hono handlers resolve
// through globalThis to the native version after the shim installs.

export function fib(n: number): number {
  if (n < 2) return n;
  return fib(n - 1) + fib(n - 2);
}

export function isPrime(n: number): boolean {
  if (n < 2) return false;
  if (n === 2) return true;
  if (n % 2 === 0) return false;
  for (let i = 3; i * i <= n; i = i + 2) {
    if (n % i === 0) return false;
  }
  return true;
}

export function countPrimes(limit: number): number {
  let count = 0;
  for (let i = 2; i < limit; i = i + 1) {
    if (isPrime(i)) count = count + 1;
  }
  return count;
}

// === Hono app — stays on the JS floor ===

const app = new Hono();

app.get("/", (c) =>
  c.text(
    "Hono + Ramune hybrid extraction\n" +
      "\n" +
      "Routes:\n" +
      "  GET /fib/:n           compute fibonacci(n) in native Go\n" +
      "  GET /primes/:limit    count primes below `limit` in native Go\n" +
      "  GET /health           liveness probe\n",
  ),
);

app.get("/fib/:n", (c) => {
  const n = parseInt(c.req.param("n"), 10);
  if (Number.isNaN(n) || n < 0 || n > 40) {
    return c.json({ error: "n must be an integer in [0, 40]" }, 400);
  }
  return c.json({ n, fib: fib(n) });
});

app.get("/primes/:limit", (c) => {
  const limit = parseInt(c.req.param("limit"), 10);
  if (Number.isNaN(limit) || limit < 0 || limit > 1_000_000) {
    return c.json({ error: "limit must be an integer in [0, 1_000_000]" }, 400);
  }
  return c.json({ limit, count: countPrimes(limit) });
});

app.get("/health", (c) => c.json({ ok: true }));

// === Start the server ===

declare const Ramune: {
  serve(opts: { port: number; fetch: (req: Request) => Response | Promise<Response> }): unknown;
};

const port = parseInt((globalThis as any).process?.env?.PORT ?? "3001", 10);
Ramune.serve({
  port,
  fetch: (req) => app.fetch(req),
});
