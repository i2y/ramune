// agentboard — a terminal kanban that orchestrates headless coding agents.
//
//   AGENTBOARD_REPO=/path/to/repo AGENTBOARD_AGENT=claude ramune run agentboard.ts
//
// AGENTBOARD_AGENT is "mock" (default — no tokens) or "claude".
import * as orch from "./orchestrator";
import { init, update, view } from "./ui/board";

declare const Ramune: any;
declare const process: any;

const repo = (process.env && process.env.AGENTBOARD_REPO) || process.cwd();
const agent = (process.env && process.env.AGENTBOARD_AGENT) || "mock";

orch.init(repo, agent);

// Re-render ~5x/s so async agent progress shows live on the cards.
const tick = Ramune.tui.Cmd.every(200, { type: "tick" });
Ramune.tui.run({ init, update, view }).then(() => {
  Ramune.tui.Cmd.cancelEvery(tick);
});
