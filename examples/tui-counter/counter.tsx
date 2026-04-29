// JSX-flavored Ramune.tui counter. <Box>/<Text>/<Stack> compose
// Lipgloss styles directly; the runtime ships them via tsx defaults.
//
// Run with: ../../ramune run counter.tsx

declare const Ramune: any;
declare namespace JSX {
  interface IntrinsicElements { [k: string]: any }
  type Element = string;
}

type Model = { count: number };
type Msg = { type: "key"; key: string } | { type: "resize"; width: number; height: number } | { type: "quit" };

const Box = Ramune.tui.Box;
const Text = Ramune.tui.Text;
const Stack = Ramune.tui.Stack;

const init = (): Model => ({ count: 0 });

function update(model: Model, msg: Msg): Model | { _state: Model; _cmd: string } {
  if (msg.type !== "key") return model;
  switch (msg.key) {
    case "up": case "k": case "+": return { count: model.count + 1 };
    case "down": case "j": case "-": return { count: model.count - 1 };
    case "r": return { count: 0 };
    case "q": case "ctrl+c": case "esc": return Ramune.tui.Cmd.quit(model);
  }
  return model;
}

function view(model: Model): string {
  const sign = model.count >= 0 ? "+" : "";
  const numColor = model.count > 0 ? "10" : model.count < 0 ? "9" : "14";
  return (
    <Box border="rounded" padding={[1, 2]} fg="15">
      <Stack gap={1}>
        <Text bold fg="12" padding={[0, 1]}>ramune × bubbletea</Text>
        <Text bold fg={numColor} padding={[0, 2]}>{sign}{model.count}</Text>
        <Text fg="242">↑/↓ or +/-  •  r reset  •  q quit</Text>
      </Stack>
    </Box>
  );
}

Ramune.tui.run({ init, update, view }).then((final: Model) => {
  console.log(`final count: ${final.count}`);
});
