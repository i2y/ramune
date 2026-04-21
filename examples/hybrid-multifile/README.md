# Multi-file Hybrid Extraction

Shows that `--hybrid` walks every user TS file reachable from the entry's
import graph. The hot kernels live in `lib/math.ts` and `lib/format.ts`;
`app.ts` imports from both. All four kernel functions get extracted to
native Go even though none of them are declared in the entry file itself.

## Files

```
app.ts              # entry — imports kernels, runs them
lib/math.ts         # fib, sumRange
lib/format.ts       # fmtPoint, banner
```

## Run

```sh
ramune compile app.ts --hybrid --hybrid-report -o demo
./demo
```

Expected `--hybrid-report` output (the header points at one file but the
candidates list is the merged picker result across all user TS files):

```
picker: .../lib/math.ts
  extracted  function fib
  extracted  function sumRange
  extracted  function fmtPoint
  extracted  function repeat
  extracted  function summarise
  5 extracted, 0 skipped
```

Expected stdout:

```
========================================
Multi-file hybrid demo
========================================
fib(10)=55, sum(1..10)=55, pt=(1.2, 6.7)
fib(20)=6765, sum(1..20)=210, pt=(1.2, 6.7)
```

## How it works

When you pass `app.ts` to `ramune compile --hybrid`, the composer builds a
`tsgo` Program, enumerates every user SourceFile reachable from the entry
(excluding `.d.ts` and anything under `node_modules`), and runs the picker on
each with a shared top-level function name set. Cross-file calls between
extractable functions are accepted.

All extracted functions end up in one merged Go package (`native_app`) and
get installed on `globalThis` via the same boot-time shim used by the
single-file variant.
