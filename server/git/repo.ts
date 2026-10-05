import { execFileSync } from "node:child_process";
import type { RepoPort } from "../desk/context.ts";

/** Flags that stop a repo's own config from making the plugin run code it did not choose. */
const SAFE = ["-c", "core.hooksPath=", "-c", "core.fsmonitor=false", "-c", "core.pager=cat"];
const DIFF_LIMIT = 60_000;

export function git(cwd: string, args: string[]): string {
  return execFileSync("git", [...SAFE, ...args], { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], maxBuffer: 32 * 1024 * 1024 });
}

function tryGit(cwd: string, args: string[]): string | null {
  try {
    return git(cwd, args);
  } catch {
    return null;
  }
}

export function currentBranch(repo: string): string {
  return git(repo, ["rev-parse", "--abbrev-ref", "HEAD"]).trim();
}

/** Git facts for the desk, read synchronously from one repository and its worktrees. */
export class RealRepo implements RepoPort {
  private readonly repo: string;

  constructor(repo: string) {
    this.repo = repo;
  }

  revParse(ref: string): string | null {
    return tryGit(this.repo, ["rev-parse", "--verify", "--quiet", `${ref}^{commit}`])?.trim() || null;
  }

  readFile(ref: string, file: string): string | null {
    return tryGit(this.repo, ["show", `${ref}:${file.replaceAll("\\", "/")}`]);
  }

  branches(pattern: string): string[] {
    return git(this.repo, ["for-each-ref", "--format=%(refname:short)", `refs/heads/${pattern}`]).split("\n").filter(Boolean);
  }

  head(worktree: string): string {
    return git(worktree, ["rev-parse", "HEAD"]).trim();
  }

  dirty(worktree: string): boolean {
    return git(worktree, ["status", "--porcelain", "--untracked-files=all"]).trim().length > 0;
  }

  isAncestor(ancestor: string, descendant: string): boolean {
    return tryGit(this.repo, ["merge-base", "--is-ancestor", ancestor, descendant]) !== null;
  }

  changedFiles(base: string, sha: string): string[] {
    return git(this.repo, ["diff", "--name-only", `${base}..${sha}`]).split("\n").filter(Boolean);
  }

  diff(base: string, sha: string, file?: string): string {
    const stat = git(this.repo, ["diff", "--stat", `${base}..${sha}`]);
    const body = git(this.repo, ["diff", `${base}..${sha}`, ...(file ? ["--", file] : [])]);
    const text = `${stat}\n${body}`;
    return text.length > DIFF_LIMIT ? `${text.slice(0, DIFF_LIMIT)}\n… (cắt bớt; gọi diff với path để xem từng file)` : text;
  }
}
