# Markdown theme switcher

A live cycle through every glamour-bundled preset to compare how the
same Markdown source renders under each. Useful when picking a theme
for a documentation viewer or chat-style TUI.

```bash
cd examples/tui-themes
../../ramune run themes.tsx
```

## Available themes

| Theme | Behavior |
|---|---|
| `auto` | Auto-detects the terminal's bg color (defaults to `dark`-style on most modern terms; falls back to `ascii` when stdout isn't a TTY) |
| `dark` | Charm's stock palette tuned for dark backgrounds |
| `light` | Charm's stock palette tuned for light backgrounds |
| `pink` | Magenta-leaning warm palette |
| `dracula` | The popular Dracula scheme (purple/cyan/pink) |
| `tokyo-night` | Tokyo Night (deep blue/violet) |
| `ascii` | Plain ASCII — no ANSI sequences |
| `notty` | Plain text with minimal formatting (for piped output) |

## Keys

| Key | |
|---|---|
| `←` / `h` / `shift+tab` | previous theme |
| `→` / `l` / `tab` | next theme |
| `q` / `esc` / `ctrl+c` | quit |

## Custom themes

`Ramune.tui.markdown(text, { theme, width })` accepts any name that
[charmbracelet/glamour](https://github.com/charmbracelet/glamour)
recognizes. To ship your own palette, point the embedding host at a
JSON theme file via glamour's `WithStylesFromJSONFile` API and expose
it as a custom name through a small wrapper module — out of scope
for this demo, but the path is ready for v2.
