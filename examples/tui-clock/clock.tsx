// Live digital clock — exercises Cmd.every for periodic dispatch.
//
//   ../../ramune run clock.tsx

declare const Ramune: any;
declare namespace JSX {
  interface IntrinsicElements { [k: string]: any }
  type Element = string;
}

const Box = Ramune.tui.Box;
const Text = Ramune.tui.Text;
const Stack = Ramune.tui.Stack;

type Model = { now: string; ticks: number };
type Msg =
  | { type: "key"; key: string }
  | { type: "tick" };

const fmt = (d: Date) =>
  [d.getHours(), d.getMinutes(), d.getSeconds()]
    .map((n) => String(n).padStart(2, "0"))
    .join(":");

const init = (): Model => ({ now: fmt(new Date()), ticks: 0 });

function update(model: Model, msg: Msg): Model | { _state: Model; _cmd: string } {
  if (msg.type === "tick") return { now: fmt(new Date()), ticks: model.ticks + 1 };
  if (msg.type === "key" && (msg.key === "q" || msg.key === "ctrl+c" || msg.key === "esc")) {
    return Ramune.tui.Cmd.quit(model);
  }
  return model;
}

function view(model: Model): string {
  return (
    <Box border="rounded" padding={[1, 3]} fg="15">
      <Stack gap={1}>
        <Text bold fg="12">⏱  ramune.tui clock</Text>
        <Text bold fg="14">{model.now}</Text>
        <Text fg="242">ticks: {model.ticks}  •  q to quit</Text>
      </Stack>
    </Box>
  );
}

// Wire the periodic dispatch before run(). Stop it when the program exits
// so the JS event loop doesn't keep ticking after the TUI closes.
const everyToken = Ramune.tui.Cmd.every(1000, { type: "tick" });
Ramune.tui.run({ init, update, view }).then((final: Model) => {
  Ramune.tui.Cmd.cancelEvery(everyToken);
  console.log(`final ticks: ${final.ticks}`);
});
