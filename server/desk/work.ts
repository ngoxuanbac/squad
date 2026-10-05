import type { ToolArgs } from "../../shared/protocol.ts";
import type { CiResult, Task } from "../ledger/types.ts";
import { mail, refuse, type Reply, type Turn } from "./context.ts";
import { wakeWaiting } from "./squads.ts";
import { brief, debatesOf, expectState, leadTask, openDebates, peerTask, squadById, worktreeOf } from "./lookup.ts";

const OUTPUT_LIMIT = 4096;

const norm = (path: string) => path.replaceAll("\\", "/").replace(/^\.\//, "");

/** Repo-relative path globs of `scope`: `*` stays within a folder, `**` crosses folders. */
function globToRegExp(glob: string): RegExp {
  const pattern = norm(glob)
    .replace(/[.+^${}()|[\]]/g, "\\$&")
    .replace(/\*\*\/?/g, "\u0000")
    .replace(/\*/g, "[^/]*")
    .replace(/\?/g, "[^/]")
    .replaceAll("\u0000", ".*");
  return new RegExp(`^${pattern}$`);
}

/** Changed files no `scope` glob names; handoff warns about them, it does not refuse. */
export function outOfScope(files: string[], scope: string[]): string[] {
  const patterns = scope.map(globToRegExp);
  return files.filter((file) => !patterns.some((p) => p.test(norm(file))));
}

export function handoff(turn: Turn, args: ToolArgs<"handoff">): Reply {
  const task = peerTask(turn);
  const { repo } = turn.ctx;
  if (task.state === "proposing" || task.state === "debating") {
    refuse("debate_first", `task ${task.id} chưa qua vòng đề xuất`, [task.state === "proposing" ? "propose trước" : "position trước"]);
  }
  expectState(task, ["running"], "handoff");
  const open = openDebates(turn, task);
  if (open.length) refuse("open_debate", `còn tranh luận chưa kết thúc: ${open.map((d) => `${d.id}(${d.state})`).join(", ")}`, ["chờ Lead trả lời, hoặc withdraw"]);
  if (repo.dirty(task.worktree)) refuse("dirty", "worktree còn thay đổi chưa commit", ["commit rồi handoff lại"]);
  const head = repo.head(task.worktree);
  if (head !== args.sha && !head.startsWith(args.sha)) refuse("not_head", `sha ${args.sha} không phải HEAD (${head.slice(0, 12)})`, ["gửi SHA của commit cuối trên nhánh task"]);
  if (!repo.isAncestor(task.base, head)) refuse("bad_base", `base ${task.base.slice(0, 12)} không phải tổ tiên của ${head.slice(0, 12)}`);
  const b = brief(task);
  const files = repo.changedFiles(task.base, head);
  const unknowns = [...args.unknowns];
  for (const debate of debatesOf(turn, task)) {
    if (debate.state === "decided" && debate.decision?.side === "lead") {
      const line = `Bất đồng còn mở ${debate.id}: ${debate.challenge.claim}`;
      if (!unknowns.includes(line)) unknowns.push(line);
    }
  }
  task.handoff = {
    sha: head,
    outcome: args.outcome,
    summary: args.summary,
    claimed: args.checks.map((v) => ({ ...v, output: v.output.slice(-OUTPUT_LIMIT) })),
    skipped: args.skipped,
    unknowns,
    discovered: args.discovered ?? [],
    outOfScope: outOfScope(files, b.scope),
  };
  task.ci = undefined;
  task.state = "ci_running";
  turn.effects.push({ kind: "run_ci", task: task.id, sha: head });
  return { text: "Đã nhận, CI đang chạy. Không sửa worktree tới khi Lead ra verdict." };
}

/** Runtime could not merge the squad branch into the task branch. */
export function conflicted(turn: Turn, taskId: string, files: string[]): void {
  const task = turn.ledger.tasks[taskId];
  if (!task || task.state !== "ci_running") return;
  task.state = "running";
  const squad = squadById(turn, task.squad);
  mail(turn, task.hc, `conflict:${task.id}`, `Nhánh squad đã có thay đổi đụng vào việc của bạn. Plugin đã bắt đầu merge ${squad.branch} vào nhánh task và để lại dấu conflict ở:\n${files.map((f) => `  ${f}`).join("\n")}\nGỡ conflict, commit, rồi handoff lại. Đừng tự chạy git merge.`, ["gỡ conflict", "commit", "handoff"]);
  mail(turn, squad.lead, `conflict:${task.id}`, `${task.id} có conflict với ${squad.branch} ở ${files.length} file; Peer đang gỡ.`, []);
}

/** Runtime merged the squad branch in (or found nothing to merge) and reran checks at `sha`. */
export function ciDone(turn: Turn, taskId: string, sha: string, actual: { command: string; exit: number; output: string }[]): void {
  const task = turn.ledger.tasks[taskId];
  if (!task || task.state !== "ci_running" || !task.handoff) return;
  const results: CiResult[] = actual.map((run) => {
    const claimed = task.handoff!.claimed.find((c) => c.command === run.command);
    return {
      command: run.command,
      claimed: claimed ? { exit: claimed.exit, output: claimed.output } : undefined,
      actual: { exit: run.exit, output: run.output.slice(-OUTPUT_LIMIT) },
      mismatch: !claimed || claimed.exit !== run.exit,
    };
  });
  const failed = results.some((r) => r.actual.exit !== 0);
  task.ci = { sha, results, failed };
  task.state = "ci_done";
  const squad = squadById(turn, task.squad);
  const h = task.handoff;
  const lines = results.map((r) => `  ${r.actual.exit === 0 ? "ok  " : "FAIL"} ${r.command}  thật exit ${r.actual.exit}${r.claimed ? `, Peer khai exit ${r.claimed.exit}` : ", Peer không khai"}${r.mismatch ? "  ← LỆCH" : ""}`);
  mail(
    turn,
    squad.lead,
    `done:${task.id}`,
    [
      `${task.id} đã handoff (${h.outcome}) ở ${sha}${sha !== h.sha ? ` (đã gộp ${squad.branch} vào ${h.sha.slice(0, 8)})` : ""}.`,
      h.summary,
      `CI chạy lại checks:\n${lines.join("\n")}`,
      h.outOfScope.length ? `Ngoài phạm vi scope: ${h.outOfScope.join(", ")}` : "",
      h.skipped.length ? `Cố tình không kiểm: ${h.skipped.join("; ")}` : "",
      h.unknowns.length ? `Chưa biết, bất đồng:\n${h.unknowns.map((u) => `  - ${u}`).join("\n")}` : "",
      h.discovered.length ? `Phát hiện ngoài phạm vi: ${h.discovered.join("; ")}` : "",
      debateLine(turn, task),
    ]
      .filter(Boolean)
      .join("\n\n"),
    [`diff{task:"${task.id}"}`, `verdict{task:"${task.id}", sha:"${sha}"}`],
  );
  if (failed) {
    mail(turn, task.hc, `ci_failed:${task.id}`, `CI chạy lại checks ở ${sha} và có lệnh fail:\n${lines.filter((l) => l.includes("FAIL")).join("\n")}`, ["chờ verdict của Lead"]);
  }
}

export function debateLine(turn: Turn, task: Task): string {
  const debates = debatesOf(turn, task);
  if (!debates.length) return "Tranh luận: 0";
  const count = (state: string) => debates.filter((d) => d.state === state).length;
  return `Tranh luận: ${debates.length} · Lead nhận sai ${count("conceded")} · Lead giữ ${debates.reduce((n, d) => n + d.holds, 0)} · Lead quyết sau 2 vòng ${debates.filter((d) => d.state === "decided" && d.decision?.side !== "supervisor").length} · Peer rút ${count("withdrawn")}`;
}

export function verdict(turn: Turn, args: ToolArgs<"verdict">): Reply {
  const task = leadTask(turn, args.task);
  const { repo } = turn.ctx;
  if (task.state !== "ci_done") refuse("ci_not_done", `${task.id} đang ở ${task.state}, CI chưa xong`, ["chờ message done"]);
  const ci = task.ci!;
  if (args.sha !== ci.sha && !ci.sha.startsWith(args.sha)) refuse("wrong_sha", `sha phải là ${ci.sha}, SHA đã qua CI`);
  const head = repo.head(task.worktree);
  if (head !== ci.sha) {
    task.state = "running";
    task.ci = undefined;
    mail(turn, task.hc, `head_moved:${task.id}`, `HEAD đã đổi sau handoff (${head.slice(0, 8)} ≠ ${ci.sha.slice(0, 8)}). Kết quả CI bị hủy; handoff lại.`, ["handoff"]);
    return {
      text: `HEAD của ${task.id} đã đổi sau CI; kết quả CI bị hủy`,
      error: { code: "head_moved", fix: ["chờ handoff mới"] },
    };
  }
  const at = turn.ctx.now();
  if (args.action === "request_changes") {
    task.verdicts.push({ action: "request_changes", sha: ci.sha, at });
    task.state = "running";
    task.ci = undefined;
    mail(turn, task.hc, `changes_requested:${task.id}`, `Lead request changes ở ${ci.sha.slice(0, 8)}:\n${args.findings.map((f) => `  ${f.path}${f.line ? `:${f.line}` : ""} — ${f.problem}`).join("\n")}`, ["sửa", "commit", "handoff"]);
    return { text: `Đã request changes ${task.id}; task về running.` };
  }
  if (!args.read.length) refuse("read_required", "approve phải liệt kê file đã đọc trong read", [`diff{task:"${task.id}"}`]);
  if (ci.failed && !args.overFailed?.trim()) refuse("over_failed_required", "có lệnh checks fail; phải ghi lý do vào overFailed");
  const squad = squadById(turn, task.squad);
  const squadHead = repo.revParse(squad.branch);
  if (!squadHead || !repo.isAncestor(squadHead, ci.sha)) {
    task.state = "ci_running";
    task.ci = undefined;
    turn.effects.push({ kind: "run_ci", task: task.id, sha: ci.sha });
    return { text: `${squad.branch} đã đi tiếp từ lúc chạy CI. ${task.id} quay lại ci_running: gộp lại và chạy lại CI, message done mới sẽ tới.` };
  }
  task.verdicts.push({ action: "approve", sha: ci.sha, at, overFailed: args.overFailed });
  task.state = "approved";
  turn.effects.push({ kind: "fast_forward", task: task.id, branch: squad.branch, sha: ci.sha });
  return { text: `Đã approve ${task.id} ở ${ci.sha.slice(0, 8)}. ${debateLine(turn, task)}` };
}

/** Runtime moved the squad branch to the approved SHA. */
export function merged(turn: Turn, taskId: string): void {
  const task = turn.ledger.tasks[taskId];
  if (!task || task.state !== "approved") return;
  task.state = "merged";
  const hc = turn.ledger.hcs[task.hc];
  mail(turn, task.hc, `merged:${task.id}`, `${task.id} đã vào nhánh squad. Cảm ơn; HC này sẽ đóng.`, []);
  if (hc && hc.state !== "closed") {
    hc.state = "closed";
    turn.effects.push({ kind: "archive_hc", hc: hc.id });
  }
  turn.effects.push({ kind: "remove_worktree", path: task.worktree });
  const squad = turn.ledger.squads[task.squad];
  const lead = squad && turn.ledger.hcs[squad.lead];
  if (lead) turn.effects.push(worktreeOf(turn.ledger, lead)!);
  wakeWaiting(turn);
}

export function diff(turn: Turn, args: ToolArgs<"diff">): Reply {
  const task = leadTask(turn, args.task);
  const sha = task.ci?.sha ?? task.handoff?.sha ?? turn.ctx.repo.head(task.worktree);
  return { text: turn.ctx.repo.diff(task.base, sha, args.path) };
}
