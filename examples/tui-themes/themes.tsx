// Markdown theme switcher — cycle through every glamour preset and
// render the same source under each. Demonstrates the available
// `theme` values for `<Markdown>` / `Ramune.tui.markdown`.
//
//   ../../ramune run themes.tsx

declare const Ramune: any;
declare namespace JSX {
  interface IntrinsicElements { [k: string]: any }
  type Element = string;
}

const Box = Ramune.tui.Box;
const Text = Ramune.tui.Text;
const Stack = Ramune.tui.Stack;
const Row = Ramune.tui.Row;
const Tabs = Ramune.tui.Tabs;
const Markdown = Ramune.tui.Markdown;
const Help = Ramune.tui.Help;

const THEMES = [
  "auto", "dark", "light", "ascii", "notty", "pink", "dracula", "tokyo-night",
] as const;
type Theme = typeof THEMES[number];

type Model = { themeIdx: number; width: number };
type Msg =
  | { type: "key"; key: string }
  | { type: "resize"; width: number; height: number };

const init = (): Model => ({ themeIdx: 1, width: 70 }); // start on "dark"

const keys = Ramune.tui.keymap({
  prev:  { keys: ["left", "h", "shift+tab"], help: { key: "←/h", desc: "prev theme" } },
  next:  { keys: ["right", "l", "tab"], help: { key: "→/l", desc: "next theme" } },
  quit:  { keys: ["q", "ctrl+c", "esc"], help: { key: "q", desc: "quit" } },
});

function update(model: Model, msg: Msg): Model | { _state: Model; _cmd: string } {
  if (msg.type === "resize") return { ...model, width: Math.min(80, msg.width - 6) };
  if (msg.type !== "key") return model;
  if (keys.matches(msg, "prev")) {
    return { ...model, themeIdx: (model.themeIdx - 1 + THEMES.length) % THEMES.length };
  }
  if (keys.matches(msg, "next")) {
    return { ...model, themeIdx: (model.themeIdx + 1) % THEMES.length };
  }
  if (keys.matches(msg, "quit")) return Ramune.tui.Cmd.quit(model);
  return model;
}

const SAMPLE = `# Charm Markdown Themes

Render this text under any of the eight glamour-bundled styles. Use
\`←\`/\`→\` to cycle.

## Inline

This paragraph mixes **bold**, *italic*, and \`inline code\`.

> Blockquote: glamour renders these with the active palette.

## Lists

1. ordered first
2. ordered second
   - nested unordered
   - and another

## Code

\`\`\`go
func add(a, b int) int { return a + b }
\`\`\`

| Theme | Use |
|---|---|
| dark | terminals with dark backgrounds |
| light | white-background terminals |
| dracula / tokyo-night | popular themed presets |
| ascii / notty | strip color for non-TTY pipes |
`;

function view(model: Model): string {
  const theme = THEMES[model.themeIdx] as Theme;
  const rendered = Ramune.tui.markdown(SAMPLE, { theme, width: model.width });
  return (
    <Box border="rounded" padding={[1, 2]}>
      <Stack gap={1}>
        <Tabs labels={[...THEMES]} selected={model.themeIdx} />
        <Text fg="242">theme = "{theme}"</Text>
        {rendered}
        <Help keys={keys.helpEntries()} />
      </Stack>
    </Box>
  );
}

Ramune.tui.run({ init, update, view }).then((final: Model) => {
  console.log(`exit theme: ${THEMES[final.themeIdx]}`);
});
