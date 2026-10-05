import type { Squad } from "../ledger/types.ts";
import type { Turn } from "./context.ts";

export interface DebateStats {
  agreed: number;
  challenged: number;
  conceded: number;
  held: number;
  decided: number;
  escalated: number;
  withdrawn: number;
  amended: number;
  approvedOverFailed: number;
}

export function debateStats(turn: Turn, squad: Squad): DebateStats {
  const tasks = Object.values(turn.ledger.tasks).filter((t) => t.squad === squad.id);
  const ids = new Set(tasks.map((t) => t.id));
  const debates = Object.values(turn.ledger.debates).filter((d) => ids.has(d.task));
  return {
    // Agreed: the Peer got past its position without opening a single debate.
    agreed: tasks.filter((t) => t.proposal && t.state !== "debating" && !debates.some((d) => d.task === t.id)).length,
    challenged: debates.length,
    conceded: debates.filter((d) => d.state === "conceded").length,
    held: debates.reduce((n, d) => n + d.holds, 0),
    decided: debates.filter((d) => d.state === "decided" && d.decision?.side !== "supervisor").length,
    escalated: debates.filter((d) => d.ask !== undefined).length,
    withdrawn: debates.filter((d) => d.state === "withdrawn").length,
    amended: tasks.reduce((n, t) => n + (t.amends?.length ?? 0), 0),
    approvedOverFailed: tasks.reduce((n, t) => n + t.verdicts.filter((v) => v.action === "approve" && v.overFailed).length, 0),
  };
}

export function statusText(turn: Turn, squads: Squad[]): string {
  if (!squads.length) return "Chưa có squad nào.";
  const { ledger } = turn;
  return squads
    .map((squad) => {
      const tasks = Object.values(ledger.tasks).filter((t) => t.squad === squad.id);
      const s = debateStats(turn, squad);
      const openDebates = Object.values(ledger.debates).filter((d) => tasks.some((t) => t.id === d.task) && !["conceded", "withdrawn", "decided"].includes(d.state));
      const hcs = new Set(Object.values(ledger.hcs).filter((x) => x.squad === squad.id).map((x) => x.id));
      const openAsks = Object.values(ledger.asks).filter((a) => a.state === "open" && (hcs.has(a.from) || hcs.has(a.to)));
      return [
        `${squad.id} [${squad.state}] ${squad.goal}  (nhánh ${squad.branch})`,
        ...tasks.map((t) => `  ${t.id} [${t.state}] ${t.title}${t.ci ? `  sha ${t.ci.sha.slice(0, 8)}${t.ci.failed ? " CI FAIL" : ""}` : ""}`),
        `  tranh luận mở: ${openDebates.map((d) => `${d.id}(${d.task},${d.state})`).join(", ") || "không"}`,
        `  câu hỏi mở: ${openAsks.map((a) => `${a.id} ${a.from}→${a.to}`).join(", ") || "không"}`,
        `  thống kê: Peer đồng ý ${s.agreed}, phản biện ${s.challenged}; Lead nhận sai ${s.conceded}, giữ ${s.held}, quyết ${s.decided}, leo thang ${s.escalated}; Peer rút ${s.withdrawn}; Lead tự sửa brief ${s.amended}; approve dù CI fail ${s.approvedOverFailed}`,
      ].join("\n");
    })
    .join("\n\n");
}
