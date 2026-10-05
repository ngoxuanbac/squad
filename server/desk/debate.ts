import type { ChallengeT, LeadApproachT, PatchT, ToolArgs } from "../../shared/protocol.ts";
import type { Debate, Task } from "../ledger/types.ts";
import { hashOf, mail, refuse, type Reply, type Turn } from "./context.ts";
import { newId } from "./names.ts";
import {
  brief,
  checkCounterEvidence,
  checkTarget,
  escalate,
  CLOSED_DEBATE,
  expectState,
  freshEvidence,
  leadSquad,
  leadTask,
  openDebates,
  peerTask,
  squadById,
  storeEvidence,
} from "./lookup.ts";

export function propose(turn: Turn, args: ToolArgs<"propose">): Reply {
  const task = peerTask(turn);
  expectState(task, ["proposing"], "propose");
  const b = brief(task);
  const squad = squadById(turn, task.squad);
  for (const concern of args.requiredConcerns ?? []) {
    const required = b.required[concern.index];
    if (!required) refuse("bad_index", `requiredConcerns: không có required ${concern.index}`);
    storeEvidence(turn, concern.evidence, task, task.hc);
    const question = `Peer của ${task.id} lo ngại ràng buộc "${required.text}" (${required.source.kind}: ${required.source.ref}): ${concern.concern}`;
    escalate(turn, squad, question, { concern: { task: task.id, index: concern.index } });
  }
  const { requiredConcerns: _, ...plan } = args;
  task.proposal = { ...plan, hash: hashOf(plan) };
  task.state = "debating";
  const approach = b.leadApproach;
  mail(
    turn,
    squad.lead,
    `proposal:${task.id}`,
    `Đề xuất của Peer cho ${task.id}:\n${plan.approach}\nBước:\n${plan.steps.map((s, i) => `  ${i + 1}. ${s}`).join("\n")}\nRủi ro: ${plan.risks.join("; ")}`,
    ["chờ position của Peer", "đề xuất hay hơn thì respond{action:\"concede\"} khi Peer phản biện"],
  );
  return {
    text: [
      "Đã khóa đề xuất của bạn. Đây là cách làm của Lead, đặt cạnh đề xuất của bạn.",
      `Lead: ${approach.approach}`,
      `Bước của Lead:\n${approach.steps.map((s) => `  ${s.id}. ${s.text}`).join("\n")}`,
      `Lựa chọn đang dùng (được chất vấn):\n${approach.current.map((s) => `  ${s.id}. ${s.text}`).join("\n") || "  (không có)"}`,
      `Rủi ro Lead thấy: ${approach.risks.join("; ") || "(không)"}`,
      `Bạn: ${plan.approach}`,
      "So từng điểm khác nhau. Điểm nào có bằng chứng làm đổi kết quả thì phản biện; còn lại thì đồng ý kèm so sánh.",
      "Bước tiếp: position.",
    ].join("\n\n"),
  };
}

export function position(turn: Turn, args: ToolArgs<"position">): Reply {
  const task = peerTask(turn);
  expectState(task, ["debating"], "position");
  const squad = squadById(turn, task.squad);
  task.state = "running";
  if (args.agree) {
    mail(
      turn,
      squad.lead,
      `agree:${task.id}`,
      `Peer đồng ý cách làm cho ${task.id}:\n${args.comparison.map((c) => `  ${c.point}: Peer "${c.mine}" / Lead "${c.lead}" — ${c.whyLeadIsFine}`).join("\n")}`,
      ["chờ handoff"],
    );
    return { text: "Đã ghi đồng ý. Bắt đầu làm. Bước tiếp: commit rồi handoff." };
  }
  const approach = brief(task).leadApproach;
  args.challenges.forEach((c) => checkTarget(c.target, approach));
  const ids = args.challenges.map((challenge) => {
    const id = newId(turn.ledger, task.id, "d");
    const evidence = storeEvidence(turn, challenge.evidence, task, task.hc);
    turn.ledger.debates[id] = {
      id,
      task: task.id,
      challenge,
      state: "open",
      holds: 0,
      evidence,
      rounds: [{ by: "peer", action: "challenge", evidence: evidence.map((e) => e.id), text: challenge.claim, at: turn.ctx.now() }],
    };
    return id;
  });
  mail(
    turn,
    squad.lead,
    `debate:${task.id}`,
    args.challenges
      .map((c, i) => `${ids[i]}: ${c.target.kind}${c.target.ref ? ` ${c.target.ref}` : ""} — ${c.claim}\n  hậu quả: ${c.impact}\n  đề nghị: ${c.proposal}\n  bằng chứng: ${turn.ledger.debates[ids[i]!]!.evidence.map((e) => e.id).join(", ")}`)
      .join("\n"),
    ids.map((id) => `respond{challenge:"${id}", action:"concede"|"hold"|"escalate"}`),
  );
  return {
    text: `Đã mở tranh luận ${ids.join(", ")}. Bước và path trong affects đang khóa; làm tiếp phần còn lại. Không handoff được khi tranh luận còn mở.`,
  };
}

function leadDebate(turn: Turn, id: string): { debate: Debate; task: Task } {
  const squad = leadSquad(turn);
  const debate = turn.ledger.debates[id];
  const task = debate ? turn.ledger.tasks[debate.task] : undefined;
  if (!debate || !task || task.squad !== squad.id) refuse("unknown_debate", `không có tranh luận ${id} trong ${squad.id}`);
  return { debate, task };
}

function peerDebate(turn: Turn, id: string): { debate: Debate; task: Task } {
  const task = peerTask(turn);
  const debate = turn.ledger.debates[id];
  if (!debate || debate.task !== task.id) refuse("unknown_debate", `không có tranh luận ${id} của task ${task.id}`);
  return { debate, task };
}

/**
 * Applies a patch as a new brief version. A patch that changes nothing is refused; a patch that settles a
 * debate must change what it was about. Touching the squad's own acceptance needs the Supervisor.
 */
export function applyPatch(turn: Turn, task: Task, patch: PatchT, about?: { id: string; target: Target }): number {
  const target = about?.target;
  const squad = squadById(turn, task.squad);
  const current = brief(task);
  const next = {
    ...current,
    leadApproach: patch.leadApproach ?? current.leadApproach,
    acceptance: patch.acceptance ?? current.acceptance,
    scope: patch.scope ?? current.scope,
  };
  const changed = {
    leadApproach: hashOf(next.leadApproach) !== hashOf(current.leadApproach),
    acceptance: hashOf(next.acceptance) !== hashOf(current.acceptance),
    scope: hashOf(next.scope) !== hashOf(current.scope),
  };
  if (!changed.leadApproach && !changed.acceptance && !changed.scope) {
    refuse("no_change", `patch giữ nguyên brief ${task.id} phiên bản ${task.briefs.length}`, ["patch phải sửa leadApproach, acceptance hoặc scope"]);
  }
  if (changed.acceptance) {
    const dropped = current.acceptance.filter((a) => !next.acceptance.includes(a) && squad.acceptance.includes(a));
    if (dropped.length) {
      refuse("must_escalate", `patch bỏ acceptance của mục tiêu squad: ${dropped.join("; ")}`, ["mục tiêu squad chỉ Supervisor đổi được: respond{action:\"escalate\"}"]);
    }
  }
  if (target && !patchHits(target, current.leadApproach, next.leadApproach, changed)) {
    refuse("patch_misses_target", `patch không sửa ${targetName(target)}, chỗ Peer chỉ ra`, [
      "nhận Peer đúng thì sửa đúng chỗ đó",
      "Peer sai thì giữ ý kèm phản chứng: respond{action:\"hold\"}",
    ]);
  }
  task.briefs.push(next);
  if (changed.leadApproach) warnStale(turn, task, next.leadApproach, about?.id);
  return task.briefs.length;
}

type Target = ChallengeT["target"];

function targetName(target: Target): string {
  const names = { approach: "cách làm", step: "bước", current: "lựa chọn", acceptance: "acceptance", scope: "scope" };
  return `${names[target.kind]}${target.ref ? ` ${target.ref}` : ""}`;
}

/** A step or choice that is gone or reworded counts as changed; so does any approach change once the ref is gone. */
function patchHits(target: Target, before: LeadApproachT, after: LeadApproachT, changed: Record<"leadApproach" | "acceptance" | "scope", boolean>): boolean {
  if (target.kind === "acceptance") return changed.acceptance;
  if (target.kind === "scope") return changed.scope;
  if (target.kind === "approach" || !target.ref) return changed.leadApproach;
  const list = (a: LeadApproachT) => (target.kind === "step" ? a.steps : a.current);
  const old = list(before).find((s) => s.id === target.ref);
  if (!old) return changed.leadApproach;
  const now = list(after).find((s) => s.id === target.ref);
  return !now || now.text !== old.text;
}

/** Open debates pointing at a step or choice the new brief no longer has: the Peer decides whether they still stand. */
function warnStale(turn: Turn, task: Task, approach: LeadApproachT, settling?: string): void {
  for (const debate of openDebates(turn, task)) {
    if (debate.id === settling) continue;
    const { kind, ref } = debate.challenge.target;
    if ((kind !== "step" && kind !== "current") || !ref) continue;
    if ((kind === "step" ? approach.steps : approach.current).some((s) => s.id === ref)) continue;
    mail(
      turn,
      task.hc,
      `stale:${debate.id}`,
      `${debate.id} trỏ tới ${targetName(debate.challenge.target)}, không còn trong brief ${task.id} phiên bản ${task.briefs.length}.\n${briefDelta(task)}`,
      [`brief mới đã giải quyết thì withdraw{challenge:"${debate.id}"}`, "chưa thì giữ, Lead vẫn phải trả lời"],
    );
  }
}

export function amendTask(turn: Turn, args: ToolArgs<"amend_task">): Reply {
  const task = leadTask(turn, args.task);
  expectState(task, ["proposing", "debating", "running"], "amend_task");
  const open = openDebates(turn, task);
  if (open.length) {
    refuse("debate_open", `${task.id} còn tranh luận ${open.map((d) => d.id).join(", ")}`, ["sửa brief qua respond của tranh luận đó"]);
  }
  const before = brief(task);
  const version = applyPatch(turn, task, args.patch);
  (task.amends ??= []).push({ version, reason: args.reason, at: turn.ctx.now() });
  const b = brief(task);
  if (task.state === "proposing") {
    if (hashOf([before.acceptance, before.scope]) === hashOf([b.acceptance, b.scope])) {
      return { text: `Brief ${task.id} lên phiên bản ${version}. Cách làm vẫn niêm phong, Peer không nhận message.` };
    }
    mail(
      turn,
      task.hc,
      `amended:${task.id}`,
      `Lead sửa đề bài ${task.id}: ${args.reason}\nAcceptance:\n${b.acceptance.map((a) => `  - ${a}`).join("\n")}\nĐược sửa: ${b.scope.join(", ")}`,
      ["lập phương án theo đề bài mới", "propose"],
    );
    return { text: `Brief ${task.id} lên phiên bản ${version}. Peer đã nhận đề bài mới.` };
  }
  task.state = "debating";
  mail(
    turn,
    task.hc,
    `amended:${task.id}`,
    `Lead sửa brief ${task.id}: ${args.reason}\n${briefDelta(task)}`,
    ["so brief mới với đề xuất của bạn", "position: agree kèm comparison, hoặc challenges kèm bằng chứng"],
  );
  return { text: `Brief ${task.id} lên phiên bản ${version}. Peer phải position lại trước khi làm tiếp.` };
}

export function briefDelta(task: Task): string {
  const b = brief(task);
  return [
    `Brief ${task.id} phiên bản ${task.briefs.length}:`,
    `Cách làm: ${b.leadApproach.approach}`,
    `Bước:\n${b.leadApproach.steps.map((s) => `  ${s.id}. ${s.text}`).join("\n")}`,
    `Lựa chọn đang dùng:\n${b.leadApproach.current.map((s) => `  ${s.id}. ${s.text}`).join("\n") || "  (không có)"}`,
    `Acceptance:\n${b.acceptance.map((a) => `  - ${a}`).join("\n")}`,
    `Được sửa: ${b.scope.join(", ")}`,
  ].join("\n");
}

export function respond(turn: Turn, args: ToolArgs<"respond">): Reply {
  const { debate, task } = leadDebate(turn, args.challenge);
  if (CLOSED_DEBATE.has(debate.state)) refuse("closed", `${debate.id} đã kết thúc (${debate.state})`);
  if (debate.state === "escalated") refuse("escalated", `${debate.id} đang chờ Supervisor`);
  const peer = task.hc;
  const now = turn.ctx.now();
  switch (args.action) {
    case "concede": {
      const version = applyPatch(turn, task, args.patch, { id: debate.id, target: debate.challenge.target });
      debate.state = "conceded";
      debate.rounds.push({ by: "lead", action: "concede", evidence: [], text: `brief v${version}`, at: now });
      mail(turn, peer, `conceded:${debate.id}`, `Lead nhận sai ở ${debate.id}.\n${briefDelta(task)}`, ["làm theo brief mới"]);
      return { text: `Đã nhận sai ở ${debate.id}. Brief ${task.id} lên phiên bản ${version}.` };
    }
    case "hold": {
      if (debate.state === "deadlocked") refuse("no_more_holds", `${debate.id} đã bế tắc sau 2 lần giữ`, [`respond{challenge:"${debate.id}", action:"decide"}`]);
      if (debate.state === "held") refuse("peer_turn", `${debate.id} đang chờ Peer phản bác hoặc rút`);
      const fresh = freshEvidence(debate, args.counterEvidence, ["giữ ý cần phản chứng chưa ai nộp trong tranh luận này", "không có thì concede hoặc escalate"]);
      checkCounterEvidence(fresh);
      const evidence = storeEvidence(turn, fresh, task, turn.caller);
      debate.evidence.push(...evidence);
      debate.holds += 1;
      debate.rounds.push({ by: "lead", action: "hold", evidence: evidence.map((e) => e.id), text: args.reason, at: now });
      if (debate.holds >= 2) {
        debate.state = "deadlocked";
        mail(turn, peer, `held:${debate.id}`, `Lead giữ ý lần 2 ở ${debate.id}: ${args.reason}\nPhản chứng: ${evidence.map((e) => e.id).join(", ")}\nTranh luận bế tắc; Lead sẽ quyết.`, [`withdraw{challenge:"${debate.id}"}`, "làm tiếp phần không bị khóa"]);
        return { text: `${debate.id} bế tắc. Bước tiếp: respond{action:"decide", answers:"${latestPeerEvidence(debate)}"} hoặc escalate.` };
      }
      debate.state = "held";
      mail(turn, peer, `held:${debate.id}`, `Lead giữ ý ở ${debate.id}: ${args.reason}\nPhản chứng: ${evidence.map((e) => `${e.id} ${JSON.stringify(e.item)}`).join("\n")}`, [`rebut{challenge:"${debate.id}", newEvidence}`, `withdraw{challenge:"${debate.id}"}`]);
      return { text: `Đã giữ ý ở ${debate.id}. Chờ Peer.` };
    }
    case "decide": {
      if (debate.state !== "deadlocked") refuse("not_deadlocked", `${debate.id} chưa bế tắc (${debate.state})`, ["chỉ quyết sau 2 lần giữ ý"]);
      const latest = latestPeerEvidence(debate);
      if (args.answers !== latest) refuse("answer_latest_evidence", `answers phải là ${latest}, bằng chứng mới nhất của Peer`);
      if (args.side === "peer") {
        if (!args.patch) refuse("patch_required", "quyết theo Peer thì phải kèm patch");
        applyPatch(turn, task, args.patch, { id: debate.id, target: debate.challenge.target });
      }
      debate.state = "decided";
      debate.decision = { side: args.side, reason: args.reason };
      debate.rounds.push({ by: "lead", action: "decide", evidence: [], text: args.reason, at: now });
      mail(turn, peer, `decided:${debate.id}`, `Lead quyết ${debate.id} theo ${args.side === "lead" ? "Lead" : "Peer"}: ${args.reason}${args.side === "peer" ? `\n${briefDelta(task)}` : "\nBất đồng của bạn đã được ghi và sẽ vào handoff."}`, ["làm theo quyết định", "thấy không an toàn thì ask kèm bằng chứng mới"]);
      return { text: `Đã quyết ${debate.id}.` };
    }
    case "escalate": {
      const id = escalate(turn, squadById(turn, task.squad), `Tranh luận ${debate.id} của ${task.id}: ${debate.challenge.claim}. Lý do leo thang: ${args.why}`, { debate: debate.id });
      debate.state = "escalated";
      debate.ask = id;
      debate.rounds.push({ by: "lead", action: "escalate", evidence: [], text: args.why, at: now });
      mail(turn, peer, `escalated:${debate.id}`, `Lead chuyển ${debate.id} lên trên quyết.`, ["làm tiếp phần không bị khóa"]);
      return { text: `Đã leo thang ${debate.id} thành câu hỏi ${id}.` };
    }
  }
}

function latestPeerEvidence(debate: Debate): string {
  const mine = debate.evidence.filter((e) => debate.rounds.some((r) => r.by === "peer" && r.evidence.includes(e.id)));
  return mine[mine.length - 1]!.id;
}

export function rebut(turn: Turn, args: ToolArgs<"rebut">): Reply {
  const { debate, task } = peerDebate(turn, args.challenge);
  if (debate.state === "deadlocked") refuse("no_more_rounds", `${debate.id} đã hết vòng`, ["chờ Lead quyết", `withdraw{challenge:"${debate.id}"}`]);
  if (debate.state !== "held") refuse("lead_turn", `${debate.id} đang ở ${debate.state}, chưa tới lượt phản bác`);
  const fresh = freshEvidence(debate, args.newEvidence, ["đưa bằng chứng chưa nộp trong tranh luận này, hoặc withdraw"]);
  const evidence = storeEvidence(turn, fresh, task, task.hc);
  debate.evidence.push(...evidence);
  debate.state = "rebutted";
  debate.rounds.push({ by: "peer", action: "rebut", evidence: evidence.map((e) => e.id), text: "", at: turn.ctx.now() });
  const squad = squadById(turn, task.squad);
  mail(turn, squad.lead, `rebut:${debate.id}`, `Peer phản bác ở ${debate.id} với bằng chứng mới:\n${evidence.map((e) => `  ${e.id} ${JSON.stringify(e.item)}`).join("\n")}`, [`respond{challenge:"${debate.id}", action:"concede"|"hold"|"escalate"}`]);
  return { text: `Đã gửi phản bác ở ${debate.id}.` };
}

export function withdraw(turn: Turn, args: ToolArgs<"withdraw">): Reply {
  const { debate, task } = peerDebate(turn, args.challenge);
  if (CLOSED_DEBATE.has(debate.state) || debate.state === "escalated") refuse("closed", `${debate.id} đang ở ${debate.state}`);
  debate.state = "withdrawn";
  debate.rounds.push({ by: "peer", action: "withdraw", evidence: [], text: args.reason, at: turn.ctx.now() });
  mail(turn, squadById(turn, task.squad).lead, `withdrawn:${debate.id}`, `Peer rút ${debate.id}: ${args.reason}`, []);
  return { text: `Đã rút ${debate.id}.` };
}
