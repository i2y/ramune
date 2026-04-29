// A minimal Ramune.tui counter. Drives BubbleTea from JS by returning
// new state from update(state, msg) and a string from view(state).
//
// Run with: ../../ramune run counter.ts

declare const Ramune: any;

type Model = { count: number };
type Msg =
  | { type: "key"; key: string; alt: boolean; runes: string }
  | { type: "resize"; width: number; height: number }
  | { type: "quit" };

const init = (): Model => ({ count: 0 });

function update(model: Model, msg: Msg): Model | { _state: Model; _cmd: string } {
  if (msg.type !== "key") return model;
  switch (msg.key) {
    case "up":
    case "k":
    case "+":
      return { count: model.count + 1 };
    case "down":
    case "j":
    case "-":
      return { count: model.count - 1 };
    case "r":
      return { count: 0 };
    case "q":
    case "ctrl+c":
    case "esc":
      return Ramune.tui.Cmd.quit(model);
  }
  return model;
}

function view(model: Model): string {
  const title = Ramune.tui.style("ramune × bubbletea", {
    bold: true,
    fg: "12",
    padding: [0, 1],
  });
  const sign = model.count >= 0 ? "+" : "";
  const num = Ramune.tui.style(`${sign}${model.count}`, {
    bold: true,
    fg: model.count > 0 ? "10" : model.count < 0 ? "9" : "14",
    padding: [0, 2],
  });
  const help = Ramune.tui.style(
    "↑/↓ or +/-  •  r reset  •  q quit",
    { fg: "242" }
  );
  return Ramune.tui.style(
    `${title}\n\n${num}\n\n${help}`,
    { padding: [1, 2], border: "rounded", fg: "15" }
  );
}

Ramune.tui.run({ init, update, view }).then((final: Model) => {
  console.log(`final count: ${final.count}`);
});
