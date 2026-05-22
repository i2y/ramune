// Claude Code adapter — drives `claude -p` in headless stream-json mode and
// normalises its NDJSON event stream into AgentEvents.
import { basename } from "path";
import { spawnLines } from "../proc";
import { AgentAdapter, AgentEvent } from "./types";

function toolDetail(name: string, input: any): string {
  const n = name || "tool";
  if (!input || typeof input !== "object") return n;
  if (input.file_path) return n + " " + basename(input.file_path);
  if (input.command) return n + ": " + String(input.command).slice(0, 50);
  if (input.pattern) return n + " /" + String(input.pattern).slice(0, 30) + "/";
  return n;
}

// One NDJSON line from `claude --output-format stream-json`.
function handleLine(line: string, onEvent: (e: AgentEvent) => void): void {
  let o: any;
  try {
    o = JSON.parse(line);
  } catch {
    return;
  }
  if (o.type === "assistant" && o.message && Array.isArray(o.message.content)) {
    for (const b of o.message.content) {
      if (b.type === "text" && b.text && String(b.text).trim()) {
        onEvent({ kind: "text", text: String(b.text).trim() });
      } else if (b.type === "tool_use") {
        onEvent({
          kind: "tool",
          tool: b.name || "tool",
          detail: toolDetail(b.name, b.input),
        });
      }
    }
  } else if (o.type === "result") {
    const u = o.usage || {};
    onEvent({ kind: "usage", inTok: u.input_tokens || 0, outTok: u.output_tokens || 0 });
    onEvent({ kind: "status", text: o.is_error ? "error" : "done" });
  } else if (o.type === "system" && o.subtype === "init") {
    onEvent({ kind: "status", text: "started" });
  }
}

export const claudeAdapter: AgentAdapter = {
  id: "claude",
  start(cwd, prompt, onEvent, onDone) {
    const args = [
      "-p",
      prompt,
      "--output-format",
      "stream-json",
      "--verbose",
      // The agent runs unattended inside an isolated worktree — its blast
      // radius is that worktree, so skip interactive permission prompts.
      "--dangerously-skip-permissions",
    ];
    return spawnLines("claude", args, cwd, (line) => handleLine(line, onEvent), (r) =>
      onDone({
        code: r.code,
        error: r.error ?? (r.code ? r.stderr.trim() || "exit " + r.code : undefined),
      }),
    );
  },
};
