# Hybrid Extraction Example

Demonstrates `ramune compile --hybrid`: the compiler walks the app, extracts
every top-level function / pure class whose signature and body are inside a
statically-verified subset, and compiles them to native Go. Whatever it can't
extract keeps running on the JS floor.

## Files

- `app.ts` — four workloads plus a bench harness.
- `run.sh` — builds four binaries (JSC / qjswasm × JS-only / hybrid) and runs
  them side by side.

## Run it

```sh
./run.sh
```

Or manually:

```sh
ramune compile app.ts                         -o bench-js
ramune compile app.ts --hybrid                -o bench-hybrid
ramune compile app.ts --tags qjswasm          -o bench-js-qjs
ramune compile app.ts --tags qjswasm --hybrid -o bench-hybrid-qjs
```

## Picker report

`--hybrid-report` prints what the picker decided, per function. On this file:

```
  extracted  function fib
  extracted  function isPrime
  extracted  function countPrimes
  extracted  function sumSquares
  skipped    function bucketSign  [object-type]  local `buckets`: object/reference type not supported
  skipped    function bench       [object-type]  param 2 (fn): object/reference type not supported
  4 extracted, 2 skipped
```

`bucketSign` uses `Map`, which is outside the picker's safelist — it stays on
the JS floor. `bench` takes a callback whose return type is `unknown`, which
the picker also declines. Both still run (as JS) in the hybrid binary.

## Results (Apple M4 Max)

```
### JSC + JIT (default) ###
workload                          JS-only      Hybrid       Ratio
-----------------------------------------------------------------
fib(30)                          3107 us/op   1998 us/op    1.55x
countPrimes(10000)                 48 us/op   2935 us/op    0.02x  (60x SLOWER)
sumSquares(1000 arr)             0.95 us/op   98.2 us/op    0.01x  (103x SLOWER)
bucketSign(1000) [JS floor]      5.93 us/op   5.66 us/op    1.0x   (not extracted)

### qjswasm (pure-Go, no JIT) ###
workload                          JS-only      Hybrid       Ratio
-----------------------------------------------------------------
fib(30)                        185771 us/op   2008 us/op   92.5x
countPrimes(10000)               5849 us/op   2862 us/op    2.0x
sumSquares(1000 arr)             65.3 us/op    204 us/op    0.32x  (3x SLOWER)
bucketSign(1000) [JS floor]       281 us/op    283 us/op    1.0x   (not extracted)
```

## Reading the results

**`--hybrid` is a big win on no-JIT backends (qjswasm, goja).** `fib(30)` goes
92× faster on qjswasm because the native Go recursion destroys interpreted
QuickJS-NG. `countPrimes`, `isPrime`, even array kernels that lose on JSC all
pick up 2×+ on qjswasm. If your deployment target is a pure-Go `FROM scratch`
container or a Windows binary — where JSC isn't available — `--hybrid` is a
meaningful accelerant almost for free.

**On JSC + JIT the picture is mixed.** JSC's JIT aggressively specialises
integer-typed JavaScript (it notices `n % 2` and emits a single-cycle integer
modulo; it notices loops and unrolls them). The naive Go emitted by the
transpiler uses `math.Mod` (IEEE-754 float remainder, software-implemented,
~10 ns per call) for `%` on `number` operands because JS `%` is float-correct
by default. For `countPrimes`, that single cost difference makes the native
Go path 60× slower than JSC's JIT-compiled JS. `fib(30)` still wins 1.55× —
recursive function calls are an area JSC's JIT handles less well.

**Array marshalling is the other cliff.** `sumSquares(xs)` crosses the JS/Go
boundary once per call, but has to convert a 1000-element JavaScript array to
`[]float64` on each crossing. That marshalling cost (~100 ns per element)
dominates the ~1 µs of body work; on JSC+JIT the hybrid version is 103× slower
than staying in JS. On qjswasm the JS baseline is slower, so the regression is
only 3×, but the sign is the same.

**Rejected functions keep working.** `bucketSign` uses `Map`, which the picker
refuses to extract. The hybrid binary still runs it — at exactly the same
speed as the JS-only build, because it _is_ the JS-only version. No silent
skip, no broken behavior.

## Binary size

The extracted Go adds to the compiled binary. Measured on this example
(4 functions + 1 class):

```
             JSC           qjswasm
JS-only     28.64 MB      34.24 MB
Hybrid      28.66 MB      34.26 MB
delta       +17 KB        +17 KB
```

The 28-34 MB baseline is dominated by the embedded ramune runtime; the
extraction itself is a rounding-error contribution for typical kernel
sizes. The Hono example (3 extracted functions) measured +1 KB; the
multi-file example (5 extracted functions across 3 files) measured +17 KB.

## Rules of thumb

The picker proves semantic equivalence, not speed. Two patterns to watch for
before reaching for `--hybrid`:

- **Backend choice dominates.** If you're deploying on JSC, treat `--hybrid`
  as a targeted tool (recursive functions, complex control flow), not a
  blanket win. If you're deploying on qjswasm / goja, `--hybrid` is close to
  a free speedup for any CPU-heavy extractable kernel.
- **Measure per-endpoint when arrays or per-call-frequency is high.** Integer
  modulo in tight loops and array-arg marshalling are the two known regression
  sources on JSC. For everything else, `--hybrid-report` + a quick `wrk` tells
  you whether the extraction was worth it for that specific function.
