// Board state + JSON persistence. The board lives under .agentboard/ in
// the target repo so it survives restarts.
import * as fs from "fs";

export type Column = "backlog" | "running" | "review" | "done";
export const COLUMNS: Column[] = ["backlog", "running", "review", "done"];

export interface Card {
  id: string;
  title: string;
  prompt: string;
  column: Column;
  agent: string;
  branch?: string;
  worktree?: string;
  base?: string;
  status: string; // one-line live status
  log: string[]; // recent agent event lines
  diff?: { files: number; ins: number; del: number };
  createdAt: number;
  finishedAt?: number;
  error?: string;
}

export interface Board {
  repo: string;
  cards: Card[];
}

function boardDir(repo: string): string {
  return repo + "/.agentboard";
}
function boardFile(repo: string): string {
  return boardDir(repo) + "/board.json";
}

export function loadBoard(repo: string): Board {
  try {
    const b = JSON.parse(fs.readFileSync(boardFile(repo), "utf8")) as Board;
    b.repo = repo;
    if (!Array.isArray(b.cards)) b.cards = [];
    return b;
  } catch {
    return { repo, cards: [] };
  }
}

export function saveBoard(b: Board): void {
  try {
    fs.mkdirSync(boardDir(b.repo), { recursive: true });
  } catch {}
  fs.writeFileSync(boardFile(b.repo), JSON.stringify(b, null, 2));
}

export function newCard(agent: string, title: string, prompt: string): Card {
  const p = prompt.trim();
  return {
    id: Math.random().toString(36).slice(2, 8),
    title: title.trim() || p.slice(0, 40) || "untitled",
    prompt: p,
    column: "backlog",
    agent,
    status: "",
    log: [],
    createdAt: Date.now(),
  };
}
