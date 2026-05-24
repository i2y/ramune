// Headless smoke test: drives the orchestrator loop with the mock adapter
// on a throwaway repo and renders the board view — no TUI, no tokens.
//
//   AB_VERIFY_REPO=/tmp/ab-scratch ramune run verify.ts
import * as orch from "./orchestrator";
import { init as uiInit, view } from "./ui/board";

declare const process: any;

function stripAnsi(x: string): string {
  return x.replace(/\x1b\[[0-9;]*m/g, "");
}

const repo = process.env && process.env.AB_VERIFY_REPO;
if (!repo) {
  console.log("FAIL: set AB_VERIFY_REPO to a scratch git repo");
  process.exit(1);
}

const agent = (process.env && process.env.AB_VERIFY_AGENT) || "mock";
orch.init(repo, agent);
console.log("agent: " + agent);
orch.createCard(
  "create a HELLO.md file",
  "Create a file named HELLO.md containing exactly one line: hello from agentboard",
);
orch.createCard("a second backlog idea", "another task");

console.log("=== board after creating 2 cards ===");
console.log(stripAnsi(view(uiInit())));

const card = orch.getBoard().cards[0];
console.log("\n=== starting card " + card.id + " ===");
orch.startCard(card.id);

function waitReview(): Promise<boolean> {
  return new Promise((resolve) => {
    let n = 0;
    const iv = setInterval(() => {
      if (card.column === "review") {
        clearInterval(iv);
        resolve(true);
      } else if (++n > 600) {
        clearInterval(iv);
        resolve(false);
      }
    }, 200);
  });
}

waitReview().then(async (ok) => {
  if (!ok) {
    console.log("FAIL: card never reached review (timeout)");
    process.exit(1);
  }
  console.log("column:    " + card.column);
  console.log("status:    " + card.status);
  console.log("diff stat: " + JSON.stringify(card.diff));
  console.log("error:     " + (card.error || "(none)"));

  const d = await orch.cardDiff(card.id);
  console.log("\n=== diff ===");
  console.log(d.split("\n").slice(0, 16).join("\n"));

  console.log("\n=== board view (card now in review) ===");
  console.log(stripAnsi(view(uiInit())));

  const pass = card.column === "review" && !card.error && !!card.diff &&
    card.diff.files > 0;
  console.log("\n" + (pass ? "VERIFY OK" : "VERIFY FAILED"));
  process.exit(pass ? 0 : 1);
});
