import type { ToolArgs } from "../../shared/protocol.ts";
import { mail, refuse, type Reply, type Turn } from "./context.ts";
import { newId } from "./names.ts";
import { brief, leadSquad, hcName, hcOf, squadById } from "./lookup.ts";

export function ask(turn: Turn, args: ToolArgs<"ask">): Reply {
  const hc = hcOf(turn);
  const squad = hc.squad ? turn.ledger.squads[hc.squad] : undefined;
  if (!squad) refuse("no_squad", "HC này không thuộc squad nào");
  let to: string;
  if (hc.role === "peer") {
    if (!args.bestGuess?.trim()) refuse("best_guess_required", "Peer hỏi phải kèm bestGuess và làm tiếp theo nó trong lúc chờ");
    to = squad.lead;
  } else if (hc.role === "lead") {
    to = squad.supervisor;
  } else {
    refuse("no_superior", "Supervisor hỏi Human ngay trong chat");
  }
  const id = newId(turn.ledger, squad.id, "a");
  turn.ledger.asks[id] = { id, from: hc.id, to, question: args.question, bestGuess: args.bestGuess, state: "open" };
  mail(turn, to, `ask:${id}`, `${hcName(turn, hc.id)} hỏi: ${args.question}${args.bestGuess ? `\nPhỏng đoán đang làm theo: ${args.bestGuess}` : ""}`, [`answer{ask:"${id}", text}`]);
  return { text: `Đã gửi câu hỏi ${id}.${hc.role === "peer" ? " Làm tiếp theo bestGuess trong lúc chờ." : ""}` };
}

export function answer(turn: Turn, args: ToolArgs<"answer">): Reply {
  const hc = hcOf(turn);
  const question = turn.ledger.asks[args.ask];
  if (!question || question.to !== hc.id) refuse("unknown_ask", `không có câu hỏi ${args.ask} gửi cho HC này`);
  if (question.state === "dropped") refuse("asker_gone", `${question.id} không cần trả lời nữa: HC hỏi đã mất`);
  if (question.state !== "open") refuse("answered", `${question.id} đã được trả lời`);
  if (question.debate && !args.source) {
    refuse("source_required", "trả lời leo thang phải ghi source", ["human nếu Human đã quyết, squad-goal nếu suy từ mục tiêu squad"]);
  }
  question.state = "answered";
  question.answer = args.text;
  mail(turn, question.from, `answer:${question.id}`, `Trả lời ${question.id}: ${args.text}`, []);

  if (question.debate) {
    const debate = turn.ledger.debates[question.debate]!;
    const task = turn.ledger.tasks[debate.task]!;
    const current = brief(task);
    task.briefs.push({ ...current, required: [...current.required, { text: args.text, source: { kind: args.source!, ref: question.id } }] });
    debate.state = "decided";
    debate.decision = { side: "supervisor", reason: args.text };
    debate.rounds.push({ by: "lead", action: "answer", evidence: [], text: args.text, at: turn.ctx.now() });
    mail(turn, task.hc, `decided:${debate.id}`, `${debate.id} đã được quyết ở cấp trên: ${args.text}\nĐây giờ là ràng buộc bắt buộc của brief.`, ["làm theo"]);
  }
  if (question.concern) {
    const task = turn.ledger.tasks[question.concern.task];
    if (task) mail(turn, task.hc, `answer:${question.id}`, `Về lo ngại với ràng buộc ${question.concern.index}: ${args.text}`, []);
  }
  return { text: `Đã trả lời ${question.id}.` };
}

export function message(turn: Turn, args: ToolArgs<"message">): Reply {
  const hc = hcOf(turn);
  const target = turn.ledger.hcs[args.to];
  if (!target || target.state === "closed") refuse("unknown_hc", `không có HC ${args.to} đang hoạt động`);
  const squad = target.squad ? turn.ledger.squads[target.squad] : undefined;
  const from = hcName(turn, hc.id);
  if (hc.role === "supervisor") {
    if (!squad || squad.supervisor !== hc.id) refuse("not_your_tree", `${args.to} không thuộc squad của bạn`);
    if (target.role === "peer") {
      mail(turn, target.id, `message:${hc.id}`, `${from} hỏi: ${args.text}\n(Lead của bạn cũng nhận tin này. Chỉ đổi việc khi Lead giao.)`, []);
      mail(turn, squad.lead, `cc:${target.id}`, `${from} đã nhắn ${hcName(turn, target.id)}: ${args.text}`, []);
      return { text: `Đã gửi cho ${args.to}, kèm bản sao cho Lead ${squad.lead}.` };
    }
  } else if (hc.role === "lead") {
    const own = leadSquad(turn);
    const ok = target.id === own.supervisor || (target.role === "peer" && target.squad === own.id);
    if (!ok) refuse("not_your_tree", `Lead chỉ nhắn Supervisor và Peer của squad mình`);
  } else {
    refuse("not_allowed", "Peer không nhắn tin; dùng ask");
  }
  mail(turn, target.id, `message:${hc.id}`, `${from}: ${args.text}`, []);
  return { text: `Đã gửi cho ${args.to}.` };
}

/** A Peer's turn failed: the Lead hears, since the Peer will not go on by itself. */
export function turnEnded(turn: Turn, hcId: string, outcome: "completed" | "failed" | "canceled", lastText: string): void {
  const hc = turn.ledger.hcs[hcId];
  const task = hc?.task ? turn.ledger.tasks[hc.task] : undefined;
  if (outcome !== "failed" || !hc || hc.role !== "peer" || hc.state === "closed" || !task || task.hc !== hc.id) return;
  mail(turn, squadById(turn, task.squad).lead, `turn_failed:${task.id}`, `Lượt của Peer ${task.id} bị lỗi. Câu cuối: ${lastText.slice(0, 500)}`, [`message{to:"${hc.id}"}`]);
}
