import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import type { BriefT } from "../shared/protocol.ts";
import { loadCatalog, loadHarness, type Catalog, type Harness } from "../server/hc/catalog.ts";
import { CONTENT } from "../server/hc/content.ts";
import type { DeskContext, Effect, RepoPort } from "../server/desk/context.ts";
import { Desk, MemoryLedger } from "../server/desk/desk.ts";
import { emptyLedger } from "../server/ledger/types.ts";

export const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

const made: string[] = [];
process.once("exit", () => {
  for (const dir of made) fs.rmSync(dir, { recursive: true, force: true, maxRetries: 3 });
});

/** A fresh directory under the OS temp dir, removed when the test process exits. */
export function tempDir(name: string): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), `squad-${name}-`));
  made.push(dir);
  return dir;
}

/** A copy of the embedded roles.json, prompts and harness files, for tests to change. */
export function content(): Record<string, string> {
  return { ...CONTENT };
}

export function readHarness(): Harness {
  return loadHarness(CONTENT, "claude");
}

export function realCatalog(): Catalog {
  return loadCatalog(CONTENT, readHarness());
}

function lines(count: number, text: Record<number, string> = {}): string {
  return Array.from({ length: count }, (_, i) => text[i + 1] ?? `line ${i + 1}`).join("\n") + "\n";
}

export class FakeRepo implements RepoPort {
  refs = new Map<string, string>();
  heads = new Map<string, string>();
  dirtySet = new Set<string>();
  /** Files present at every ref. Line n of a generated file reads "line n". */
  files = new Map<string, string>([
    ["src/cart.ts", lines(120)],
    ["src/price.ts", lines(40)],
    ["docs/rules.md", lines(30, { 3: "Tiền: giá luôn chia hết", 4: "cho 100.", 5: "giá luôn chia hết cho 100 kể cả sau giảm giá" })],
  ]);
  /** Pairs "a>b" meaning a is an ancestor of b. */
  ancestry = new Set<string>();
  changed: string[] = ["src/cart.ts"];

  revParse(ref: string) {
    return this.refs.get(ref) ?? null;
  }
  readFile(_ref: string, file: string) {
    return this.files.get(file) ?? null;
  }
  branches(pattern: string) {
    const glob = new RegExp(`^${pattern.replace(/[.+^${}()|[\]\\]/g, "\\$&").replaceAll("*", ".*")}$`);
    return [...this.refs.keys()].filter((ref) => glob.test(ref));
  }
  head(worktree: string) {
    return this.heads.get(worktree) ?? "0000000";
  }
  dirty(worktree: string) {
    return this.dirtySet.has(worktree);
  }
  isAncestor(a: string, b: string) {
    return a === b || this.ancestry.has(`${a}>${b}`);
  }
  changedFiles() {
    return this.changed;
  }
  diff(base: string, sha: string, file?: string) {
    return `diff ${base}..${sha}${file ? ` -- ${file}` : ""}`;
  }
}

export function makeBrief(overrides: Partial<BriefT> = {}): BriefT {
  return {
    title: "discount-code",
    objective: "Đơn hàng áp được mã giảm giá phần trăm",
    acceptance: ["test giảm giá pass"],
    required: [],
    unknowns: [],
    scope: ["src/**"],
    checks: ["npm test"],
    leadApproach: {
      approach: "Thêm hàm applyDiscount trong cart.ts",
      steps: [
        { id: "s1", text: "viết applyDiscount" },
        { id: "s2", text: "gọi từ checkout" },
      ],
      current: [{ id: "c1", text: "lưu phần trăm dạng float" }],
      risks: ["làm tròn"],
    },
    ...overrides,
  };
}

export const PROPOSAL = {
  approach: "Tách module price.ts tính giảm giá bằng số nguyên cent",
  steps: ["viết price.ts", "nối vào cart"],
  risks: ["đổi kiểu tiền"],
  assumptions: [],
  questions: [],
};

export const FILE_EVIDENCE = { kind: "file" as const, path: "src/price.ts", lines: [1, 10] as [number, number] };
export const CMD_EVIDENCE = { kind: "command" as const, command: "node -e 0.1+0.2", exit: 0, output: "0.30000000000000004" };

export interface World {
  desk: Desk;
  store: MemoryLedger;
  repo: FakeRepo;
  ctx: DeskContext;
  sup: string;
  squad: string;
  lead: string;
  effects: Effect[];
  call(caller: string, tool: string, args: unknown): ReturnType<Desk["call"]>;
}

/** A workspace with a live Supervisor, one open squad and its live Lead. */
export function world(options: { acceptance?: string[] } = {}): World {
  const repo = new FakeRepo();
  const ctx: DeskContext = {
    catalog: realCatalog(),
    repo,
    project: { base: "main", rules: [{ id: "R1", text: "không đổi API công khai" }] },
    limits: { peers: 4 },
    paths: { worktree: (ws, slot) => `/wt/${ws}/${slot}` },
    now: () => "2026-10-03T00:00:00.000Z",
  };
  const store = new MemoryLedger(emptyLedger());
  const desk = new Desk(store, ctx);
  const effects: Effect[] = [];
  const call = (caller: string, tool: string, args: unknown) => {
    const result = desk.call(caller, tool, args);
    effects.push(...result.effects);
    return result;
  };
  const sup = (desk.supervisor("ws1") as { hc: string }).hc;
  desk.event({ kind: "hc_filled", hc: sup, agentId: "agent-sup" });
  const opened = call(sup, "open_squad", {
    goal: "Hỗ trợ mã giảm giá",
    acceptance: options.acceptance ?? ["test giảm giá pass"],
    constraints: [{ text: "dùng tiền tệ VND", source: { kind: "human", ref: "chat 10:02" } }],
    slug: "discount",
  });
  if (!opened.ok) throw new Error(opened.text);
  const { squad, lead } = opened.data as { squad: string; lead: string };
  desk.event({ kind: "hc_filled", hc: lead, agentId: "agent-lead" });
  repo.refs.set("squad/s1-discount", "base0");
  return { desk, store, repo, ctx, sup, squad, lead, effects, call };
}

export interface Planned {
  task: string;
  peer: string;
  worktree: string;
}

/** Plans one brief and fills its Peer HC. */
export function planTask(w: World, brief: BriefT = makeBrief()): Planned {
  const result = w.call(w.lead, "plan", { briefs: [brief] });
  if (!result.ok) throw new Error(`${result.text} ${result.fix?.join("; ")}`);
  const ledger = w.store.ledger;
  const task = Object.values(ledger.tasks).at(-1)!;
  w.desk.event({ kind: "hc_filled", hc: task.hc, agentId: `agent-${task.id}` });
  return { task: task.id, peer: task.hc, worktree: task.worktree };
}

/** Plans a task and agrees with the Lead, so the task is running. */
export function runningTask(w: World, brief?: BriefT): Planned {
  const t = planTask(w, brief);
  must(w.call(t.peer, "propose", PROPOSAL));
  must(w.call(t.peer, "position", { agree: true, comparison: [{ point: "kiểu tiền", mine: "cent", lead: "float", whyLeadIsFine: "phạm vi nhỏ" }] }));
  return t;
}

/** Hands off at sha "h1" and lets the server finish checking with the given exits. */
export function checkedTask(w: World, exits: { claimed: number; actual: number } = { claimed: 0, actual: 0 }): Planned {
  const t = runningTask(w);
  w.repo.heads.set(t.worktree, "h1");
  w.repo.ancestry.add("base0>h1");
  must(w.call(t.peer, "handoff", handoffArgs("h1", exits.claimed)));
  w.effects.push(...w.desk.event({ kind: "ci_done", task: t.task, sha: "h1", results: [{ command: "npm test", exit: exits.actual, output: exits.actual ? "1 failing" : "ok" }] }));
  return t;
}

export function handoffArgs(sha: string, exit = 0) {
  return {
    outcome: "complete",
    sha,
    summary: "xong giảm giá",
    checks: [{ command: "npm test", exit, output: exit ? "1 failing" : "ok" }],
    skipped: [],
    unknowns: [],
  };
}

export function must(result: { ok: boolean; text: string; fix?: string[] }) {
  if (!result.ok) throw new Error(`refused: ${result.text} ${result.fix?.join("; ") ?? ""}`);
  return result;
}

export function mailsTo(w: World, hc: string) {
  return w.effects.filter((e): e is Extract<Effect, { kind: "mail" }> => e.kind === "mail" && e.to === hc);
}
