# Ramune.tui clock

A live digital clock that ticks every second. Demonstrates
`Ramune.tui.Cmd.every(ms, msg)` for periodic dispatch — the same pattern
you'd use for animation frames, polling timers, or watchdog refreshes.

```bash
cd examples/tui-clock
../../ramune run clock.tsx
```

Press `q` to quit. The Promise resolves with the final state, including
how many ticks elapsed.

## How periodic dispatch works

`Cmd.every(intervalMs, msg)` is sugar over `setInterval` that
dispatches `msg` (or `msg()` if it's a factory) into the running
program every `intervalMs` ms. It returns a token; pass it to
`Cmd.cancelEvery(token)` to stop the timer — typically inside the
Promise's `.then` so the JS event loop drains cleanly after the TUI
exits.

Because `dispatch` injects directly via `tea.Program.Send`, ticks reach
your `update(state, msg)` exactly the same way keystrokes do — no
special handling needed beyond pattern-matching `msg.type === "tick"`.
