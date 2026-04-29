// SSH-served counter — a Charm wish stack rendered from TSX. Each SSH
// connection gets its own counter; init/update/view are shared.
//
//   ../../ramune run server.tsx
//   # then in another terminal:
//   ssh -p 2222 anything@localhost   (any user, no auth)

declare const Ramune: any;
declare namespace JSX {
  interface IntrinsicElements { [k: string]: any }
  type Element = string;
}

const Box = Ramune.tui.Box;
const Stack = Ramune.tui.Stack;
const Text = Ramune.tui.Text;
const Help = Ramune.tui.Help;

type Model = { count: number; user: string };
type Msg = { type: "key"; key: string } | { type: "resize"; width: number; height: number };

const init = (): Model => ({ count: 0, user: "guest" });

const keys = Ramune.tui.keymap({
  up:    { keys: ["up", "k", "+"], help: { key: "↑/k/+", desc: "increment" } },
  down:  { keys: ["down", "j", "-"], help: { key: "↓/j/-", desc: "decrement" } },
  reset: { keys: ["r"], help: { key: "r", desc: "reset" } },
  quit:  { keys: ["q", "ctrl+c"], help: { key: "q", desc: "disconnect" } },
});

function update(model: Model, msg: Msg): Model | { _state: Model; _cmd: string } {
  if (msg.type !== "key") return model;
  if (keys.matches(msg, "up")) return { ...model, count: model.count + 1 };
  if (keys.matches(msg, "down")) return { ...model, count: model.count - 1 };
  if (keys.matches(msg, "reset")) return { ...model, count: 0 };
  if (keys.matches(msg, "quit")) return Ramune.tui.Cmd.quit(model);
  return model;
}

function view(model: Model): string {
  const sign = model.count >= 0 ? "+" : "";
  const numColor = model.count > 0 ? "10" : model.count < 0 ? "9" : "14";
  return (
    <Box border="rounded" padding={[1, 3]} fg="15">
      <Stack gap={1}>
        <Text bold fg="12">ramune.tui over SSH</Text>
        <Text bold fg={numColor} padding={[0, 2]}>{sign}{model.count}</Text>
        <Help keys={keys.helpEntries()} />
      </Stack>
    </Box>
  );
}

const port = process.argv[2] ? parseInt(process.argv[2]) : 2222;
const addr = `:${port}`;
console.log(`ramune.tui SSH server on ${addr}`);
console.log(`  ssh -p ${port} -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null anyone@localhost`);
console.log(`(ctrl+c to stop the server)`);

Ramune.tui.serveSSH({
  addr,
  hostKeyPath: "./host_key",
  init,
  update,
  view,
}).then(() => {
  console.log("server stopped");
});
