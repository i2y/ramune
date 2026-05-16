// agentboard kanban TUI — board / input / detail modes.
//
// The board lives in orchestrator (module-scope, mutated by async agent
// events). This file owns only navigation state; a 200ms Cmd.every tick
// re-renders so agent progress shows live.
import * as orch from "../orchestrator";
import { Card, COLUMNS } from "../board";

declare const Ramune: any;

const s = Ramune.tui.style;
const SPIN = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];
const COLW = 20;
const COLTITLE: { [k: string]: string } = {
  backlog: "BACKLOG",
  running: "RUNNING",
  review: "REVIEW",
  done: "DONE",
};

export interface Model {
  col: number;
  row: number;
  mode: "board" | "input" | "detail";
  input: { value: string; cursor: number };
  detailId: string;
  detailScroll: number;
  tick: number;
}

let detailDiff = ""; // module-scope: diff text loaded for detail mode

export function init(): Model {
  return {
    col: 0,
    row: 0,
    mode: "board",
    input: Ramune.tui.input.init(""),
    detailId: "",
    detailScroll: 0,
    tick: 0,
  };
}

// --- helpers ---------------------------------------------------------------

function colCards(col: string): Card[] {
  return orch.getBoard().cards.filter((c) => c.column === col);
}
function selected(m: Model): Card | undefined {
  return colCards(COLUMNS[m.col])[m.row];
}
function clip(str: string, w: number): string {
  return str.length <= w ? str : str.slice(0, Math.max(0, w - 1)) + "…";
}

// --- update ----------------------------------------------------------------

export function update(m: Model, msg: any): any {
  if (msg.type === "tick") return { ...m, tick: m.tick + 1 };
  if (msg.type !== "key") return m;
  if (m.mode === "input") return updateInput(m, msg);
  if (m.mode === "detail") return updateDetail(m, msg);
  return updateBoard(m, msg);
}

function updateInput(m: Model, msg: any): any {
  if (msg.key === "esc") {
    return { ...m, mode: "board", input: Ramune.tui.input.init("") };
  }
  if (msg.key === "enter") {
    const text = m.input.value.trim();
    if (text) orch.createCard(text, text);
    return { ...m, mode: "board", input: Ramune.tui.input.init("") };
  }
  const next = Ramune.tui.input.handleKey(m.input, msg);
  return next ? { ...m, input: next } : m;
}

function updateDetail(m: Model, msg: any): any {
  if (msg.key === "esc" || msg.key === "q") return { ...m, mode: "board" };
  if (msg.key === "up" || msg.key === "k") {
    return { ...m, detailScroll: Math.max(0, m.detailScroll - 1) };
  }
  if (msg.key === "down" || msg.key === "j") {
    return { ...m, detailScroll: m.detailScroll + 1 };
  }
  return m;
}

function clampRow(m: Model): Model {
  const n = colCards(COLUMNS[m.col]).length;
  return { ...m, row: Math.max(0, Math.min(m.row, n - 1)) };
}

function updateBoard(m: Model, msg: any): any {
  switch (msg.key) {
    case "left":
    case "h":
      return clampRow({ ...m, col: Math.max(0, m.col - 1) });
    case "right":
    case "l":
      return clampRow({ ...m, col: Math.min(3, m.col + 1) });
    case "up":
    case "k":
      return { ...m, row: Math.max(0, m.row - 1) };
    case "down":
    case "j": {
      const n = colCards(COLUMNS[m.col]).length;
      return { ...m, row: Math.min(m.row + 1, Math.max(0, n - 1)) };
    }
    case "n":
      return { ...m, mode: "input", input: Ramune.tui.input.init("") };
    case "enter": {
      const c = selected(m);
      if (!c) return m;
      if (c.column === "backlog") {
        void orch.startCard(c.id);
        return m;
      }
      detailDiff = "(loading diff…)";
      void orch.cardDiff(c.id).then((t) => {
        detailDiff = t;
      });
      return { ...m, mode: "detail", detailId: c.id, detailScroll: 0 };
    }
    case "m": {
      const c = selected(m);
      if (c && c.column === "review") void orch.mergeCard(c.id);
      return m;
    }
    case "d": {
      const c = selected(m);
      if (c) void orch.discardCard(c.id);
      return clampRow(m);
    }
    case "q":
    case "ctrl+c":
      return Ramune.tui.Cmd.quit(m);
  }
  return m;
}

// --- view ------------------------------------------------------------------

export function view(m: Model): string {
  return m.mode === "detail" ? viewDetail(m) : viewBoard(m);
}

function cardIcon(c: Card, tick: number): string {
  if (c.column === "backlog") return s("▢", { fg: "245" });
  if (c.column === "running") return s(SPIN[tick % SPIN.length], { fg: "11" });
  if (c.column === "review") {
    return c.error ? s("✗", { fg: "9" }) : s("✓", { fg: "10" });
  }
  return s("✔", { fg: "244" });
}

function cardLine(c: Card, ci: number, ri: number, m: Model): string {
  const line = cardIcon(c, m.tick) + " " + clip(c.title, COLW - 3);
  if (m.col === ci && m.row === ri) {
    return s(line, { bold: true, fg: "10" });
  }
  return line;
}

function cardStrip(c: Card): string {
  const head =
    s("▸ ", { fg: "14" }) +
    s(clip(c.title, 50), { bold: true }) +
    s("  [" + c.id + "] " + c.column, { fg: "242" });
  let l2 = s("  " + (c.status || "—"), { fg: c.error ? "9" : "245" });
  if (c.diff) {
    l2 += s(
      "   +" + c.diff.ins + " −" + c.diff.del + " · " + c.diff.files + "f",
      { fg: "10" },
    );
  }
  return head + "\n" + l2;
}

function viewBoard(m: Model): string {
  const board = orch.getBoard();
  const repo = board.repo.split("/").filter(Boolean).pop() || board.repo;
  const header =
    s("agentboard", { bold: true, fg: "12" }) +
    s(
      "  ·  " + repo + "  ·  agent:" + orch.agentName() +
        "  ·  " + board.cards.length + " cards",
      { fg: "242" },
    );

  const cols: string[][] = COLUMNS.map((col, ci) => {
    const cards = colCards(col);
    const lines: string[] = [
      s(COLTITLE[col] + " (" + cards.length + ")", { bold: true, fg: "14" }),
      s("─".repeat(COLW), { fg: "238" }),
    ];
    cards.forEach((c, ri) => lines.push(cardLine(c, ci, ri, m)));
    if (cards.length === 0) lines.push(s("—", { fg: "238" }));
    return lines;
  });
  // Lay the four columns out side by side via Ramune.tui.Row (real block
  // layout, ANSI-aware); a full-height " | " block divides them.
  const height = Math.max.apply(null, cols.map((c) => c.length));
  const divider = new Array(height).fill(s(" │ ", { fg: "238" })).join("\n");
  const rowKids: string[] = [];
  cols.forEach((c, i) => {
    if (i > 0) rowKids.push(divider);
    rowKids.push(c.join("\n"));
  });
  const grid = Ramune.tui.Row({}, rowKids);

  const sel = selected(m);
  const strip = sel
    ? cardStrip(sel)
    : s("no card selected — press n to add one", { fg: "242" });

  const footer =
    m.mode === "input"
      ? s("new card › ", { bold: true, fg: "14" }) +
        s(m.input.value + "▎", { fg: "15" }) +
        s("    enter add · esc cancel", { fg: "242" })
      : s(
          "←↑↓→ move · n new · ↵ start/open · m merge · d discard · q quit",
          { fg: "242" },
        );

  const rule = s("─".repeat(COLW * 4 + 9), { fg: "238" });
  const body = [header, "", grid, "", rule, strip, rule, footer].join("\n");
  return s(body, { padding: [1, 2], border: "rounded" });
}

function diffLine(l: string): string {
  if (l.indexOf("@@") === 0) return s(l, { fg: "14" });
  if (l.indexOf("+") === 0) return s(l, { fg: "10" });
  if (l.indexOf("-") === 0) return s(l, { fg: "9" });
  return s(l, { fg: "250" });
}

function viewDetail(m: Model): string {
  const c = orch.getBoard().cards.find((x) => x.id === m.detailId);
  if (!c) {
    return s("card gone — press esc", { padding: [1, 2], border: "rounded" });
  }
  const head =
    s("◂ " + clip(c.title, 50), { bold: true, fg: "12" }) +
    s("   [" + c.id + "] " + c.column, { fg: c.error ? "9" : "242" });
  const log = c.log.length
    ? c.log.slice(-6).map((x) => s("  " + clip(x, 84), { fg: "245" })).join("\n")
    : s("  (no log)", { fg: "238" });
  const all = detailDiff.split("\n");
  const H = 20;
  const start = Math.max(0, Math.min(m.detailScroll, all.length - 1));
  const shown = all.slice(start, start + H).map(diffLine).join("\n");
  const footer = s(
    "↑/↓ scroll · esc back   lines " +
      (start + 1) + "-" + Math.min(start + H, all.length) + "/" + all.length,
    { fg: "242" },
  );
  const body = [
    head,
    "",
    s("agent log", { bold: true, fg: "14" }),
    log,
    "",
    s("diff", { bold: true, fg: "14" }),
    shown,
    "",
    footer,
  ].join("\n");
  return s(body, { padding: [1, 2], border: "rounded" });
}
