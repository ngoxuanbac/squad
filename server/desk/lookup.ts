import type { EvidenceT, LeadApproachT } from "../../shared/protocol.ts";
import type { Ask, Debate, Hc, Ledger, Squad, StoredEvidence, Task } from "../ledger/types.ts";
import { hashOf, mail, refuse, type Effect, type Turn } from "./context.ts";
import { newId } from "./names.ts";

export function hcOf(turn: Turn, id = turn.caller): Hc {
  const hc = turn.ledger.hcs[id];
  if (!hc || hc.state === "closed") refuse("unknown_hc", `không có HC ${id} đang hoạt động`);
  return hc;
}

/** The one squad a Lead leads. */
export function leadSquad(turn: Turn): Squad {
  const hc = hcOf(turn);
  const squad = hc.squad ? turn.ledger.squads[hc.squad] : undefined;
  if (!squad || squad.lead !== hc.id) refuse("not_lead", "HC này không dẫn squad nào");
  return squad;
}

/** A squad by its id "s1", its name "s1-pricing", or the slug of an unclosed squad, "pricing". */
export function squadById(turn: Turn, id: string): Squad {
  const all = Object.values(turn.ledger.squads);
  const squad = turn.ledger.squads[id] ?? all.find((s) => s.name === id) ?? all.find((s) => s.slug === id && s.state !== "closed");
  if (!squad) refuse("unknown_squad", `không có squad ${id}`, ["xem danh sách squad bằng status"]);
  return squad;
}

/** A task of the calling Lead's squad. */
export function leadTask(turn: Turn, id: string): Task {
  const squad = leadSquad(turn);
  const task = turn.ledger.tasks[id];
  if (!task || task.squad !== squad.id) refuse("unknown_task", `squad ${squad.id} không có task ${id}`);
  return task;
}

/** The task the calling Peer works on. */
export function peerTask(turn: Turn): Task {
  const hc = hcOf(turn);
  const task = hc.task ? turn.ledger.tasks[hc.task] : undefined;
  if (!task || task.hc !== hc.id) refuse("no_task", "HC này không giữ task nào");
  return task;
}

export function brief(task: Task) {
  return task.briefs[task.briefs.length - 1]!;
}

export function debatesOf(turn: Turn, task: Task): Debate[] {
  return Object.values(turn.ledger.debates).filter((d) => d.task === task.id);
}

export const CLOSED_DEBATE = new Set(["conceded", "withdrawn", "decided"]);

export function openDebates(turn: Turn, task: Task): Debate[] {
  return debatesOf(turn, task).filter((d) => !CLOSED_DEBATE.has(d.state));
}

export function expectState(task: Task, allowed: Task["state"][], doing: string): void {
  if (!allowed.includes(task.state)) {
    refuse("bad_state", `task ${task.id} đang ở ${task.state}, không thể ${doing}`, [`chỉ ${doing} khi task ở ${allowed.join(" hoặc ")}`]);
  }
}

/** Checks evidence against the repo at the task's base and stores it with an id ("s1-t2-e1") and a hash. */
export function storeEvidence(turn: Turn, items: EvidenceT[], task: Task, by: string): StoredEvidence[] {
  return items.map((item, index) => {
    if (item.kind === "file") checkFile(turn, item, task.base, index);
    return { id: newId(turn.ledger, task.id, "e"), hash: hashOf(item), by, item };
  });
}

const squash = (text: string) => text.replace(/\s+/g, " ").trim();

/** The file exists at `base`, the lines are inside it, and the quote is really there. */
function checkFile(turn: Turn, item: Extract<EvidenceT, { kind: "file" }>, base: string, index: number): void {
  const content = turn.ctx.repo.readFile(base, item.path);
  if (content === null) refuse("bad_evidence", `bằng chứng ${index}: file ${item.path} không có ở ${base.slice(0, 12)}`, ["trỏ tới file có thật ở base"]);
  const lines = content.replace(/\r\n/g, "\n").replace(/\n$/, "").split("\n");
  if (item.lines && (item.lines[0] > item.lines[1] || item.lines[1] > lines.length)) {
    refuse("bad_evidence", `bằng chứng ${index}: dòng ${item.lines.join("-")} nằm ngoài ${item.path} (${lines.length} dòng)`);
  }
  if (item.quote && squash(item.quote)) {
    const shown = item.lines ? lines.slice(item.lines[0] - 1, item.lines[1]) : lines;
    if (!squash(shown.join("\n")).includes(squash(item.quote))) {
      const where = item.lines ? `dòng ${item.lines.join("-")} của ${item.path}` : item.path;
      refuse("bad_quote", `bằng chứng ${index}: ${where} ở ${base.slice(0, 12)} không chứa câu trích`, ["chép đúng câu trong file, hoặc sửa lines cho khớp"]);
    }
  }
}

/**
 * Evidence that points at something not yet submitted in this debate. Holding or rebutting needs at least one.
 * Rewording a quote does not make it new: a file range inside one already cited, the same command, or the same
 * passage of a document counts as old.
 */
export function freshEvidence(debate: Debate, items: EvidenceT[], fix: string[]): EvidenceT[] {
  const fresh = items.filter((item) => !debate.evidence.some((old) => covers(old.item, item)));
  if (!fresh.length) refuse("no_new_evidence", "không tranh luận lại khi không có bằng chứng mới", fix);
  return fresh;
}

const samePath = (a: string, b: string) => a.replaceAll("\\", "/").replace(/^\.\//, "") === b.replaceAll("\\", "/").replace(/^\.\//, "");

function covers(old: EvidenceT, item: EvidenceT): boolean {
  if (old.kind === "file" && item.kind === "file") {
    if (!samePath(old.path, item.path)) return false;
    if (!old.lines) return true;
    return !!item.lines && old.lines[0] <= item.lines[0] && item.lines[1] <= old.lines[1];
  }
  if (old.kind === "command" && item.kind === "command") return squash(old.command) === squash(item.command);
  if (old.kind === "doc" && item.kind === "doc") return squash(old.ref) === squash(item.ref) && squash(old.quote) === squash(item.quote);
  return false;
}

/** A step or choice `ref` must exist in the Lead's approach. */
export function checkTarget(target: { kind: string; ref?: string }, approach: LeadApproachT): void {
  if (target.kind === "step" && !approach.steps.some((s) => s.id === target.ref)) {
    refuse("bad_target", `không có bước ${target.ref} trong cách làm của Lead`, [`chọn một trong: ${approach.steps.map((s) => s.id).join(", ")}`]);
  }
  if (target.kind === "current" && !approach.current.some((s) => s.id === target.ref)) {
    refuse("bad_target", `không có lựa chọn ${target.ref} trong cách làm của Lead`, [`chọn một trong: ${approach.current.map((s) => s.id).join(", ") || "(không có)"}`]);
  }
}

/** Counter-evidence must point somewhere other than the brief itself. */
export function checkCounterEvidence(items: EvidenceT[]): void {
  const onlyBrief = items.every((item) => item.kind === "doc" && /^brief\b/i.test(item.ref.trim()));
  if (onlyBrief) {
    refuse("brief_is_not_evidence", "phản chứng chỉ trỏ lại brief", ["đưa bằng chứng từ repo, output lệnh hoặc tài liệu ngoài brief"]);
  }
}

/** A question from a squad's Lead to its Supervisor about a debate or a Peer's concern; returns its id. */
export function escalate(turn: Turn, squad: Squad, question: string, about: Pick<Ask, "debate" | "concern">): string {
  const id = newId(turn.ledger, squad.id, "a");
  turn.ledger.asks[id] = { id, from: squad.lead, to: squad.supervisor, question, state: "open", ...about };
  mail(turn, squad.supervisor, `ask:${id}`, question, [`answer{ask:"${id}", text, source:"human"|"squad-goal"}`]);
  return id;
}

/** Where an HC works: the Lead at its squad's head, a Peer on its task branch. */
export function worktreeOf(ledger: Ledger, hc: Hc): Extract<Effect, { kind: "open_worktree" }> | undefined {
  const squad = hc.squad ? ledger.squads[hc.squad] : undefined;
  if (!squad) return undefined;
  if (hc.role === "lead") return { kind: "open_worktree", hc: hc.id, path: squad.leadWorktree, at: squad.branch };
  const task = hc.task ? ledger.tasks[hc.task] : undefined;
  if (task) return { kind: "open_worktree", hc: hc.id, path: task.worktree, at: squad.branch, branch: task.branch };
  return undefined;
}

export function hcName(turn: Turn, id: string): string {
  const hc = turn.ledger.hcs[id];
  if (!hc) return id;
  if (hc.role === "peer") return `Peer (${id})`;
  return `${hc.role === "lead" ? "Lead" : "Supervisor"} (${id})`;
}
