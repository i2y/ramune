# Real-world picker extraction rate

Two hand-written, lodash-shaped / zod-shaped TS modules that exist to
**measure how the `--hybrid` picker fares against real-world code**.
Re-run `./measure.sh` after a picker change to see the delta.

## Latest snapshot (Apr 2026, picker shipped in this commit)

### `lodash_mini.ts` — 9/15 extracted (60%)

Numeric, array and string utilities a project might lift from lodash.

| Pattern | Count | Picker verdict |
|---|---|---|
| Pure numeric (`sum`, `mean`, `clamp`, `inRange`) | 4 | ✅ extracted |
| String build/scan (`repeat`, `padStart`, `startsWithAny`, `nonEmpty`-style) | 3 | ✅ extracted |
| `parseFloat` builtin | 1 | ✅ extracted |
| Array reshape returning `[]` typed-literal locals (`take`, `drop`, `uniq`, `reverse`) | 4 | ❌ `[object-type] array literal: array element must be primitive` — empty `[]` literal lowers to `[]never`, picker doesn't read the surrounding declared type |
| 2D array shape (`chunk` returns `number[][]`) | 1 | ❌ `[object-type] array element must be primitive` — nested arrays are an explicit non-goal in the current emit |
| Unsafelisted string method (`s.charAt(i)` in `isPalindrome`) | 1 | ❌ `[builtin-call] builtin call not in safelist` |

The empty-literal rejection is a **near-miss** that a future picker
pass could fix: when `const out: number[] = []` has a declared type
on the binding, the picker should infer the element type from the
binding instead of from the literal. Lifting this alone would push
lodash_mini's extraction from 60% to ~93%.

### `zod_lite.ts` — 17/17 extracted (100%)

Input validators in the shape of zod schemas.

| Pattern | Count | Picker verdict |
|---|---|---|
| Numeric range / `Number.isInteger` / `Number.isFinite` | 3 | ✅ |
| String checks (`startsWith`, `endsWith`, `length`-bounds) | 4 | ✅ |
| `looksLikeEmail` (`indexOf`, length math) | 1 | ✅ |
| Array `for` with elementwise pred | 2 | ✅ |
| Composite struct (`isAdult(u: User)`, `isReasonable(o: Order)`) | 2 | ✅ — nested-struct support extracts these |
| Literal union return (`Kind = "user" \| "guest"`) | 1 | ✅ — typemapper unwraps to `string` |
| Nullable input (`name: string \| null`, `age: number \| undefined`) | 2 | ✅ — `T \| null` lowers to `*T` |

Validator code is the picker's **best-fit shape today**. Every function
that wraps `string`/`number`/struct args with branching logic and
returns `boolean` extracts cleanly.

## Run it yourself

```bash
make build-cli                       # ensure ramune binary is fresh
cd examples/hybrid-realworld
./measure.sh
```

Pick at the per-function detail under "Reject reasons" to spot which
constructs in your own code base would and wouldn't extract.

## What this measures (and what it doesn't)

The numbers above are **picker acceptance**, not runtime speedup. Every
extracted function pays a fixed JS↔Go bridge cost per call, so:

- A function called 10 K times in a tight loop with primitive args will
  go several × faster (recursive numerics, hash kernels).
- A function called once per request that does ~1 µs of work may
  *regress* — the bridge cost dominates.

Use `--hybrid-report` plus a wrk / hyperfine bench to verify the
extraction is actually a win on your workload.
