// spawnLines — the process-spawn plumbing shared by every agent adapter:
// spawn a child, hand its stdout to a per-line callback, report exit once.
// Adapters supply only the per-line parser and the exit wording, so the
// line-buffer carry-over logic lives in exactly one place.
import { spawn } from "child_process";

export interface ProcHandle {
  abort(): void;
}

export interface ProcResult {
  code: number;
  stderr: string;
  error?: string; // set only when the process failed to spawn
}

export function spawnLines(
  cmd: string,
  args: string[],
  cwd: string,
  onLine: (line: string) => void,
  onDone: (r: ProcResult) => void,
): ProcHandle {
  const proc = spawn(cmd, args, { cwd });
  // Some agent CLIs (claude -p) peek stdin and stall ~3s waiting for EOF;
  // close it so they start at once. Harmless for children that ignore it.
  try {
    if (proc.stdin) proc.stdin.end();
  } catch {}
  let buf = "";
  let stderr = "";
  proc.stdout.on("data", (d: any) => {
    buf += String(d);
    let nl: number;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, nl).trim();
      buf = buf.slice(nl + 1);
      if (line) onLine(line);
    }
  });
  proc.stderr.on("data", (d: any) => (stderr += String(d)));
  proc.on("exit", (code: any) => onDone({ code: code ?? 0, stderr }));
  proc.on("error", (e: any) =>
    onDone({ code: -1, stderr, error: String((e && e.message) || e) }),
  );
  return {
    abort() {
      try {
        proc.kill();
      } catch {}
    },
  };
}
