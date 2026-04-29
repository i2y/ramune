# Ramune.tui todo

A small todo list that pulls together every Ramune.tui surface added
in the bubbles pass: `<List>`, `<Input>`, `<Spinner>`, `Cmd.every`
(spinner ticks), and `Cmd.fromPromise` (a fake "sync" pulse).

```bash
cd examples/tui-todo
../../ramune run todo.tsx
```

## Keys

Normal mode:

| Key | |
|---|---|
| `↑` / `k`, `↓` / `j` | move selection |
| `x` / space | toggle done |
| `d` | delete selected |
| `i` | enter input mode |
| `s` | "sync" — fake fetch, watch the spinner |
| `q` / `esc` / `ctrl+c` | quit |

Input mode:

| Key | |
|---|---|
| typing | append at cursor |
| `←` / `→` | move cursor |
| `home` / `end` | line ends |
| `backspace` / `delete` | edit |
| `enter` | commit, returns to normal mode |
| `esc` | cancel, returns to normal mode |

## Patterns to lift

1. **Stateless components**: `<List>` and `<Input>` take state
   through props; the user owns navigation/editing in `update`. The
   `Ramune.tui.input.handleKey` reducer keeps key handling to one
   line per mode.
2. **Spinner driven by external tick**: the spinner is pure render —
   advance via `Cmd.every(80, { type: "spin" })` and bump `spinFrame`
   in update.
3. **Promise → msg bridge**: `Cmd.fromPromise(p, ok, fail)` lifts an
   async result into the dispatch loop so `update` stays sync. Useful
   for fetch, file reads, anything that resolves later.
