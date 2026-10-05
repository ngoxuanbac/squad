import { execFile, spawn } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const SAFE = ["-c", "core.hooksPath=", "-c", "core.fsmonitor=false", "-c", "core.pager=cat"];
const IDENTITY = ["-c", "user.name=squad", "-c", "user.email=squad@localhost"];

export function gitAsync(cwd: string, args: string[]): Promise<{ code: number; stdout: string; stderr: string }> {
  return new Promise((resolve) => {
    execFile("git", [...SAFE, ...args], { cwd, encoding: "utf8", maxBuffer: 32 * 1024 * 1024 }, (error, stdout, stderr) => {
      const code = error ? (typeof (error as { code?: unknown }).code === "number" ? ((error as { code: number }).code) : 1) : 0;
      resolve({ code, stdout, stderr });
    });
  });
}

async function must(cwd: string, args: string[]): Promise<string> {
  const result = await gitAsync(cwd, args);
  if (result.code !== 0) throw new Error(`git ${args.join(" ")}: ${result.stderr.trim() || `exit ${result.code}`}`);
  return result.stdout;
}

export async function createWorktree(repo: string, path: string, branch: string, from: string): Promise<void> {
  if (fs.existsSync(path)) return;
  await must(repo, ["worktree", "add", "-b", branch, path, from]);
}

export async function createDetachedWorktree(repo: string, path: string, sha: string): Promise<void> {
  if (fs.existsSync(path)) await removeWorktree(repo, path);
  await must(repo, ["worktree", "add", "--detach", path, sha]);
}

/** Moves a read-only detached worktree to `sha`, discarding nothing because nothing is edited there. */
export async function syncDetached(path: string, sha: string): Promise<void> {
  if (!fs.existsSync(path)) return;
  await must(path, ["checkout", "--detach", "--force", sha]);
}

/** Marks a working copy as in use, so git refuses to prune or move it while an HC works there. */
export async function lockWorktree(repo: string, path: string, reason: string): Promise<void> {
  await gitAsync(repo, ["worktree", "lock", "--reason", reason, path]);
}

export async function unlockWorktree(repo: string, path: string): Promise<void> {
  await gitAsync(repo, ["worktree", "unlock", path]);
}

export async function removeWorktree(repo: string, path: string): Promise<void> {
  if (fs.existsSync(path)) await unlockWorktree(repo, path);
  if (fs.existsSync(path)) await gitAsync(repo, ["worktree", "remove", "--force", path]);
  if (fs.existsSync(path)) fs.rmSync(path, { recursive: true, force: true });
  await gitAsync(repo, ["worktree", "prune"]);
}

export async function deleteBranch(repo: string, branch: string): Promise<void> {
  await gitAsync(repo, ["branch", "-D", branch]);
}

export type MergeOutcome = { sha: string } | { conflict: string[] };

/** Merges `branch` into whatever the worktree has checked out, unless it is already there. */
export async function bringBranchIn(repo: string, worktree: string, branch: string): Promise<MergeOutcome> {
  const head = (await must(worktree, ["rev-parse", "HEAD"])).trim();
  const branchHead = (await must(repo, ["rev-parse", branch])).trim();
  const contained = await gitAsync(repo, ["merge-base", "--is-ancestor", branchHead, head]);
  if (contained.code === 0) return { sha: head };
  const merge = await gitAsync(worktree, [...IDENTITY, "merge", "--no-edit", "--no-ff", "-m", `squad: gộp ${branch}`, branchHead]);
  if (merge.code === 0) return { sha: (await must(worktree, ["rev-parse", "HEAD"])).trim() };
  const files = (await gitAsync(worktree, ["diff", "--name-only", "--diff-filter=U"])).stdout.split("\n").filter(Boolean);
  if (!files.length) {
    await gitAsync(worktree, ["merge", "--abort"]);
    throw new Error(`git merge ${branch} lỗi: ${merge.stderr.trim()}`);
  }
  return { conflict: files };
}

/** Commits on `ref` that `head` does not have yet. */
export async function commitsBehind(repo: string, head: string, ref: string): Promise<number> {
  const result = await gitAsync(repo, ["rev-list", "--count", `${head}..${ref}`]);
  return result.code === 0 ? Number(result.stdout.trim()) || 0 : 0;
}

/** Moves the squad branch to `sha` only if it still points at `expected`. */
export async function fastForward(repo: string, branch: string, sha: string, expected: string): Promise<void> {
  await must(repo, ["update-ref", `refs/heads/${branch}`, sha, expected]);
}

export async function revParse(repo: string, ref: string): Promise<string> {
  return (await must(repo, ["rev-parse", ref])).trim();
}

export interface CommandRun {
  command: string;
  exit: number;
  output: string;
}

const OUTPUT_LIMIT = 4096;

/** Git for Windows' bash next to the real git, so checks run in the same shell the HCs use. */
export function findBash(realGit: string): string | undefined {
  if (process.platform !== "win32") return undefined;
  let dir = path.dirname(realGit);
  for (let up = 0; up < 4; up += 1) {
    for (const candidate of [path.join(dir, "bin", "bash.exe"), path.join(dir, "usr", "bin", "bash.exe")]) {
      if (fs.existsSync(candidate)) return candidate;
    }
    dir = path.dirname(dir);
  }
  return undefined;
}

/** Runs one shell command with a time limit; the whole process tree dies on timeout. */
export function runCommand(cwd: string, command: string, timeoutMs: number, bash?: string): Promise<CommandRun> {
  return new Promise((resolve) => {
    const env = { ...process.env, CI: "1" };
    const child = bash
      ? spawn(bash, ["-c", command], { cwd, windowsHide: true, env })
      : spawn(command, { cwd, shell: true, windowsHide: true, env });
    let output = "";
    const keep = (chunk: Buffer) => {
      output = (output + chunk.toString("utf8")).slice(-OUTPUT_LIMIT * 4);
    };
    child.stdout.on("data", keep);
    child.stderr.on("data", keep);
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      if (process.platform === "win32" && child.pid) spawn("taskkill", ["/pid", String(child.pid), "/t", "/f"], { windowsHide: true });
      else child.kill("SIGKILL");
    }, timeoutMs);
    child.on("close", (code) => {
      clearTimeout(timer);
      const tail = output.slice(-OUTPUT_LIMIT);
      resolve({ command, exit: timedOut ? 124 : (code ?? 1), output: timedOut ? `${tail}\n(quá ${Math.round(timeoutMs / 1000)} giây, đã dừng)` : tail });
    });
    child.on("error", (error) => {
      clearTimeout(timer);
      resolve({ command, exit: 127, output: error.message });
    });
  });
}
