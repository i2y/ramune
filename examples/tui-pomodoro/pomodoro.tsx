// Pomodoro timer — a 25/5 work/break cycle that exercises Timer,
// Stopwatch, keymap, Help, and Cmd.every all at once.
//
//   ../../ramune run pomodoro.tsx

declare const Ramune: any;
declare namespace JSX {
  interface IntrinsicElements { [k: string]: any }
  type Element = string;
}

const Box = Ramune.tui.Box;
const Text = Ramune.tui.Text;
const Stack = Ramune.tui.Stack;
const Row = Ramune.tui.Row;
const Timer = Ramune.tui.Timer;
const Stopwatch = Ramune.tui.Stopwatch;
const Help = Ramune.tui.Help;
const Progress = Ramune.tui.Progress;

const WORK_MS = 25 * 60 * 1000;
const BREAK_MS = 5 * 60 * 1000;

type Phase = "work" | "break";
type Model = {
  phase: Phase;
  timer: any;
  total: any; // Stopwatch — total elapsed across phases
  cycles: number;
};

const init = (): Model => ({
  phase: "work",
  timer: Ramune.tui.timer.init(WORK_MS, { running: false }),
  total: Ramune.tui.stopwatch.init(),
  cycles: 0,
});

const keys = Ramune.tui.keymap({
  toggle: { keys: [" ", "p"], help: { key: "space", desc: "start/pause" } },
  reset:  { keys: ["r"], help: { key: "r", desc: "reset phase" } },
  skip:   { keys: ["s"], help: { key: "s", desc: "skip phase" } },
  quit:   { keys: ["q", "ctrl+c", "esc"], help: { key: "q", desc: "quit" } },
});

type Msg =
  | { type: "key"; key: string }
  | { type: "tick" };

function nextPhase(model: Model): Model {
  const isWork = model.phase === "work";
  return {
    ...model,
    phase: isWork ? "break" : "work",
    timer: Ramune.tui.timer.init(isWork ? BREAK_MS : WORK_MS, { running: true }),
    cycles: isWork ? model.cycles + 1 : model.cycles,
  };
}

function update(model: Model, msg: Msg): Model | { _state: Model; _cmd: string } {
  if (msg.type === "tick") {
    let timer = Ramune.tui.timer.tick(model.timer);
    const total = Ramune.tui.stopwatch.tick(model.total);
    if (timer.expired) {
      // auto-advance to the next phase, keep running
      return nextPhase({ ...model, timer, total });
    }
    return { ...model, timer, total };
  }
  if (msg.type !== "key") return model;
  if (keys.matches(msg, "quit")) return Ramune.tui.Cmd.quit(model);
  if (keys.matches(msg, "toggle")) {
    return {
      ...model,
      timer: Ramune.tui.timer.toggle(model.timer),
      total: Ramune.tui.stopwatch.toggle(model.total),
    };
  }
  if (keys.matches(msg, "reset")) {
    return {
      ...model,
      timer: Ramune.tui.timer.reset(model.timer),
    };
  }
  if (keys.matches(msg, "skip")) {
    return nextPhase(model);
  }
  return model;
}

function view(model: Model): string {
  const isWork = model.phase === "work";
  const total = isWork ? WORK_MS : BREAK_MS;
  const ratio = (total - model.timer.remainingMs) / total;
  const phaseColor = isWork ? "9" : "10"; // red work, green break
  const label = isWork ? "FOCUS" : "BREAK";

  return (
    <Box border="rounded" padding={[1, 3]} fg="15">
      <Stack gap={1}>
        <Text bold fg={phaseColor} padding={[0, 1]}>{label}</Text>
        <Timer
          remainingMs={model.timer.remainingMs}
          format="mm:ss"
          warningAt={isWork ? 60_000 : 30_000}
          warningStyle={{ fg: "9", bold: true }}
          bold={true}
        />
        <Progress
          value={ratio}
          width={32}
          fillStyle={{ fg: phaseColor }}
        />
        <Row gap={2}>
          <Text fg="242">cycles</Text>
          <Text bold fg="14">{model.cycles}</Text>
          <Text fg="242">total</Text>
          <Stopwatch elapsedMs={model.total.elapsedMs} format="HH:MM:SS" fg="14" />
        </Row>
        <Help keys={keys.helpEntries()} />
      </Stack>
    </Box>
  );
}

const tickT = Ramune.tui.Cmd.every(250, { type: "tick" });
Ramune.tui.run({ init, update, view }).then((final: Model) => {
  Ramune.tui.Cmd.cancelEvery(tickT);
  console.log(`completed ${final.cycles} pomodori in ${Ramune.tui.Stopwatch({ elapsedMs: final.total.elapsedMs, format: "HH:MM:SS" })}`);
});
