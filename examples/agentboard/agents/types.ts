// Agent adapter contract.
//
// An adapter wraps one headless coding-agent CLI (claude, codex, crush, ...)
// behind a uniform event stream, so the orchestrator stays agent-agnostic —
// adding a new agent is one file in this directory.

export type AgentEvent =
  | { kind: "text"; text: string }
  | { kind: "tool"; tool: string; detail: string }
  | { kind: "usage"; inTok: number; outTok: number }
  | { kind: "status"; text: string };

export interface AgentRun {
  abort(): void;
}

export interface AgentAdapter {
  id: string;
  // Run `prompt` with the agent inside `cwd` (an isolated git worktree).
  // Stream progress through onEvent; call onDone exactly once on exit.
  start(
    cwd: string,
    prompt: string,
    onEvent: (e: AgentEvent) => void,
    onDone: (r: { code: number; error?: string }) => void,
  ): AgentRun;
}
