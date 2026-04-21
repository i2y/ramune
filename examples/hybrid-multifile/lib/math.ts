// Hot kernels split out into a separate module. The picker walks every user
// TS file reachable from the entry, so these still get extracted to native
// Go when compiled with --hybrid.

export function fib(n: number): number {
  if (n < 2) return n;
  return fib(n - 1) + fib(n - 2);
}

export function sumRange(a: number, b: number): number {
  let s = 0;
  for (let i = a; i <= b; i = i + 1) s = s + i;
  return s;
}
