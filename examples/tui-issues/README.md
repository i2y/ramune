# Ramune.tui issues

Showcase demo wiring together Tabs, List, Viewport, Help, Progress,
Textarea, Spinner, and Cmd.{every,delay} into one screen — a mock
issue browser with reply composition.

```bash
cd examples/tui-issues
../../ramune run issues.tsx
```

## Keys

Browse mode:

| Key | |
|---|---|
| `1` / `2` / `3` | Open / Closed / All tabs |
| `tab` | cycle tabs |
| `j` / `k` | move selection |
| `J` / `K` | scroll the issue body |
| `r` | reply (enter compose mode) |
| `q` / `esc` | quit |

Compose mode:

| Key | |
|---|---|
| typing | edit draft |
| `←` `→` `↑` `↓` | navigate cursor |
| `home` / `end` / `backspace` / `delete` | edit |
| `enter` | newline |
| `ctrl+s` / `ctrl+enter` | post (fake — runs the progress bar) |
| `esc` | cancel and return to browse |

## Components on display

- **Tabs**: `<Tabs labels selected />`
- **List**: `<List items selected maxRows selectedStyle width />` with
  the tab selection driving which subset shows.
- **Viewport**: `<Viewport content offset height width />` shows the
  selected issue body and supports J/K scrolling within it.
- **Help**: `<Help keys mode />` gives mode-aware shortcuts.
- **Textarea**: `<Textarea value cursor rows cols />` for the reply
  draft, with `Ramune.tui.textarea.handleKey` driving edits.
- **Progress + Spinner** combined to indicate the fake "post" run.

The data is hard-coded in `ISSUES`; swap that for an `await fetch(...)`
inside `init` to drive a real backend.
