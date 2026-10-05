import type { BriefT, ToolArgs } from "../../shared/protocol.ts";
import type { Constraint, Ledger, Squad, Task } from "../ledger/types.ts";
import { mail, refuse, type Reply, type Turn } from "./context.ts";
import { constraintId, fullId, newId, newSquadId, squadNames, taskNames } from "./names.ts";
import { leadSquad, hcOf, squadById, worktreeOf } from "./lookup.ts";
import { statusText } from "./status.ts";

export function openSquad(turn: Turn, args: ToolArgs<"open_squad">): Reply {
  const supervisor = hcOf(turn);
  const { ledger, ctx } = turn;
  const id = newSquadId(ledger, (glob) => ctx.repo.branches(glob));
  const slug = args.slug;
  const clash = Object.values(ledger.squads).find((s) => s.ws === supervisor.ws && s.slug === slug && s.state !== "closed");
  if (clash) refuse("slug_taken", `slug ${slug} đang dùng cho ${clash.id}`, ["chọn slug khác"]);
  const constraints: Constraint[] = args.constraints.map((c, index) => {
    if (c.source.kind === "project-rule" && !ctx.project.rules.some((r) => r.id === c.source.ref)) {
      refuse("bad_source", `constraints.${index}: không có luật dự án ${c.source.ref}`, [`luật có: ${ctx.project.rules.map((r) => r.id).join(", ") || "(chưa có)"}`]);
    }
    return { id: constraintId(id, index + 1), text: c.text, source: c.source };
  });
  checkModel(turn, "lead", args.model, "model");
  const { name, branch, lead, leadDir } = squadNames(id, slug);
  const squad: Squad = {
    id,
    ws: supervisor.ws,
    slug,
    name,
    branch,
    goal: args.goal,
    acceptance: args.acceptance,
    constraints,
    model: args.model,
    state: "open",
    supervisor: supervisor.id,
    lead,
    leadWorktree: ctx.paths.worktree(supervisor.ws, leadDir),
  };
  ledger.squads[id] = squad;
  ledger.hcs[lead] = { id: lead, role: "lead", ws: supervisor.ws, squad: id, state: "open" };
  turn.effects.push({ kind: "create_branch", branch: squad.branch, from: ctx.project.base });
  turn.effects.push(worktreeOf(ledger, ledger.hcs[lead]!)!);
  turn.effects.push({ kind: "create_hc", hc: lead, kickoff: leadKickoff(turn, squad) });
  return { text: `Đã mở ${name} trên nhánh ${squad.branch}. Lead ${lead} đang được tạo.`, data: { squad: id, lead } };
}

function leadKickoff(turn: Turn, squad: Squad): string {
  return [
    `Squad ${squad.id}, nhánh ${squad.branch}. Thư mục của bạn là bản chỉ đọc của nhánh này, tự cập nhật mỗi khi có task merge.`,
    `Mục tiêu: ${squad.goal}`,
    `Acceptance:\n${squad.acceptance.map((a, i) => `  acceptance:${i}  ${a}`).join("\n")}`,
    `Ràng buộc có nguồn:\n${squad.constraints.map((c) => `  ${c.id}  ${c.text}  (${c.source.kind}: ${c.source.ref})`).join("\n") || "  (không có)"}`,
    `Luật dự án:\n${turn.ctx.project.rules.map((r) => `  ${r.id}  ${r.text}`).join("\n") || "  (không có)"}`,
    "Trong brief, required.source dùng: human với ref là id ràng buộc (s1-c1…), squad-goal với ref goal hoặc acceptance:<n>, project-rule với ref là id luật.",
    "Bước tiếp: plan.",
  ].join("\n\n");
}

export function amendSquad(turn: Turn, args: ToolArgs<"amend_squad">): Reply {
  const supervisor = hcOf(turn);
  const squad = squadById(turn, args.squad);
  if (squad.ws !== supervisor.ws) refuse("unknown_squad", `không có squad ${args.squad}`);
  if (squad.state !== "open") refuse("bad_state", `${squad.id} đang ở ${squad.state}, chỉ sửa được squad đang open`);
  const added = args.constraints.map((c, index) => {
    if (c.source.kind === "project-rule" && !turn.ctx.project.rules.some((r) => r.id === c.source.ref)) {
      refuse("bad_source", `constraints.${index}: không có luật dự án ${c.source.ref}`);
    }
    const constraint: Constraint = { id: constraintId(squad.id, squad.constraints.length + 1), text: c.text, source: c.source };
    squad.constraints.push(constraint);
    return constraint;
  });
  const firstAcceptance = squad.acceptance.length;
  squad.acceptance.push(...args.acceptance);
  mail(
    turn,
    squad.lead,
    `amend:${squad.id}`,
    [
      `Squad ${squad.id} có thay đổi từ Human: ${args.note}`,
      added.length ? `Ràng buộc mới:\n${added.map((c) => `  ${c.id}  ${c.text}  (${c.source.kind}: ${c.source.ref})`).join("\n")}` : "",
      args.acceptance.length ? `Acceptance mới:\n${args.acceptance.map((a, i) => `  acceptance:${firstAcceptance + i}  ${a}`).join("\n")}` : "",
      "Brief mới dùng các id này làm nguồn trong required. Việc phải sửa file, kể cả file luật hay tài liệu, giao thành task như mọi việc khác.",
    ]
      .filter(Boolean)
      .join("\n\n"),
    ["plan task mới nếu cần", "task đang chạy bị ảnh hưởng thì message Peer"],
  );
  return { text: `Đã ghi vào ${squad.id}: ${[...added.map((c) => c.id), ...args.acceptance.map((_, i) => `acceptance:${firstAcceptance + i}`)].join(", ")}. Lead đã nhận message.` };
}

export function closeSquad(turn: Turn, args: ToolArgs<"close_squad">): Reply {
  const supervisor = hcOf(turn);
  const squad = squadById(turn, args.squad);
  if (squad.ws !== supervisor.ws) refuse("unknown_squad", `không có squad ${args.squad}`);
  if (squad.state === "closed") refuse("bad_state", `${squad.id} đã đóng`);
  for (const hc of Object.values(turn.ledger.hcs)) {
    if (hc.squad !== squad.id) continue;
    // An HC lost earlier, say archived by hand, still has its Paseo workspace to archive.
    if (hc.state !== "closed" || hc.workspace) turn.effects.push({ kind: "archive_hc", hc: hc.id });
    hc.state = "closed";
  }
  turn.effects.push({ kind: "remove_worktree", path: squad.leadWorktree });
  for (const task of Object.values(turn.ledger.tasks)) {
    if (task.squad !== squad.id) continue;
    turn.effects.push({ kind: "remove_worktree", path: task.worktree });
    if (task.state === "merged") turn.effects.push({ kind: "delete_branch", branch: task.branch });
  }
  squad.state = "closed";
  wakeWaiting(turn);
  return { text: `Đã đóng ${squad.id}. Nhánh ${squad.branch} được giữ; ledger được giữ.` };
}

export function report(turn: Turn, args: ToolArgs<"report">): Reply {
  const squad = leadSquad(turn);
  if (squad.state !== "open") refuse("bad_state", `${squad.id} đang ở ${squad.state}`);
  squad.report = args;
  squad.state = "reporting";
  turn.effects.push({ kind: "ci_squad", squad: squad.id });
  return { text: `Đang chạy lại checks của mọi task đã merge trên ${squad.branch}. Kết quả sẽ gửi Supervisor.` };
}

/** The base branch moved on since the squad started: `behind` commits, merged into the check run unless it conflicted. */
export interface BaseDrift {
  branch: string;
  behind: number;
  conflict?: string[];
}

/** Runtime ran every merged task's checks on the squad branch head, with a newer base merged in. */
export function squadCiDone(turn: Turn, squadId: string, results: { task: string; command: string; exit: number; output: string }[], base?: BaseDrift): void {
  const squad = squadById(turn, squadId);
  if (squad.state !== "reporting") return;
  squad.state = "ready";
  const failed = results.filter((r) => r.exit !== 0);
  const lines = results.map((r) => `  ${r.exit === 0 ? "ok  " : "FAIL"} ${r.task}  ${r.command}`);
  const rep = squad.report!;
  const target = turn.ctx.project.base;
  const drift = !base
    ? `${target} chưa đi tiếp từ lúc mở squad.`
    : base.conflict
      ? `${base.branch} đã đi tiếp ${base.behind} commit, và gộp thử vào ${squad.branch} bị conflict ở: ${base.conflict.join(", ")}. CI chưa chạy. Human cần gộp ${base.branch} vào ${squad.branch} và gỡ conflict trước khi merge.`
      : `${base.branch} đã đi tiếp ${base.behind} commit; checks dưới đây chạy trên ${squad.branch} đã gộp thử ${base.branch}, không conflict.`;
  mail(
    turn,
    squad.supervisor,
    `ready:${squad.id}`,
    [
      `${squad.id} sẵn sàng. ${rep.summary}`,
      `Đã approve: ${rep.approved.join(", ") || "(không)"}`,
      `Còn mở: ${rep.open.join(", ") || "(không)"}`,
      `Rủi ro: ${rep.risks.join("; ") || "(không)"}`,
      drift,
      base?.conflict ? "" : `CI trên ${squad.branch}: ${failed.length ? `${failed.length} lệnh FAIL` : "tất cả ok"}\n${lines.join("\n")}`,
    ]
      .filter(Boolean)
      .join("\n"),
    ["báo Human", `Human merge ${squad.branch} lên ${target}`, `close_squad{squad:"${squad.id}"}`],
  );
}

export function plan(turn: Turn, args: ToolArgs<"plan">): Reply {
  const squad = leadSquad(turn);
  if (squad.state !== "open") refuse("bad_state", `${squad.id} đang ở ${squad.state}`);
  const { ledger, ctx } = turn;
  args.briefs.forEach((b, index) => checkBrief(turn, squad, b, index));
  const working = workingTasks(turn);
  if (working.length + args.briefs.length > ctx.limits.peers) {
    refuse("too_many_peers", `workspace đã có ${working.length} Peer đang làm, tối đa ${ctx.limits.peers}; lần plan này thêm ${args.briefs.length}`, [
      `Peer đang làm: ${working.map((t) => `${t.id} (${t.state})`).join(", ") || "(không có)"}`,
      `còn ${Math.max(0, ctx.limits.peers - working.length)} chỗ: giao ít task hơn, hoặc gộp task nhỏ`,
      "hết chỗ thì kết thúc lượt; squad sẽ gửi message khi có Peer xong",
    ]);
  }
  const base = ctx.repo.revParse(squad.branch);
  if (!base) refuse("no_branch", `chưa có nhánh ${squad.branch}`);
  const made: string[] = [];
  for (const b of args.briefs) {
    const id = newId(ledger, squad.id, "t");
    const hc = id;
    const { branch, dir } = taskNames(squad, id, b.title);
    if (ctx.repo.revParse(branch)) refuse("branch_taken", `nhánh ${branch} đã có trong repo`, ["đặt title khác cho brief này"]);
    const task: Task = {
      id,
      squad: squad.id,
      hc,
      title: b.title,
      briefs: [b],
      base,
      branch,
      worktree: ctx.paths.worktree(squad.ws, dir),
      state: "proposing",
      verdicts: [],
    };
    ledger.tasks[id] = task;
    ledger.hcs[hc] = { id: hc, role: "peer", ws: squad.ws, squad: squad.id, task: id, state: "open" };
    turn.effects.push(worktreeOf(ledger, ledger.hcs[hc]!)!);
    turn.effects.push({ kind: "create_hc", hc, kickoff: peerKickoff(task, b) });
    made.push(id);
  }
  return { text: `Đã giao ${made.join(", ")}.`, data: { tasks: made } };
}

/** An HC runs only on the models its role file lists. */
function checkModel(turn: Turn, role: "lead" | "peer", model: string | undefined, where: string): void {
  const allowed = turn.ctx.catalog.kinds[role]!.models;
  if (model !== undefined && !allowed.includes(model)) {
    refuse("bad_model", `${where}: ${role === "lead" ? "Lead" : "Peer"} không chạy model ${model}`, [`model được phép: ${allowed.join(", ")}`]);
  }
}

function checkBrief(turn: Turn, squad: Squad, b: BriefT, index: number): void {
  checkModel(turn, "peer", b.model, `briefs.${index}.model`);
  b.required.forEach((r, i) => {
    const where = `briefs.${index}.required.${i}`;
    const ok =
      (r.source.kind === "human" && squad.constraints.some((c) => c.id === fullId(r.source.ref, squad.id) && c.source.kind === "human")) ||
      (r.source.kind === "squad-goal" && (r.source.ref === "goal" || /^acceptance:(\d+)$/.test(r.source.ref) && Number(r.source.ref.split(":")[1]) < squad.acceptance.length)) ||
      (r.source.kind === "project-rule" && turn.ctx.project.rules.some((rule) => rule.id === r.source.ref));
    if (!ok) {
      refuse("required_without_source", `${where}: nguồn ${r.source.kind}:${r.source.ref} không có thật`, [
        "human: ref là id ràng buộc Human (s1-c1…); squad-goal: goal hoặc acceptance:<n>; project-rule: id luật",
        "nếu đây là lựa chọn của Lead thì chuyển sang leadApproach.current để Peer được chất vấn",
      ]);
    }
  });
}

/** The problem half of the brief. The Lead's approach stays sealed. */
export function peerKickoff(task: Task, b: BriefT): string {
  return [
    `Task ${task.id}: ${b.title}`,
    `Mục tiêu: ${b.objective}`,
    `Acceptance:\n${b.acceptance.map((a) => `  - ${a}`).join("\n")}`,
    `Ràng buộc bắt buộc:\n${b.required.map((r, i) => `  ${i}. ${r.text}  (nguồn ${r.source.kind}: ${r.source.ref})`).join("\n") || "  (không có)"}`,
    `Chưa biết:\n${b.unknowns.map((u) => `  - ${u}`).join("\n") || "  (không có)"}`,
    `Được sửa: ${b.scope.join(", ")}`,
    b.reads?.length ? `Nên đọc: ${b.reads.join(", ")}` : "",
    `Checks phải chạy:\n${b.checks.map((v) => `  $ ${v}`).join("\n")}`,
    "Lập phương án của bạn trước, chưa sửa code. Bước tiếp: propose.",
  ]
    .filter(Boolean)
    .join("\n\n");
}

export function status(turn: Turn, args: ToolArgs<"status">): Reply {
  const hc = hcOf(turn);
  const squads = Object.values(turn.ledger.squads).filter((s) =>
    hc.role === "lead" ? s.id === hc.squad : s.ws === hc.ws && (!args.squad || [s.id, s.name, s.slug].includes(args.squad)),
  );
  return { text: statusText(turn, squads) };
}

/** Tasks that hold a Peer: not merged, in a squad that is not closed. */
export function workingTasks(turn: Turn): Task[] {
  return Object.values(turn.ledger.tasks).filter((t) => t.state !== "merged" && turn.ledger.squads[t.squad]?.state !== "closed");
}

/** A Lead refused for lack of Peers waits; it hears when one frees up, since nothing else would wake it. */
export function markWaiting(ledger: Ledger, caller: string): void {
  const squad = Object.values(ledger.squads).find((s) => s.lead === caller);
  if (squad) squad.waitingForHc = true;
}

/** After a Peer frees up: mails every waiting Lead how many Peers it may plan now. */
export function wakeWaiting(turn: Turn): void {
  const free = turn.ctx.limits.peers - workingTasks(turn).length;
  if (free <= 0) return;
  for (const squad of Object.values(turn.ledger.squads)) {
    if (!squad.waitingForHc) continue;
    squad.waitingForHc = undefined;
    if (squad.state !== "open") continue;
    mail(turn, squad.lead, `hc_free:${squad.id}`, `Đã có ${free} chỗ Peer trống (tối đa ${turn.ctx.limits.peers} cho cả repo). Squad khác cũng có thể đang chờ, nên plan ngay phần bạn cần.`, ["plan"]);
  }
}
