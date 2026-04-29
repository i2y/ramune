# Ramune × BubbleTea — TUI in TSX

A counter demo authored in JSX-flavored TS that drives [BubbleTea](https://github.com/charmbracelet/bubbletea)
+ [Lipgloss](https://github.com/charmbracelet/lipgloss) from inside the
Ramune runtime.

```bash
cd examples/tui-counter
../../ramune run counter.tsx
```

Keys:
- `↑` / `k` / `+` — increment
- `↓` / `j` / `-` — decrement
- `r` — reset
- `q` / `esc` / `ctrl+c` — quit

## How it works

`Ramune.tui.run(opts)` starts a BubbleTea program in a Go goroutine and
returns a Promise. BubbleTea owns the input loop and screen; Ramune
bridges per-tick `update(state, msg)` and `view(state)` calls back to JS
through the JSC dispatch channel. State round-trips as JSON so the
Go-side never has to interpret the model shape.

Lipgloss styling rides through `Ramune.tui.style(text, opts)`. JSX in
`.tsx` lowers to `Ramune.tui.h(component, props, ...children)` — both
intrinsic strings (`<box>`, `<text>`) and capitalized component
references (`<Box>`, `<Text>`) work. Built-in components:

| Component | Behavior |
|---|---|
| `<Box>` | newline-joined children, full Lipgloss style options |
| `<Text>` | concatenated children, inline styling |
| `<Stack gap={n}>` | vertical layout with optional blank-line gaps |
| `<Row gap={n}>` | horizontal layout (space-separated) |
| `<Spacer size={n}>` | pure newline padding |

Style options (passed as JSX props or the second arg to `tui.style`):

| Prop | Type | Notes |
|---|---|---|
| `bold` / `italic` / `underline` / `faint` | `boolean` | |
| `fg` / `bg` | `string` | ANSI color number ("9") or hex ("#ff8800") |
| `padding` / `margin` | `number \| number[]` | Lipgloss variadic semantics |
| `width` / `height` | `number` | |
| `align` | `"left" \| "center" \| "right"` | |
| `border` | `"rounded" \| "thick" \| "double" \| "normal" \| "hidden"` | |

## Why JSX-on-BubbleTea is interesting

BubbleTea is the gold standard for Go TUIs (Charm's Glamour/Wish/Vhs
all sit on it). Ink (the React-for-terminal lib) is the gold standard
for Node-shaped UIs. This wires the two together: Charm's renderer +
React-style component model + the JS runtime ergonomics. Pure
component-shaped views also fit the hybrid AOT picker — `view(model)`
is typically just (Model → string) and lowers cleanly to native Go
when the time comes.

## Cmd shape

`update` returns either:

- a plain new state (most cases), or
- `Ramune.tui.Cmd.quit(state)` to exit the program with that final state

The Promise resolves with whatever `Ramune.tui.Cmd.quit` returned, or
with the latest state when BubbleTea exits on its own (window close,
SIGTERM, etc.).

## Async dispatch

For periodic or async work, use `Ramune.tui.dispatch(msg)` to inject
messages from anywhere. Helpers:

| Helper | Purpose |
|---|---|
| `Cmd.delay(ms, msg)` | one-shot timer; returns a token |
| `Cmd.cancelDelay(token)` | cancel a pending delay |
| `Cmd.every(ms, msg)` | recurring tick; `msg` may be a `() => msg` factory |
| `Cmd.cancelEvery(token)` | stop the recurring tick |
| `Cmd.fromPromise(p, onOk, onFail)` | dispatch the result of a Promise |

See `../tui-clock/clock.tsx` for `Cmd.every` and
`../tui-todo/todo.tsx` for `Cmd.fromPromise`.
