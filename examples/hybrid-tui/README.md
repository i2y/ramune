# Hybrid AOT for TUI views

`view(model: Model): string` is the natural extraction target for the
hybrid picker — it's a pure function from a struct to a string,
called per render frame. Lowering it to native Go skips the per-call
JSC dispatch.

```bash
cd examples/hybrid-tui
../../ramune compile --hybrid --hybrid-report -o /tmp/tui_view.bin view.ts
```

Expected report (this directory's `view.ts`):

```
picker:
  extracted  const RED
  extracted  const GREEN
  extracted  const CYAN
  extracted  const BOLD
  extracted  const DIM
  extracted  const RESET
  extracted  interface Counter
  extracted  interface Pomodoro
  extracted  function pad2
  extracted  function viewCounter
  extracted  function viewPomodoro
  extracted  function viewSimple
  extracted  function progressBar
  13 extracted, 0 skipped
```

## What makes a view extractable

1. **Inputs are primitives or named interfaces with primitive fields.**
   Models pass through unchanged; the bridge converts JS objects to Go
   structs once per call.
2. **Output is a string.**
3. **Body composes via `+` / template literals — no runtime
   property-access calls.** `Ramune.tui.style(...)` is opaque to the
   picker (it's a property on a runtime global), so views that bake
   ANSI escapes or pre-build style strings via top-level `const`s
   extract; views that defer to `Ramune.tui.style(...)` per render
   stay on the JS floor.
4. **Top-level `const X = <primitive literal>` is reachable.** The
   picker registers each primitive const as its own candidate and
   emits Go-side `var X T = …`, which the extracted functions can
   reference without tripping the closure-capture gate.

## Pairing with the runtime

Once `view()` extracts, you can plug the AOT-compiled module back
into the runtime (`require('native:__hybrid_view__')`) and your
`Ramune.tui.run(...)` view callback becomes a single
`view = require(...).viewCounter` lookup. The TUI loop then bypasses
JS for every render — the BubbleTea goroutine still dispatches to
the JSC thread via `JSFunc.Call`, but the JS-side function it
invokes is now a one-line stub forwarding to the native helper.

## Trade-offs

- The discipline costs you the `<Box>` / `<Text>` JSX ergonomics for
  any view you AOT. A typical project keeps the JSX-flavored view as
  the default and ships an AOT'd hot view next to it.
- `Ramune.tui.style` styling stays JS-only because Lipgloss's
  surface is too wide for the picker today. Workaround: pre-build
  style strings in the TS-runtime startup path, store them in
  top-level constants, and concatenate.
- Glamour-rendered `<Markdown>` output is also JS-only; for static
  markdown in a hot view, render once at startup and cache.
