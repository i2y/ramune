# TUI view() benchmark — hybrid AOT vs JSC

A negative-result writeup. Lowering TUI `view()` functions through the
hybrid AOT picker is **slower** than running them under JSC's JIT for
every workload tested.

## Run it yourself

```bash
cd bench
../ramune compile --hybrid -o tui_view_hybrid tui_view.ts
../ramune compile          -o tui_view_jsc    tui_view.ts
./tui_view_jsc
./tui_view_hybrid
```

Both binaries embed the same `tui_view.ts`; they differ only in
whether the picker extracts the view functions to native Go or leaves
them on the JS floor.

## Numbers (M4 Max, ramune 0.21.0, Apr 2026)

| Workload | JSC ns/op | Hybrid ns/op | JSC speedup |
|---|---:|---:|---:|
| `viewTiny(n, label)` | 20 | 4115 | **205×** |
| `viewCounter(model)` | 80 | 5080 | **64×** |
| `progressBar(value, max, 40)` | 260 | 5240 | **20×** |
| `gridRender 4×40` | 1050 | 11500 | **11×** |
| `gridRender 16×80` | 7000 | 68000 | **10×** |
| `gridRender 32×120` | 22000 | 214000 | **10×** |

Grid is `rows×cols` cells of arithmetic + concat per call. The 32×120
case is 3840 cells per call — JSC still wins by 10×.

## Why JSC wins

The hybrid path's per-call cost decomposes into:

```
total = ffi_bridge (~5 µs) + native_body
```

The 5 µs floor matches every "tiny call" measurement: viewTiny / viewCounter /
progressBar all hit ~5 µs/op regardless of body size. Only when the body
work climbs above the floor does we see the bridge cost amortize —
gridRender 32×120's native body is ~209 µs which dominates the 5 µs FFI.

But JSC's JIT crushes the same body in **22 µs** for that workload —
roughly **10× faster than the native code**. JSC's string-concat fast
paths plus inlined arithmetic in a tight loop just outrun the
straight-line Go we emit. We probably leave a lot of optimization on
the table — `out = out + line + "\n"` lowers to `out += line + "\n"`
which Go's runtime handles via per-step allocate-and-copy, while JSC
typically grows ropes / cords lazily.

## When does AOT win

Compute-heavy, function-call-heavy workloads where the bridge cost
amortizes and JSC's call-overhead matters:

- `fib(40)` — ~2.3 billion recursive calls. AOT wins here (existing
  bench under `bench/run.sh` shows the speedup).
- Cryptography-style kernels — hash, scrypt, etc.
- Tight numeric loops without string concat.

For `view()` specifically: **don't bother** lowering to AOT today.
JSC delivers 10× better throughput at every render scale tested.

## Implications for Ramune.tui

The JS path through `Ramune.tui.run` already serializes Update/View
through JSFunc.Call (one FFI hop per render frame), so even at 60 Hz
we're at ~5 µs/frame of bridge cost — invisible. The JIT-optimized
JS body runs at ns scale on top of that, well below the typical
30 µs/frame BubbleTea redraw budget. Keep `view()` in TS, keep the
`<Box>` / `<Text>` JSX surface for ergonomics.

## Future directions if AOT-for-views ever becomes worth it

1. **Bypass the FFI bridge for hot paths** — emit a Go-shape
   in-process call that doesn't go through `purego` / cgo per call.
   Would close the 5 µs floor down to ns-scale.
2. **Smarter string emit** — Go's emit chains `s = s + c1 + c2`
   into a single `strings.Builder` over the loop body. Maybe a 2–3×
   improvement; still won't beat JSC's rope strings for the typical
   render size.
3. **TinyGo wasm view rendering** — different runtime, different
   trade-offs. Worth a follow-up bench.

The bench fixture (`tui_view.ts`) is small enough to evolve as we
explore those.
