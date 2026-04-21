// Entry that composes kernels from `./lib/math` and `./lib/format`.
// Compile with:
//   ramune compile app.ts --hybrid --hybrid-report -o demo
//   ./demo
//
// `--hybrid-report` will list every extracted function, across source files,
// and you'll see that `fib` / `sumRange` / `fmtPoint` / `repeat` are all
// lifted to native Go even though they're declared in sibling modules.

import { fib, sumRange } from "./lib/math";
import { fmtPoint, repeat } from "./lib/format";

export function summarise(n: number): string {
  return "fib(" + n + ")=" + fib(n) +
    ", sum(1.." + n + ")=" + sumRange(1, n) +
    ", pt=" + fmtPoint(1.2, 6.7);
}

console.log(repeat("=", 40));
console.log("Multi-file hybrid demo");
console.log(repeat("=", 40));
console.log(summarise(10));
console.log(summarise(20));
