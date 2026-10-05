import { describeIssues, TOOL_SCHEMAS, type ToolName } from "../../shared/protocol.ts";
import { kindOf } from "../hc/catalog.ts";
import type { Ledger } from "../ledger/types.ts";
import { Refusal, refuse, type DeskContext, type Effect, type Reply, type Turn } from "./context.ts";
import { answer, ask, message, turnEnded } from "./asks.ts";
import { amendTask, position, propose, rebut, respond, withdraw } from "./debate.ts";
import { worktreeOf } from "./lookup.ts";
import { fullId, SUPERVISOR } from "./names.ts";
import { amendSquad, closeSquad, markWaiting, openSquad, plan, report, squadCiDone, status, type BaseDrift } from "./squads.ts";
import { ciDone, conflicted, diff, handoff, merged, verdict } from "./work.ts";

export interface LedgerTx {
  transact<T>(decide: (ledger: Ledger) => T): T;
}

export interface CallResult {
  ok: boolean;
  text: string;
  code?: string;
  fix?: string[];
  data?: unknown;
  effects: Effect[];
}

const HANDLERS: { [T in ToolName]: (turn: Turn, args: never) => Reply } = {
  open_squad: openSquad,
  close_squad: closeSquad,
  amend_squad: amendSquad,
  status,
  message,
  ask,
  answer,
  plan,
  respond,
  amend_task: amendTask,
  verdict,
  diff,
  report,
  propose,
  position,
  rebut,
  withdraw,
  handoff,
};

export type DeskEvent =
  | { kind: "hc_filled"; hc: string; agentId: string; secretHash?: string; workspace?: string }
  | { kind: "hc_secret"; hc: string; secretHash: string }
  | { kind: "hc_lost"; hc: string }
  | { kind: "ci_done"; task: string; sha: string; results: { command: string; exit: number; output: string }[] }
  | { kind: "conflict"; task: string; files: string[] }
  | { kind: "merged"; task: string }
  | { kind: "fast_forward_failed"; task: string; reason: string }
  | { kind: "squad_ci_done"; squad: string; results: { task: string; command: string; exit: number; output: string }[]; base?: BaseDrift }
  | { kind: "turn_ended"; hc: string; outcome: "completed" | "failed" | "canceled"; lastText: string };

/** Decides one tool call on `ledger` in place. Throws Refusal when the call is refused. */
export function decideCall(ledger: Ledger, ctx: DeskContext, caller: string, tool: string, raw: unknown): { reply: Reply; effects: Effect[] } {
  const hc = ledger.hcs[caller];
  if (!hc || hc.state === "closed") refuse("unknown_hc", "HC này không còn hoạt động");
  const allowed = kindOf(ctx.catalog, hc).squadTools;
  if (!(tool in HANDLERS) || !allowed.includes(tool)) {
    refuse("not_in_toolset", `${tool} không thuộc tool set của HC này`, [`tool được dùng: ${allowed.join(", ")}`]);
  }
  const parsed = TOOL_SCHEMAS[tool as ToolName].safeParse(raw ?? {});
  if (!parsed.success) refuse("bad_args", `tham số của ${tool} không hợp lệ`, describeIssues(parsed.error));
  fullIds(parsed.data as Record<string, unknown>, hc.squad);
  const turn: Turn = { ledger, ctx, caller, effects: [] };
  const reply = (HANDLERS[tool as ToolName] as (turn: Turn, args: unknown) => Reply)(turn, parsed.data);
  keepKickoffs(ledger, turn.effects);
  return { reply, effects: turn.effects };
}

/** Fields that name a squad thing by id. */
const ID_FIELDS = ["ask", "challenge", "task", "to", "answers"];

/** Ids in full and lowercase, as names.ts reads them. */
function fullIds(args: Record<string, unknown>, squad: string | undefined): void {
  if (typeof args.squad === "string") args.squad = args.squad.trim().toLowerCase();
  for (const key of ID_FIELDS) {
    if (typeof args[key] === "string") args[key] = fullId(args[key], squad);
  }
}

function keepKickoffs(ledger: Ledger, effects: Effect[]): void {
  for (const effect of effects) {
    if (effect.kind === "create_hc" && ledger.hcs[effect.hc]) ledger.hcs[effect.hc]!.kickoff = effect.kickoff;
  }
}

/** Work that was in flight when the plugin stopped, restarted from what the ledger knows. */
export function decideRecovery(ledger: Ledger, ctx: DeskContext): Effect[] {
  const effects: Effect[] = [];
  for (const squad of Object.values(ledger.squads)) {
    if (squad.state === "reporting") effects.push({ kind: "ci_squad", squad: squad.id });
  }
  for (const task of Object.values(ledger.tasks)) {
    const squad = ledger.squads[task.squad];
    if (!squad || squad.state === "closed") continue;
    if (task.state === "ci_running" && task.handoff) effects.push({ kind: "run_ci", task: task.id, sha: task.handoff.sha });
    if (task.state === "approved" && task.ci) effects.push({ kind: "fast_forward", task: task.id, branch: squad.branch, sha: task.ci.sha });
  }
  for (const hc of Object.values(ledger.hcs)) {
    if (hc.state !== "open" || !hc.kickoff || hc.role === "supervisor") continue;
    const squad = hc.squad ? ledger.squads[hc.squad] : undefined;
    if (!squad || squad.state === "closed") continue;
    const worktree = worktreeOf(ledger, hc);
    if (!worktree) continue;
    if (hc.role === "lead") effects.push({ kind: "create_branch", branch: squad.branch, from: ctx.project.base });
    effects.push(worktree, { kind: "create_hc", hc: hc.id, kickoff: hc.kickoff });
  }
  return effects;
}

/** Applies one runtime event on `ledger` in place. */
export function decideEvent(ledger: Ledger, ctx: DeskContext, event: DeskEvent): Effect[] {
  const turn: Turn = { ledger, ctx, caller: "", effects: [] };
  switch (event.kind) {
    case "hc_filled": {
      const hc = ledger.hcs[event.hc];
      if (hc && hc.state !== "closed") {
        hc.state = "filled";
        hc.agentId = event.agentId;
        if (event.secretHash) hc.secretHash = event.secretHash;
        if (event.workspace) hc.workspace = event.workspace;
      }
      break;
    }
    case "hc_secret": {
      const hc = ledger.hcs[event.hc];
      if (hc && hc.state !== "closed") hc.secretHash = event.secretHash;
      break;
    }
    case "hc_lost": {
      const hc = ledger.hcs[event.hc];
      if (!hc || hc.state === "closed") break;
      if (hc.role === "supervisor") {
        // The seat stays: squads, questions and held mail keep pointing at it until the Human starts a new one.
        hc.state = "open";
        hc.agentId = undefined;
        hc.secretHash = undefined;
        break;
      }
      hc.state = "closed";
      const dropped = Object.values(ledger.asks).filter((a) => a.from === hc.id && a.state === "open");
      for (const ask of dropped) ask.state = "dropped";
      const squad = hc.squad ? ledger.squads[hc.squad] : undefined;
      const note = dropped.length ? ` Câu hỏi của HC này không cần trả lời nữa: ${dropped.map((a) => a.id).join(", ")}.` : "";
      if (squad) turn.effects.push({ kind: "mail", to: squad.supervisor, topic: `hc_lost:${hc.id}`, text: `HC ${hc.id} không còn agent: agent bị archive, tạo lỗi, hoặc không tìm thấy sau khi khởi động lại.${note}`, next: ["status", "báo Human nếu cần quyết đóng squad"] });
      break;
    }
    case "ci_done":
      ciDone(turn, event.task, event.sha, event.results);
      break;
    case "conflict":
      conflicted(turn, event.task, event.files);
      break;
    case "merged":
      merged(turn, event.task);
      break;
    case "fast_forward_failed": {
      const task = ledger.tasks[event.task];
      if (!task || task.state !== "approved" || !task.ci) break;
      const sha = task.ci.sha;
      task.state = "ci_running";
      task.ci = undefined;
      turn.effects.push({ kind: "run_ci", task: task.id, sha });
      const squad = ledger.squads[task.squad];
      if (squad) turn.effects.push({ kind: "mail", to: squad.lead, topic: `ci_rerun:${task.id}`, text: `Không đưa được ${task.id} vào ${squad.branch} (${event.reason}). Task đang gộp và chạy lại CI; message done mới sẽ tới.`, next: [] });
      break;
    }
    case "squad_ci_done":
      squadCiDone(turn, event.squad, event.results, event.base);
      break;
    case "turn_ended":
      turnEnded(turn, event.hc, event.outcome, event.lastText);
      break;
  }
  return turn.effects;
}

/**
 * Opens the workspace's one Supervisor HC for a new agent. A Supervisor that replaces a lost one sits in the same
 * HC, so the squads, questions and mail addressed to "supervisor" reach it.
 */
export function decideSupervisor(ledger: Ledger, ws: string, secretHash?: string): string {
  const id = SUPERVISOR;
  if (ledger.hcs[id]?.state === "filled") refuse("supervisor_exists", `workspace đã có Supervisor ${id}`);
  ledger.hcs[id] = { id, role: "supervisor", ws, state: "open", secretHash };
  return id;
}

/** The desk over a ledger store: every call and event is one transaction. */
export class Desk {
  private readonly store: LedgerTx;
  private readonly ctx: DeskContext;

  constructor(store: LedgerTx, ctx: DeskContext) {
    this.store = store;
    this.ctx = ctx;
  }

  call(caller: string, tool: string, args: unknown): CallResult {
    try {
      const { reply, effects } = this.store.transact((ledger) => decideCall(ledger, this.ctx, caller, tool, args));
      return { ok: !reply.error, text: reply.text, code: reply.error?.code, fix: reply.error?.fix, data: reply.data, effects };
    } catch (error) {
      if (!(error instanceof Refusal)) throw error;
      if (error.code === "too_many_peers") this.store.transact((ledger) => markWaiting(ledger, caller));
      return { ok: false, text: error.message, code: error.code, fix: error.fix, effects: [] };
    }
  }

  event(event: DeskEvent): Effect[] {
    return this.store.transact((ledger) => decideEvent(ledger, this.ctx, event));
  }

  recover(): Effect[] {
    return this.store.transact((ledger) => decideRecovery(ledger, this.ctx));
  }

  supervisor(ws: string, secretHash?: string): { ok: true; hc: string } | { ok: false; text: string } {
    try {
      return { ok: true, hc: this.store.transact((ledger) => decideSupervisor(ledger, ws, secretHash)) };
    } catch (error) {
      if (error instanceof Refusal) return { ok: false, text: error.message };
      throw error;
    }
  }
}

/** In-memory ledger store with the same transaction rule, for tests and dry runs. */
export class MemoryLedger implements LedgerTx {
  ledger: Ledger;
  constructor(ledger: Ledger) {
    this.ledger = ledger;
  }
  transact<T>(decide: (ledger: Ledger) => T): T {
    const draft = structuredClone(this.ledger);
    const result = decide(draft);
    this.ledger = draft;
    return result;
  }
}
