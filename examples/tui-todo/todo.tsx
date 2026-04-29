// Todo TUI — exercises Input, List, Spinner, Cmd.every (spinner tick),
// Cmd.fromPromise (fake fetch), and Cmd.quit. Two modes:
//   - normal:  ↑/↓ navigate, x toggle done, d delete, i to insert,
//              s to "sync" (fake fetch demo), q quit
//   - input:   type to add, enter to commit, esc to cancel
//
//   ../../ramune run todo.tsx

declare const Ramune: any;
declare namespace JSX {
  interface IntrinsicElements { [k: string]: any }
  type Element = string;
}

const Box = Ramune.tui.Box;
const Text = Ramune.tui.Text;
const Stack = Ramune.tui.Stack;
const Row = Ramune.tui.Row;
const List = Ramune.tui.List;
const Input = Ramune.tui.Input;
const Spinner = Ramune.tui.Spinner;

type Item = { text: string; done: boolean };
type Mode = "normal" | "input";
type Model = {
  items: Item[];
  selected: number;
  mode: Mode;
  input: { value: string; cursor: number };
  syncing: boolean;
  spinFrame: number;
  status: string;
};

type Msg =
  | { type: "key"; key: string; runes: string }
  | { type: "spin" }
  | { type: "sync-done"; result: number }
  | { type: "sync-fail"; reason: string };

const init = (): Model => ({
  items: [
    { text: "ship Ramune.tui v1", done: true },
    { text: "wire Cmd.every", done: true },
    { text: "Spinner / List / Input", done: true },
    { text: "todo demo", done: false },
    { text: "split into separate repo (eventually)", done: false },
  ],
  selected: 0,
  mode: "normal",
  input: Ramune.tui.input.init(""),
  syncing: false,
  spinFrame: 0,
  status: "ready",
});

function update(model: Model, msg: Msg): Model | { _state: Model; _cmd: string } {
  if (msg.type === "spin") {
    return { ...model, spinFrame: model.spinFrame + 1 };
  }
  if (msg.type === "sync-done") {
    return { ...model, syncing: false, status: `synced ${msg.result} items` };
  }
  if (msg.type === "sync-fail") {
    return { ...model, syncing: false, status: `sync failed: ${msg.reason}` };
  }
  if (msg.type !== "key") return model;

  if (model.mode === "input") {
    if (msg.key === "esc") return { ...model, mode: "normal", input: Ramune.tui.input.init("") };
    if (msg.key === "enter") {
      const text = model.input.value.trim();
      if (text === "") return { ...model, mode: "normal" };
      return {
        ...model,
        items: [...model.items, { text, done: false }],
        selected: model.items.length,
        mode: "normal",
        input: Ramune.tui.input.init(""),
      };
    }
    const next = Ramune.tui.input.handleKey(model.input, msg);
    if (next) return { ...model, input: next };
    return model;
  }

  switch (msg.key) {
    case "up": case "k":
      return { ...model, selected: Math.max(0, model.selected - 1) };
    case "down": case "j":
      return { ...model, selected: Math.min(model.items.length - 1, model.selected + 1) };
    case "x": case " ": {
      const items = model.items.map((it, i) =>
        i === model.selected ? { ...it, done: !it.done } : it
      );
      return { ...model, items };
    }
    case "d": {
      if (model.items.length === 0) return model;
      const items = model.items.filter((_, i) => i !== model.selected);
      return { ...model, items, selected: Math.min(model.selected, items.length - 1) };
    }
    case "i":
      return { ...model, mode: "input", input: Ramune.tui.input.init("") };
    case "s": {
      if (model.syncing) return model;
      // Fake "fetch" — resolves after 1.5s with the count.
      const fakeFetch = new Promise<number>((resolve) => {
        setTimeout(() => resolve(model.items.filter((it) => it.done).length), 1500);
      });
      Ramune.tui.Cmd.fromPromise(
        fakeFetch,
        (n: number) => ({ type: "sync-done", result: n }),
        (err: any) => ({ type: "sync-fail", reason: String(err) })
      );
      return { ...model, syncing: true, status: "syncing…" };
    }
    case "q": case "ctrl+c": case "esc":
      return Ramune.tui.Cmd.quit(model);
  }
  return model;
}

function view(model: Model): string {
  const title = (
    <Text bold fg="12" padding={[0, 1]}>
      ✦ ramune.tui todo
    </Text>
  );

  const list = (
    <List
      items={model.items.map((it) => `${it.done ? "✓" : "○"}  ${it.text}`)}
      selected={model.selected}
      maxRows={8}
      selectedStyle={{ bold: true, fg: "12" }}
      padding={[0, 1]}
    />
  );

  const inputBar =
    model.mode === "input" ? (
      <Row gap={1}>
        <Text bold fg="14">+</Text>
        <Input
          value={model.input.value}
          cursor={model.input.cursor}
          placeholder="add a task…"
          focused={true}
          width={50}
        />
      </Row>
    ) : (
      <Text fg="242">
        ↑/↓ move • x toggle • d delete • i add • s sync • q quit
      </Text>
    );

  const status = model.syncing ? (
    <Row gap={1}>
      <Spinner type="dot" frame={model.spinFrame} fg="14" />
      <Text fg="14">{model.status}</Text>
    </Row>
  ) : (
    <Text fg={model.status.startsWith("sync failed") ? "9" : "10"}>
      {model.status}
    </Text>
  );

  return (
    <Box border="rounded" padding={[1, 2]} fg="15">
      <Stack gap={1}>
        {title}
        {list}
        {inputBar}
        {status}
      </Stack>
    </Box>
  );
}

const spinToken = Ramune.tui.Cmd.every(80, { type: "spin" });
Ramune.tui.run({ init, update, view }).then((final: Model) => {
  Ramune.tui.Cmd.cancelEvery(spinToken);
  const done = final.items.filter((it) => it.done).length;
  console.log(`final: ${done}/${final.items.length} done`);
});
