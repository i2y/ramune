# Hono + Hybrid Extraction

Demonstrates `ramune compile --hybrid` applied to a real web framework. The
Hono routing, middleware, and request/response plumbing stay on the JS floor
— the picker rejects them because they use `Map`, `Headers`, `Request`,
closures over captured state, etc. The CPU-heavy route handlers call small
typed kernel functions that the picker _does_ extract to native Go.

## Files

- `app.ts` — Hono app with two compute-heavy routes (`/fib/:n`, `/primes/:limit`)
  and a `/health` control endpoint.
- `package.json` — declares `hono` as a dependency.
- `run.sh` — installs deps, builds four binaries (JSC / qjswasm × JS-only /
  hybrid), and runs a `wrk` benchmark across the routes.

## Run it

```sh
ramune add hono   # or: ramune install   (reads package.json)
./run.sh
```

Or manually:

```sh
ramune compile app.ts --http                         -o server-js
ramune compile app.ts --http --hybrid --hybrid-report -o server-hybrid
ramune compile app.ts --http --tags qjswasm          -o server-js-qjs
ramune compile app.ts --http --tags qjswasm --hybrid -o server-hybrid-qjs
./server-hybrid &                        # listens on :3001
curl http://localhost:3001/fib/30
curl http://localhost:3001/primes/10000
```

## Picker report

```
  extracted  function fib
  extracted  function isPrime
  extracted  function countPrimes
  3 extracted, 0 skipped
```

Hono itself doesn't show up in the picker output because `--hybrid-report`
only enumerates top-level functions in the entry file. Inside each handler
body, calls to `fib(n)` / `countPrimes(limit)` bind to module-scope — which
the shim rewires to the native Go functions at boot.

## Results (Apple M4 Max, wrk -t2 -c50 -d5s)

### JSC + JIT (default)

```
endpoint               JS req/s     Hybrid req/s ratio
-----------------------------------------------------------
/fib/30                314          484          1.54x
/fib/20                12302        13433        1.09x
/primes/10000          10666        331          0.03x   (32x SLOWER)
/primes/1000           8641         6445         0.75x   (1.3x slower)
/health                6701         6510         ~1.0x   (control)
```

### qjswasm (pure-Go, no JIT)

```
endpoint               JS req/s     Hybrid req/s ratio
-----------------------------------------------------------
/fib/30                5.3          457          86.3x
/fib/20                609          6046         9.9x
/primes/10000          166          323          1.95x
/primes/1000           2105         4029         1.91x
/health                8449         8479         ~1.0x   (control)
```

## Reading the results

**`/fib/30` is the hybrid-extraction win in its purest form.** Recursive
calls with small integer arguments. JSC's JIT doesn't optimise recursion as
aggressively as straight-line arithmetic, so native Go wins 1.54× even at the
top of the JIT's skill ladder. On qjswasm (interpreter, no JIT), the same
kernel wins **86×** — that's the real ceiling of hybrid extraction.

**`/primes/10000` regresses 32× on JSC.** The kernel is a tight loop of
`n % i === 0` checks. JSC's JIT detects that `n` and `i` are always integer at
runtime and emits a single-cycle integer modulo. The native Go emitted by the
transpiler uses `math.Mod` — the IEEE-754 float remainder, ~10 ns per call,
software-implemented — because `number` in TypeScript is float64 and the
transpiler can't prove the operand is integer without wider static analysis.
`countPrimes(10000)` makes ~500k `math.Mod` calls → ~5 ms of pure Go work
per request, where the JIT-compiled JS does it in ~100 µs. On qjswasm the
picture flips: the JS baseline is an interpreter with no integer-modulo
specialisation, so the native Go path wins 2×.

**`/health` is a control.** It returns a static `{ok:true}` — no extractable
kernel involved. Throughput is unchanged between JS-only and hybrid builds,
confirming the JS floor + framework path is untouched.

## Takeaway

For Hono-on-JSC, `--hybrid` is a targeted tool: use it for routes whose hot
path is recursion-heavy or otherwise outside JSC's JIT sweet spot; don't reach
for it when the kernel is "tight integer loop with `%`" because JSC JIT is
hard to beat there.

For Hono-on-qjswasm (or any no-JIT backend — `goja`, `FROM scratch` Docker,
Windows), `--hybrid` is close to a free speedup. Every CPU-heavy extractable
kernel went 2×-86× faster in the table above, and the ones it _couldn't_
extract (Hono routing, response JSON) ran at exactly the same speed as before.
