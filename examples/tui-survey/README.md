# Ramune.tui survey

A two-step survey that exercises every form field type, key chord
sequences, and the glamour-powered `<Markdown>` summary card.

```bash
cd examples/tui-survey
../../ramune run survey.tsx
```

## Keys

Form step:

| Key | |
|---|---|
| typing | edit text/textarea |
| `tab` / `shift+tab` | next/prev field |
| `←` / `→` (`h` / `l`) | switch select / toggle confirm |
| `space` / `x` | toggle multi-select item |
| `g g` | jump to the first field (chord) |
| `G` | jump to the last field |
| `enter` on the last field | validate + submit |

Review step:

| Key | |
|---|---|
| `e` / `esc` | back to edit |
| `q` / `ctrl+c` | quit (final values print to stdout) |

## Surfaces on display

- **`Ramune.tui.Form` + reducer** — the entire form state lives in
  one object, the keystrokes route through `form.handleKey`. Each
  field type (text, textarea, select, multi, confirm) shares the
  same call site.
- **`Ramune.tui.keymap` chord sequences** — `g g` is matched as a
  two-key sequence with an 800 ms timeout. `keymap.match(msg)`
  returns the binding name when complete; the form's tab/letter
  handling stays oblivious to the in-flight chord.
- **`<Markdown>`** — the review screen renders the user's responses
  as Markdown styled by glamour's "dark" theme: heading, blockquote,
  bold, horizontal rule, inline code.
- **Validation gate** — the `name` field is `required: true`. Empty
  submission writes an error and refocuses the offending field.
