# Real-world picker extraction rate

Two hand-written, lodash-shaped / zod-shaped TS modules that exist to
**measure how the `--hybrid` picker fares against real-world code**.
Re-run `./measure.sh` after a picker change to see the delta.

## Latest snapshot (Apr 2026, picker shipped in this commit)

### `lodash_mini.ts` — 16/17 extracted (94%)

Numeric, array and string utilities a project might lift from lodash.

| Pattern | Count | Picker verdict |
|---|---|---|
| Pure numeric (`sum`, `mean`, `clamp`, `inRange`) | 4 | ✅ extracted |
| String build/scan (`repeat`, `padStart`, `startsWithAny`, `nonEmpty`-style) | 3 | ✅ extracted |
| `parseFloat` builtin | 1 | ✅ extracted |
| `Map<string, number>` build / read / size (`tally`, `distinctCount`) | 2 | ✅ extracted |
| Array reshape returning `[]` typed-literal locals (`take`, `drop`, `uniq`, `reverse`) | 4 | ✅ extracted — empty `[]` literal now reads the binding's declared type, and `out.push(x)` on a fresh-literal local survives the receiver-shape gate |
| `s.charAt(i)` palindrome scan | 1 | ✅ extracted — string safelist now covers `charAt` / `charCodeAt` |
| 2D array shape (`chunk` returns `number[][]`) | 1 | ❌ `[object-type] array element must be primitive` — nested arrays are an explicit non-goal in the current emit |

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
