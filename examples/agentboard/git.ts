// Thin async wrappers over the `git` CLI. Everything shells out via
// child_process, so this works on every Ramune backend.
import { spawn } from "child_process";

export interface GitResult {
  code: number;
  out: string;
  err: string;
}

export function git(cwd: string, args: string[]): Promise<GitResult> {
  return new Promise((resolve) => {
    const p = spawn("git", args, { cwd });
    let out = "";
    let err = "";
    p.stdout.on("data", (d: any) => (out += String(d)));
    p.stderr.on("data", (d: any) => (err += String(d)));
    p.on("exit", (code: any) => resolve({ code: code ?? 0, out, err }));
    p.on("error", (e: any) =>
      resolve({ code: -1, out, err: String((e && e.message) || e) }),
    );
  });
}

export async function currentBranch(repo: string): Promise<string> {
  const r = await git(repo, ["rev-parse", "--abbrev-ref", "HEAD"]);
  return r.out.trim() || "HEAD";
}

export async function worktreeAdd(
  repo: string,
  path: string,
  branch: string,
  base: string,
): Promise<GitResult> {
  return git(repo, ["worktree", "add", "-b", branch, path, base]);
}

export async function worktreeRemove(repo: string, path: string): Promise<void> {
  await git(repo, ["worktree", "remove", "--force", path]);
}

export async function deleteBranch(repo: string, branch: string): Promise<void> {
  await git(repo, ["branch", "-D", branch]);
}

// Stage + commit everything the agent produced. Returns true if a commit
// was made (false when the agent changed nothing).
export async function commitAll(wt: string, message: string): Promise<boolean> {
  await git(wt, ["add", "-A"]);
  const staged = await git(wt, ["diff", "--cached", "--quiet"]);
  if (staged.code === 0) return false; // nothing staged
  const c = await git(wt, ["commit", "--no-verify", "-m", message]);
  return c.code === 0;
}

export interface DiffStat {
  files: number;
  ins: number;
  del: number;
}

export async function diffStat(wt: string, base: string): Promise<DiffStat> {
  const r = await git(wt, ["diff", "--numstat", base, "HEAD"]);
  let files = 0;
  let ins = 0;
  let del = 0;
  for (const line of r.out.split("\n")) {
    const m = line.match(/^(\d+|-)\t(\d+|-)\t/);
    if (!m) continue;
    files++;
    if (m[1] !== "-") ins += parseInt(m[1], 10);
    if (m[2] !== "-") del += parseInt(m[2], 10);
  }
  return { files, ins, del };
}

export async function diffText(wt: string, base: string): Promise<string> {
  const r = await git(wt, ["diff", base, "HEAD"]);
  return r.out;
}

export async function mergeBranch(
  repo: string,
  branch: string,
  message: string,
): Promise<GitResult> {
  return git(repo, ["merge", "--no-ff", "-m", message, branch]);
}
