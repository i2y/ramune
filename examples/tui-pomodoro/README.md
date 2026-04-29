# Ramune.tui pomodoro

25/5 work/break cycle that wires Timer + Stopwatch + keymap + Help +
Progress + Cmd.every into ~120 lines of TSX.

```bash
cd examples/tui-pomodoro
../../ramune run pomodoro.tsx
```

## Keys

| Key | |
|---|---|
| `space` / `p` | start / pause both timer and total stopwatch |
| `r` | reset the current phase to its full duration |
| `s` | skip to the next phase (auto-advances the cycle counter on work→break) |
| `q` / `esc` / `ctrl+c` | quit |

## Surfaces on display

- **`Ramune.tui.timer`** — wall-clock-anchored countdown. `tick(state)`
  returns expired=true when the remaining hits 0 so `update` can chain
  the next phase without an external scheduler.
- **`Ramune.tui.stopwatch`** — same anchoring, counts up. Used here
  for "total session time" so pause/resume works without drift.
- **`Ramune.tui.keymap`** — bindings declared once, used both to gate
  `update` (`keys.matches(msg, 'toggle')`) and to feed `<Help>`'s
  entries (`keys.helpEntries()`). The two stay in sync automatically.
- **`<Timer>` / `<Stopwatch>`** — both stateless, take a count in ms.
  Timer's `warningAt` swaps in `warningStyle` when remaining drops
  below the threshold (turning the digits red as time runs low).
- **`<Progress>`** — reads the elapsed ratio for the current phase.
- **`<Help>`** — picks up help labels from the keymap so pressing any
  key shows the same shortcuts the matcher accepts.
