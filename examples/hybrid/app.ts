// Hybrid extraction demo
//
// Compile this file two ways to compare:
//
//   ramune compile app.ts            -o bench-js       # pure JS
//   ramune compile app.ts --hybrid   -o bench-hybrid   # extract typed hot paths
//   ramune compile app.ts --hybrid --hybrid-report -o bench-hybrid
//     # (--hybrid-report prints which functions the picker extracted vs skipped)
//
// Run `./run.sh` for a side-by-side benchmark across both JSC and qjswasm
// backends.

// === Extractable: small-arg, CPU-heavy kernels ===
// The picker lifts these into native Go. Because the input is a single
// primitive and the body does meaningful work, the JS→Go bridge cost is
// amortised and you get a real speedup.

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

// === Extractable but the shape is a bridge-cost trap ===
// Same picker rules accept this — body is provable-safe — but each call has
// to marshal a 1000-element array across the JS/Go boundary. On JSC+JIT the
// native version is actually SLOWER than staying in JS; on qjswasm it's still
// slower, just less dramatically. Good teaching case: hybrid ≠ free win.

export function sumSquares(xs: number[]): number {
  let total = 0;
  for (const x of xs) total = total + x * x;
  return total;
}

// === Not extractable — stays on the JS floor ===
// The picker rejects this because `Map` is an object/reference type outside
// its safelist. The binary still runs the function; it just runs as JS.
// `--hybrid-report` shows it with reason code `object-type`.

export function bucketSign(xs: number[]): string {
  const buckets = new Map<string, number>();
  for (const x of xs) {
    const key = x < 0 ? "neg" : x === 0 ? "zero" : "pos";
    buckets.set(key, (buckets.get(key) ?? 0) + 1);
  }
  const parts: string[] = [];
  for (const [k, v] of buckets.entries()) parts.push(k + "=" + v);
  return parts.join(",");
}

// === Bench harness (stays on the JS floor) ===

function bench(label: string, iters: number, fn: () => unknown): void {
  for (let i = 0; i < 5; i = i + 1) fn(); // warm-up
  const t0 = performance.now();
  for (let i = 0; i < iters; i = i + 1) fn();
  const dt = performance.now() - t0;
  const usPerOp = (dt / iters) * 1000;
  console.log(
    `${label.padEnd(26)}${dt.toFixed(2).padStart(10)} ms${usPerOp.toFixed(2).padStart(12)} us/op`,
  );
}

const ys = Array.from({ length: 1000 }, (_, i) => i * 2 - 500);

// The compile shim is appended at the end of the bundle and swaps the
// extracted exports on globalThis. Deferring the bench via setTimeout(0)
// lets the shim run first so the closures pick up the native versions.
setTimeout(() => {
  console.log("=== Ramune hybrid-extraction bench ===\n");
  console.log("workload                         time         us/op");
  console.log("-----------------------------------------------------------");
  bench("fib(30)", 20, () => fib(30));
  bench("countPrimes(10000)", 50, () => countPrimes(10000));
  bench("sumSquares(1000 arr)", 10000, () => sumSquares(ys));
  bench("bucketSign(1000) [JS]", 5000, () => bucketSign(ys));
}, 0);
