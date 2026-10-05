import { createHash } from "node:crypto";
import type { Catalog } from "../hc/catalog.ts";
import type { Ledger } from "../ledger/types.ts";

/** Git facts the desk reads while deciding. Calls are synchronous so a decision never awaits. */
export interface RepoPort {
  revParse(ref: string): string | null;
  /** Content of `path` at `ref`, or null when the file does not exist there. */
  readFile(ref: string, path: string): string | null;
  /** Local branches matching a glob such as "squad/s1-*". */
  branches(pattern: string): string[];
  head(worktree: string): string;
  dirty(worktree: string): boolean;
  /** True when `ancestor` is an ancestor of, or equal to, `descendant`. */
  isAncestor(ancestor: string, descendant: string): boolean;
  changedFiles(base: string, sha: string): string[];
  diff(base: string, sha: string, path?: string): string;
}

export interface DeskContext {
  catalog: Catalog;
  repo: RepoPort;
  project: { base: string; rules: { id: string; text: string }[] };
  /** Peers working at once in the workspace. */
  limits: { peers: number };
  /** Where a working copy lives: `dir` is relative, like "s1-pricing/lead" or "s1-pricing/s1-t2-format". */
  paths: { worktree(ws: string, dir: string): string };
  now(): string;
}

export type Effect =
  | { kind: "mail"; to: string; topic: string; text: string; next: string[] }
  | { kind: "create_branch"; branch: string; from: string }
  /**
   * The working copy of an HC at `path`. With `branch`, a new branch made from `at` (a Peer's task). Without, a
   * read-only copy detached at `at`, a branch or a SHA, moved there if it already exists (a Lead).
   */
  | { kind: "open_worktree"; hc: string; path: string; at: string; branch?: string }
  | { kind: "create_hc"; hc: string; kickoff: string }
  | { kind: "archive_hc"; hc: string }
  | { kind: "remove_worktree"; path: string }
  | { kind: "delete_branch"; branch: string }
  | { kind: "run_ci"; task: string; sha: string }
  | { kind: "fast_forward"; task: string; branch: string; sha: string }
  | { kind: "ci_squad"; squad: string };

export interface Reply {
  text: string;
  data?: unknown;
  /** A refusal that still keeps the state changes made while deciding it. */
  error?: { code: string; fix: string[] };
}

/** Raised by a handler to refuse a call. The ledger draft is dropped, so nothing changes. */
export class Refusal extends Error {
  readonly code: string;
  readonly fix: string[];
  constructor(code: string, message: string, fix: string[] = []) {
    super(message);
    this.code = code;
    this.fix = fix;
  }
}

export function refuse(code: string, message: string, fix: string[] = []): never {
  throw new Refusal(code, message, fix);
}

/** What a handler sees: the ledger draft, the context, who called, and a place to queue effects. */
export interface Turn {
  ledger: Ledger;
  ctx: DeskContext;
  caller: string;
  effects: Effect[];
}

/** Each kind of id counts on its own, in lowercase: s1, s2 for squads, t1, t2 for tasks, and so on. */
export function nextId(ledger: Ledger, prefix: string): string {
  ledger.seq += 1;
  ledger.counters ??= {};
  const next = (ledger.counters[prefix] ?? 0) + 1;
  ledger.counters[prefix] = next;
  return `${prefix.toLowerCase()}${next}`;
}

export function hashOf(value: unknown): string {
  return createHash("sha256").update(canonical(value)).digest("hex").slice(0, 16);
}

function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  if (value && typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>)
      .filter(([, v]) => v !== undefined)
      .sort(([a], [b]) => (a < b ? -1 : 1));
    return `{${entries.map(([k, v]) => `${JSON.stringify(k)}:${canonical(v)}`).join(",")}}`;
  }
  return JSON.stringify(value);
}

export function mail(turn: Turn, to: string, topic: string, text: string, next: string[]): void {
  turn.effects.push({ kind: "mail", to, topic, text, next });
}
