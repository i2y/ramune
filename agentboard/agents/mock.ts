// Fake adapter: spawns a short shell script that emits a few events and
// writes one file. Lets the whole orchestrator loop be exercised without
// spending tokens or needing a real agent installed — also the reference
// for what a minimal adapter looks like.
import { spawn } from "child_process";
import { AgentAdapter } from "./types";

export const mockAdapter: AgentAdapter = {
  id: "mock",
  start(cwd, prompt, onEvent, onDone) {
    void prompt; // the mock ignores the prompt; it just produces a diff
    // Each echo is quoted: the EV| delimiter must reach our parser as
    // literal text, not be read as a shell pipe.
    const script = [
      'echo "EV|text|reading the request"',
      "sleep 1",
      'echo "EV|tool|Write|AGENT_NOTES.md"',
      'printf "# agentboard mock run\\n\\nthe mock adapter ran here.\\n" > AGENT_NOTES.md',
      "sleep 1",
      'echo "EV|tool|Bash|list files"',
      'echo "EV|status|done"',
    ].join(" && ");
    const proc = spawn("sh", ["-c", script], { cwd });
    let buf = "";
    proc.stdout.on("data", (d: any) => {
      buf += String(d);
      let nl: number;
      while ((nl = buf.indexOf("\n")) >= 0) {
        const line = buf.slice(0, nl).trim();
        buf = buf.slice(nl + 1);
        if (!line.startsWith("EV|")) continue;
        const p = line.split("|");
        if (p[1] === "tool") {
          onEvent({ kind: "tool", tool: p[2], detail: p.slice(2).join(" ") });
        } else if (p[1] === "status") {
          onEvent({ kind: "status", text: p[2] });
        } else {
          onEvent({ kind: "text", text: p.slice(2).join("|") });
        }
      }
    });
    proc.on("exit", (code: any) => {
      const c = code ?? 0;
      onDone({ code: c, error: c ? "mock exited " + c : undefined });
    });
    proc.on("error", (e: any) =>
      onDone({ code: -1, error: String((e && e.message) || e) }),
    );
    return {
      abort() {
        try {
          proc.kill();
        } catch {}
      },
    };
  },
};
