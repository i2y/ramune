# agentboard

A terminal-native kanban board that orchestrates headless coding agents.
Cards are tasks; each card runs in its own git worktree; columns are
**Backlog → Running → Review → Done**. Built with `Ramune.tui`.

The terminal / SSH / single-binary counterpart to web-based agent
orchestrators — agent-agnostic, anchored on Claude Code.

## Run

```sh
AGENTBOARD_REPO=/path/to/repo AGENTBOARD_AGENT=claude ramune run agentboard.ts
```

`AGENTBOARD_AGENT` is `mock` (default — no tokens, for trying it out) or
`claude`. `AGENTBOARD_REPO` defaults to the current directory.

## Keys

```
←↑↓→  move cursor      n  new card
↵     start (backlog card) / open detail (other)
m     merge (review card)  d  discard      q  quit
```

## Lifecycle

A card moves Backlog → Running when started: agentboard cuts a git
worktree, launches the agent there, and streams its tool calls onto the
card. On exit the agent's work is committed and the card lands in Review
with a diff. `m` merges the branch back; `d` discards it.

## Layout

```
agentboard.ts     entry — wires the Ramune.tui program
orchestrator.ts   card lifecycle: worktree, agent run, finalize, merge
board.ts          board state + .agentboard/board.json persistence
git.ts            git worktree / diff / merge helpers
agents/types.ts   AgentAdapter / AgentRun / AgentEvent — the agnostic seam
agents/claude.ts  Claude Code adapter (claude -p --output-format stream-json)
agents/mock.ts    fake adapter — exercises the loop without tokens
ui/board.ts       the kanban TUI
verify.ts         headless smoke test (mock adapter, throwaway repo)
```

## Status

MVP — local mode. `serveSSH` (remote access) and more agent adapters
(Codex, Crush, Gemini) are next.
