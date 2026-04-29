# Ramune.tui demos

Charm's [Bubbletea](https://github.com/charmbracelet/bubbletea) +
[Lipgloss](https://github.com/charmbracelet/lipgloss) +
[bubbles](https://github.com/charmbracelet/bubbles) +
[glamour](https://github.com/charmbracelet/glamour) +
[wish](https://github.com/charmbracelet/wish), authored in TSX and
driven by Ramune. Every demo runs with `../../ramune run <file>`.

| Demo | What it shows |
|---|---|
| [`tui-counter`](./tui-counter) | The minimum viable Bubbletea app: init/update/view + Lipgloss styling |
| [`tui-clock`](./tui-clock) | `Cmd.every` driving a 1-second tick |
| [`tui-todo`](./tui-todo) | `<List>`, `<Input>`, `<Spinner>`, `Cmd.fromPromise` |
| [`tui-pomodoro`](./tui-pomodoro) | `<Timer>`, `<Stopwatch>`, `keymap`, `<Help>`, `<Progress>` |
| [`tui-issues`](./tui-issues) | `<Tabs>`, `<Viewport>`, `<Help>`, `<Textarea>` — issue browser mock |
| [`tui-survey`](./tui-survey) | `<Form>` (text/textarea/select/multi/confirm) + `<Markdown>` summary + chord sequences (`g g`) |
| [`tui-ssh`](./tui-ssh) | `Ramune.tui.serveSSH` — a Bubbletea app served over SSH via wish |
| [`tui-themes`](./tui-themes) | Live cycle through every glamour preset (`dark`/`light`/`pink`/`dracula`/`tokyo-night`/...) for `<Markdown>` |
| [`hybrid-tui`](./hybrid-tui) | Pure-string `view()` extraction through the hybrid AOT picker — see what lowers to native Go |

## Recording GIFs with VHS

Each demo ships a `.tape` script driving
[charmbracelet/vhs](https://github.com/charmbracelet/vhs) for
reproducible recordings:

```bash
brew install vhs   # or see the vhs README

# render all demos at once
make tui-gifs

# or one demo at a time
cd examples/tui-counter && vhs counter.tape
```

GIFs land next to their `.tape`. Tweak the tape's `Set Width / Height /
FontSize / Theme` to suit your terminal preset before reshooting.

## API surface in one place

```ts
declare const Ramune: any;

// Programs
Ramune.tui.run({ init, update, view, fullscreen?, mouse?, headless? }): Promise<state | { state, output }>
Ramune.tui.serveSSH({ addr, hostKeyPath?, init, update, view, onStart? }): Promise<void>
Ramune.tui.test({ init, update, view, script }): { frames, states, finalState, quit }

// Cmd surface
Ramune.tui.dispatch(msg)
Ramune.tui.Cmd.quit(state)
Ramune.tui.Cmd.delay(ms, msg)
Ramune.tui.Cmd.every(ms, msg | () => msg)
Ramune.tui.Cmd.fromPromise(p, onOk?, onFail?)
Ramune.tui.Cmd.cancelDelay(token)
Ramune.tui.Cmd.cancelEvery(token)

// Style + Markdown
Ramune.tui.style(text, opts)
Ramune.tui.markdown(text, { theme, width })

// Keymap
const keys = Ramune.tui.keymap({ name: { keys, help } })
keys.match(msg)            // sequence-aware
keys.matches(msg, name)    // single-press
keys.helpEntries(names?)
```

## Components (`<Tag />` in TSX, `Ramune.tui.Tag(props)` in plain TS)

```ts
<Box> <Stack gap> <Row gap> <Spacer size> <Text>
<Spinner type frame> <List items selected maxRows renderItem selectedStyle>
<Input value cursor placeholder focused>
<Textarea value cursor rows cols focused>
<Progress value width fillStyle emptyStyle showPercent>
<Viewport content offset height width scrollHint>
<Tabs labels selected separator tabStyle activeStyle>
<Help keys mode>
<Stopwatch elapsedMs format>
<Timer remainingMs format warningAt warningStyle>
<Table columns rows selected sortColumn sortDir maxRows>
<Filepicker cwd entries selected showHidden maxRows>
<Paginator page totalPages type="arabic"|"dots">
<Markdown content theme width>
<Form state>
```

## Reducer helpers

| Helper | Returns |
|---|---|
| `Ramune.tui.input.{init, handleKey}` | text input state |
| `Ramune.tui.textarea.{init, handleKey}` | multi-line input state |
| `Ramune.tui.paginator.{init, setTotal, handleKey, sliceForPage}` | pagination state |
| `Ramune.tui.stopwatch.{init, tick, toggle, reset}` | wall-clock-anchored elapsed counter |
| `Ramune.tui.timer.{init, tick, toggle, reset}` | countdown with `expired` flag |
| `Ramune.tui.filepicker.{init, readDirSync, cd, handleKey}` | directory navigation state |
| `Ramune.tui.form.{init, handleKey, values}` | composite form state |
