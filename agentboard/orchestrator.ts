// Card lifecycle: backlog -> running -> review -> done.
//
// The board is module-scope mutable state. Agent events mutate it
// asynchronously; the UI subscribes via setNotify() and re-renders.
import { Board, Card, loadBoard, saveBoard, newCard } from "./board";
import { AgentAdapter, AgentEvent, AgentRun } from "./agents/types";
import { mockAdapter } from "./agents/mock";
import { claudeAdapter } from "./agents/claude";
import * as git from "./git";

const ADAPTERS: { [id: string]: AgentAdapter } = {
  mock: mockAdapter,
  claude: claudeAdapter,
};

let board: Board = { repo: ".", cards: [] };
let adapter: AgentAdapter = mockAdapter;
let notify: () => void = () => {};
const runs: { [id: string]: AgentRun } = {};

export function init(repo: string, agentId: string): void {
  board = loadBoard(repo);
  adapter = ADAPTERS[agentId] || mockAdapter;
}
export function setNotify(fn: () => void): void {
  notify = fn;
}
export function getBoard(): Board {
  return board;
}
export function agentName(): string {
  return adapter.id;
}

function persist(): void {
  saveBoard(board);
  try {
    notify();
  } catch {}
}
function find(id: string): Card | undefined {
  return board.cards.find((c) => c.id === id);
}
function logLine(c: Card, s: string): void {
  c.log.push(s);
  while (c.log.length > 200) c.log.shift();
}

export function createCard(title: string, prompt: string): Card {
  const c = newCard(adapter.id, title, prompt);
  board.cards.push(c);
  persist();
  return c;
}

// backlog -> running: cut a worktree, launch the agent.
export async function startCard(id: string): Promise<void> {
  const c = find(id);
  if (!c || c.column !== "backlog") return;
  c.column = "running";
  c.status = "preparing worktree";
  persist();

  const base = await git.currentBranch(board.repo);
  const branch = "agentboard/" + c.id;
  const wt = board.repo + "/.agentboard/wt/" + c.id;
  const add = await git.worktreeAdd(board.repo, wt, branch, base);
  if (add.code !== 0) {
    c.column = "review";
    c.status = "failed";
    c.error = "worktree: " + (add.err.trim() || add.out.trim());
    c.finishedAt = Date.now();
    persist();
    return;
  }
  c.base = base;
  c.branch = branch;
  c.worktree = wt;
  c.status = "agent starting";
  persist();

  const onEvent = (e: AgentEvent) => {
    if (e.kind === "tool") {
      c.status = e.detail;
      logLine(c, "• " + e.detail);
    } else if (e.kind === "text") {
      logLine(c, e.text);
    } else if (e.kind === "status") {
      c.status = e.text;
    } else if (e.kind === "usage") {
      logLine(c, "  tokens " + e.inTok + " in / " + e.outTok + " out");
    }
    persist();
  };
  const onDone = (r: { code: number; error?: string }) => {
    delete runs[c.id];
    if (r.error) c.error = r.error;
    c.status = "finalizing";
    persist();
    void finalize(c);
  };
  runs[c.id] = adapter.start(wt, c.prompt, onEvent, onDone);
}

// running -> review: commit the agent's work, compute the diff stat.
async function finalize(c: Card): Promise<void> {
  try {
    if (c.worktree) await git.commitAll(c.worktree, "agentboard: " + c.title);
    if (c.worktree && c.base) c.diff = await git.diffStat(c.worktree, c.base);
  } catch (e) {
    c.error = (c.error ? c.error + "; " : "") + "finalize: " + String(e);
  }
  c.column = "review";
  c.status = c.error ? "error" : "ready for review";
  c.finishedAt = Date.now();
  persist();
}

export async function cardDiff(id: string): Promise<string> {
  const c = find(id);
  if (!c || !c.worktree || !c.base) return "(no worktree)";
  const t = await git.diffText(c.worktree, c.base);
  return t.trim() ? t : "(no changes)";
}

// review -> done: merge the branch back into base, drop the worktree.
export async function mergeCard(id: string): Promise<void> {
  const c = find(id);
  if (!c || c.column !== "review" || !c.branch || !c.worktree || !c.base) return;
  c.status = "merging";
  persist();
  const m = await git.mergeBranch(board.repo, c.branch, "agentboard: " + c.title);
  if (m.code !== 0) {
    c.error = "merge conflict";
    c.status = "conflict — resolve in " + c.worktree;
    persist();
    return;
  }
  try {
    await git.worktreeRemove(board.repo, c.worktree);
  } catch {}
  c.column = "done";
  c.status = "merged";
  c.error = undefined;
  persist();
}

// drop a card: abort the agent, remove its worktree + branch.
export async function discardCard(id: string): Promise<void> {
  const c = find(id);
  if (!c) return;
  const run = runs[c.id];
  if (run) {
    run.abort();
    delete runs[c.id];
  }
  if (c.worktree) {
    try {
      await git.worktreeRemove(board.repo, c.worktree);
    } catch {}
  }
  if (c.branch) {
    try {
      await git.deleteBranch(board.repo, c.branch);
    } catch {}
  }
  board.cards = board.cards.filter((x) => x.id !== c.id);
  persist();
}
