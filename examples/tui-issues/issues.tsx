// Issues browser — wires together Tabs + List + Viewport + Help +
// Progress + Textarea (in compose mode). A single mock dataset stands
// in for a real backend so the demo runs without network.
//
//   ../../ramune run issues.tsx

declare const Ramune: any;
declare namespace JSX {
  interface IntrinsicElements { [k: string]: any }
  type Element = string;
}

const Box = Ramune.tui.Box;
const Stack = Ramune.tui.Stack;
const Row = Ramune.tui.Row;
const Text = Ramune.tui.Text;
const Tabs = Ramune.tui.Tabs;
const List = Ramune.tui.List;
const Viewport = Ramune.tui.Viewport;
const Help = Ramune.tui.Help;
const Progress = Ramune.tui.Progress;
const Textarea = Ramune.tui.Textarea;
const Spinner = Ramune.tui.Spinner;

type State = "open" | "closed";
type Issue = { id: number; title: string; body: string; state: State };

const ISSUES: Issue[] = [
  { id: 42, title: "Wire Cmd.dispatch through Bubbletea", state: "closed",
    body: "Plumb prog.Send so JS-side timers and Promises can\ninject msgs into the running program.\n\n## Notes\n\n- Goroutine pushes via prog.Send\n- Headless mode for tests" },
  { id: 51, title: "Add <Tabs>, <Viewport>, <Help>, <Progress>", state: "closed",
    body: "Charm bubbles equivalents as stateless renderers.\n\nProgress is value-driven; Viewport clips with a scroll hint." },
  { id: 73, title: "Multi-line Textarea component", state: "closed",
    body: "Cursor row+col, soft scroll, helpers for arrow nav and edit." },
  { id: 81, title: "Hybrid AOT for view()", state: "open",
    body: "view() is typically pure (Model → string). Lower it through\nthe picker so TUI render runs as native Go." },
  { id: 84, title: "<Table> with sortable columns", state: "open",
    body: "Mirror bubbles/table with column metadata + sort cmds." },
  { id: 87, title: "ramune tui my.tsx subcommand", state: "open",
    body: "Shortcut for `ramune run` plus templates (`init --template tui`)." },
];

type Tab = "open" | "closed" | "all";
type Mode = "browse" | "compose";

type Model = {
  tab: Tab;
  selected: number;
  vpOffset: number;
  mode: Mode;
  draft: { value: string; cursor: { row: number; col: number } };
  posted: number; // crude "submit progress" demo
  spinFrame: number;
  status: string;
};

const init = (): Model => ({
  tab: "open",
  selected: 0,
  vpOffset: 0,
  mode: "browse",
  draft: Ramune.tui.textarea.init(""),
  posted: 0,
  spinFrame: 0,
  status: "ready",
});

function visible(model: Model): Issue[] {
  if (model.tab === "all") return ISSUES;
  return ISSUES.filter((i) => i.state === model.tab);
}

function selectedIssue(model: Model): Issue | null {
  const list = visible(model);
  if (list.length === 0) return null;
  return list[Math.min(model.selected, list.length - 1)];
}

type Msg =
  | { type: "key"; key: string; runes: string }
  | { type: "spin" }
  | { type: "post-tick" }
  | { type: "post-done" };

function update(model: Model, msg: Msg): Model | { _state: Model; _cmd: string } {
  if (msg.type === "spin") return { ...model, spinFrame: model.spinFrame + 1 };
  if (msg.type === "post-tick") {
    const next = Math.min(1, model.posted + 0.1);
    return { ...model, posted: next };
  }
  if (msg.type === "post-done") {
    return { ...model, mode: "browse", draft: Ramune.tui.textarea.init(""), posted: 0, status: "comment posted" };
  }
  if (msg.type !== "key") return model;

  if (model.mode === "compose") {
    if (msg.key === "ctrl+s" || msg.key === "ctrl+enter") {
      // Simulate a 1-second post by ticking progress every 100ms.
      const tickT = Ramune.tui.Cmd.every(100, { type: "post-tick" });
      Ramune.tui.Cmd.delay(1000, { type: "post-done" });
      Ramune.tui.Cmd.delay(1100, () => Ramune.tui.Cmd.cancelEvery(tickT));
      return { ...model, status: "posting…" };
    }
    if (msg.key === "esc") return { ...model, mode: "browse", draft: Ramune.tui.textarea.init("") };
    const next = Ramune.tui.textarea.handleKey(model.draft, msg);
    if (next) return { ...model, draft: next };
    return model;
  }

  switch (msg.key) {
    case "1": return { ...model, tab: "open", selected: 0 };
    case "2": return { ...model, tab: "closed", selected: 0 };
    case "3": return { ...model, tab: "all", selected: 0 };
    case "tab": {
      const order: Tab[] = ["open", "closed", "all"];
      const i = (order.indexOf(model.tab) + 1) % order.length;
      return { ...model, tab: order[i], selected: 0 };
    }
    case "up": case "k":
      return { ...model, selected: Math.max(0, model.selected - 1), vpOffset: 0 };
    case "down": case "j":
      return { ...model, selected: Math.min(visible(model).length - 1, model.selected + 1), vpOffset: 0 };
    case "J":
      return { ...model, vpOffset: model.vpOffset + 1 };
    case "K":
      return { ...model, vpOffset: Math.max(0, model.vpOffset - 1) };
    case "r":
      return { ...model, mode: "compose", draft: Ramune.tui.textarea.init("") };
    case "q": case "ctrl+c": case "esc":
      return Ramune.tui.Cmd.quit(model);
  }
  return model;
}

function view(model: Model): string {
  const issue = selectedIssue(model);

  const list = (
    <List
      items={visible(model).map((i) =>
        `${i.state === "open" ? "○" : "✓"} #${i.id}  ${i.title}`
      )}
      selected={model.selected}
      maxRows={6}
      selectedStyle={{ bold: true, fg: "12" }}
      width={48}
    />
  );

  const detail = issue
    ? <Viewport
        content={`# ${issue.title}\n\n${issue.body}`}
        offset={model.vpOffset}
        height={8}
        width={48}
        border="rounded"
        padding={[0, 1]}
      />
    : <Box border="rounded" padding={[1, 2]}><Text fg="242">no issues in this tab</Text></Box>;

  const compose = model.mode === "compose"
    ? <Stack gap={1}>
        <Text bold fg="14">comment</Text>
        <Textarea
          value={model.draft.value}
          cursor={model.draft.cursor}
          rows={5}
          cols={48}
          border="rounded"
          padding={[0, 1]}
        />
        {model.status === "posting…"
          ? <Row gap={1}>
              <Spinner type="dot" frame={model.spinFrame} fg="14" />
              <Progress value={model.posted} width={30} fillStyle={{ fg: "14" }} />
            </Row>
          : <Text fg="242">ctrl+s post • esc cancel</Text>}
      </Stack>
    : null;

  const help = model.mode === "compose"
    ? <Help keys={[
        { key: "type", desc: "draft" },
        { key: "ctrl+s", desc: "post" },
        { key: "esc", desc: "cancel" },
      ]} />
    : <Help keys={[
        { key: "j/k", desc: "issue" },
        { key: "J/K", desc: "scroll body" },
        { key: "tab", desc: "section" },
        { key: "r", desc: "reply" },
        { key: "q", desc: "quit" },
      ]} />;

  return (
    <Box border="rounded" padding={[1, 2]} fg="15">
      <Stack gap={1}>
        <Tabs labels={["Open", "Closed", "All"]} selected={["open", "closed", "all"].indexOf(model.tab)} />
        {list}
        {detail}
        {compose ? compose : <Text fg={model.status === "ready" ? "242" : "10"}>{model.status}</Text>}
        {help}
      </Stack>
    </Box>
  );
}

const spinT = Ramune.tui.Cmd.every(80, { type: "spin" });
Ramune.tui.run({ init, update, view }).then((final: Model) => {
  Ramune.tui.Cmd.cancelEvery(spinT);
  console.log(`final tab: ${final.tab}, selected: ${final.selected}`);
});
