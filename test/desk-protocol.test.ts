import { test } from "node:test";
import assert from "node:assert/strict";
import { checkedTask, handoffArgs, mailsTo, makeBrief, must, planTask, PROPOSAL, runningTask, world } from "./helpers.ts";
import { outOfScope } from "../server/desk/work.ts";

test("plan: required không có nguồn thật bị từ chối và gợi ý chuyển sang current", () => {
  const w = world();
  const result = w.call(w.lead, "plan", {
    briefs: [makeBrief({ required: [{ text: "dùng float", source: { kind: "human", ref: "C9" } }] })],
  });
  assert.equal(result.ok, false);
  assert.equal(result.code, "required_without_source");
  assert.ok(result.fix?.some((f) => f.includes("leadApproach.current")));
});

test("plan: required trỏ đúng ràng buộc Human, mục tiêu squad và luật dự án thì qua", () => {
  const w = world();
  const t = planTask(
    w,
    makeBrief({
      required: [
        { text: "dùng VND", source: { kind: "human", ref: "C1" } },
        { text: "test pass", source: { kind: "squad-goal", ref: "acceptance:0" } },
        { text: "giữ API", source: { kind: "project-rule", ref: "R1" } },
      ],
    }),
  );
  assert.equal(w.store.ledger.tasks[t.task]!.state, "proposing");
});

test("plan: thiếu trường thì từ chối kèm danh sách trường sai", () => {
  const w = world();
  const { checks: _, ...noChecks } = makeBrief();
  const result = w.call(w.lead, "plan", { briefs: [noChecks] });
  assert.equal(result.code, "bad_args");
  assert.ok(result.fix?.some((f) => f.startsWith("briefs.0.checks")));
});

test("plan: đề bài gửi Peer không lộ cách làm của Lead", () => {
  const w = world();
  const t = planTask(w);
  const kickoff = w.effects.find((e) => e.kind === "create_hc" && e.hc === t.peer);
  assert.ok(kickoff && kickoff.kind === "create_hc");
  assert.ok(kickoff.kickoff.includes("discount-code"));
  assert.ok(!kickoff.kickoff.includes("applyDiscount"));
  assert.ok(!kickoff.kickoff.includes("lưu phần trăm dạng float"));
});

test("handoff trước propose bị từ chối", () => {
  const w = world();
  const t = planTask(w);
  const result = w.call(t.peer, "handoff", handoffArgs("h1"));
  assert.equal(result.code, "debate_first");
});

test("handoff khi worktree còn thay đổi chưa commit bị từ chối", () => {
  const w = world();
  const t = runningTask(w);
  w.repo.heads.set(t.worktree, "h1");
  w.repo.ancestry.add("base0>h1");
  w.repo.dirtySet.add(t.worktree);
  assert.equal(w.call(t.peer, "handoff", handoffArgs("h1")).code, "dirty");
});

test("handoff với sha không phải HEAD bị từ chối", () => {
  const w = world();
  const t = runningTask(w);
  w.repo.heads.set(t.worktree, "h2");
  assert.equal(w.call(t.peer, "handoff", handoffArgs("h1")).code, "not_head");
});

test("handoff ghi cảnh báo file ngoài scope và chuyển sang ci_running", () => {
  const w = world();
  const t = runningTask(w, makeBrief({ scope: ["src/cart.ts"] }));
  w.repo.heads.set(t.worktree, "h1");
  w.repo.ancestry.add("base0>h1");
  w.repo.changed = ["src/cart.ts", "package.json"];
  must(w.call(t.peer, "handoff", handoffArgs("h1")));
  const task = w.store.ledger.tasks[t.task]!;
  assert.equal(task.state, "ci_running");
  assert.deepEqual(task.handoff!.outOfScope, ["package.json"]);
  assert.ok(w.effects.some((e) => e.kind === "run_ci" && e.task === t.task));
});

test("CI: Peer khai exit 0 nhưng lệnh thật fail thì message gửi Lead chỉ ra chỗ lệch, Peer cũng nhận message", () => {
  const w = world();
  const t = checkedTask(w, { claimed: 0, actual: 1 });
  const toLead = mailsTo(w, w.lead).at(-1)!;
  assert.match(toLead.text, /FAIL npm test\s+thật exit 1, Peer khai exit 0\s+← LỆCH/);
  assert.ok(mailsTo(w, t.peer).some((m) => m.topic === `ci_failed:${t.task}`));
  assert.equal(w.store.ledger.tasks[t.task]!.ci!.failed, true);
});

test("verdict approve trước khi CI xong bị từ chối", () => {
  const w = world();
  const t = runningTask(w);
  w.repo.heads.set(t.worktree, "h1");
  w.repo.ancestry.add("base0>h1");
  must(w.call(t.peer, "handoff", handoffArgs("h1")));
  const result = w.call(w.lead, "verdict", { action: "approve", task: t.task, sha: "h1", read: ["src/cart.ts"] });
  assert.equal(result.code, "ci_not_done");
});

test("verdict sai SHA bị từ chối", () => {
  const w = world();
  const t = checkedTask(w);
  assert.equal(w.call(w.lead, "verdict", { action: "approve", task: t.task, sha: "zzz", read: ["a"] }).code, "wrong_sha");
});

test("approve phải liệt kê file đã đọc", () => {
  const w = world();
  const t = checkedTask(w);
  assert.equal(w.call(w.lead, "verdict", { action: "approve", task: t.task, sha: "h1", read: [] }).code, "read_required");
});

test("approve khi CI fail mà thiếu overFailed bị từ chối; có lý do thì qua và vào thống kê", () => {
  const w = world();
  const t = checkedTask(w, { claimed: 1, actual: 1 });
  w.repo.ancestry.add("base0>h1");
  assert.equal(w.call(w.lead, "verdict", { action: "approve", task: t.task, sha: "h1", read: ["src/cart.ts"] }).code, "over_failed_required");
  must(w.call(w.lead, "verdict", { action: "approve", task: t.task, sha: "h1", read: ["src/cart.ts"], overFailed: "test flaky đã biết" }));
  const status = w.call(w.sup, "status", {});
  assert.match(status.text, /approve dù CI fail 1/);
});

test("HEAD đổi sau handoff: kết quả CI bị hủy, verdict bị từ chối, Peer được báo", () => {
  const w = world();
  const t = checkedTask(w);
  w.repo.heads.set(t.worktree, "h9");
  const result = w.call(w.lead, "verdict", { action: "approve", task: t.task, sha: "h1", read: ["src/cart.ts"] });
  assert.equal(result.ok, false);
  assert.equal(result.code, "head_moved");
  const task = w.store.ledger.tasks[t.task]!;
  assert.equal(task.state, "running");
  assert.equal(task.ci, undefined);
  assert.ok(mailsTo(w, t.peer).some((m) => m.topic === `head_moved:${t.task}`));
});

test("approve fast-forward nhánh squad; merged thì archive Peer và dọn worktree", () => {
  const w = world();
  const t = checkedTask(w);
  must(w.call(w.lead, "verdict", { action: "approve", task: t.task, sha: "h1", read: ["src/cart.ts"] }));
  assert.ok(w.effects.some((e) => e.kind === "fast_forward" && e.sha === "h1" && e.branch === "squad/s1-discount"));
  const effects = w.desk.event({ kind: "merged", task: t.task });
  assert.equal(w.store.ledger.tasks[t.task]!.state, "merged");
  assert.ok(effects.some((e) => e.kind === "archive_hc" && e.hc === t.peer));
  assert.ok(effects.some((e) => e.kind === "remove_worktree" && e.path === t.worktree));
});

test("nhánh squad đã đi tiếp lúc approve: task tự về ci_running để gộp và chạy lại CI", () => {
  const w = world();
  const t = checkedTask(w);
  w.repo.refs.set("squad/s1-discount", "other-merge");
  const result = must(w.call(w.lead, "verdict", { action: "approve", task: t.task, sha: "h1", read: ["src/cart.ts"] }));
  assert.match(result.text, /quay lại ci_running/);
  assert.equal(w.store.ledger.tasks[t.task]!.state, "ci_running");
  assert.equal(w.effects.filter((e) => e.kind === "run_ci" && e.task === t.task).length, 2);
});

test("conflict khi gộp nhánh squad: task về running, Peer nhận danh sách file", () => {
  const w = world();
  const t = runningTask(w);
  w.repo.heads.set(t.worktree, "h1");
  w.repo.ancestry.add("base0>h1");
  must(w.call(t.peer, "handoff", handoffArgs("h1")));
  const effects = w.desk.event({ kind: "conflict", task: t.task, files: ["src/cart.ts"] });
  assert.equal(w.store.ledger.tasks[t.task]!.state, "running");
  assert.ok(effects.some((e) => e.kind === "mail" && e.to === t.peer && e.text.includes("src/cart.ts")));
});

test("request_changes đưa task về running cùng HC", () => {
  const w = world();
  const t = checkedTask(w);
  must(w.call(w.lead, "verdict", { action: "request_changes", task: t.task, sha: "h1", findings: [{ path: "src/cart.ts", line: 3, problem: "làm tròn sai" }] }));
  const task = w.store.ledger.tasks[t.task]!;
  assert.equal(task.state, "running");
  assert.equal(task.hc, t.peer);
});

test("report chạy lại checks trên nhánh squad rồi mới báo Supervisor", () => {
  const w = world();
  must(w.call(w.lead, "report", { summary: "xong", approved: ["T2"], open: [], risks: [] }));
  assert.ok(w.effects.some((e) => e.kind === "ci_squad"));
  assert.equal(mailsTo(w, w.sup).length, 0);
  const effects = w.desk.event({ kind: "squad_ci_done", squad: w.squad, results: [{ task: "T2", command: "npm test", exit: 0, output: "ok" }] });
  assert.equal(w.store.ledger.squads[w.squad]!.state, "ready");
  assert.ok(effects.some((e) => e.kind === "mail" && e.to === w.sup && e.text.includes("tất cả ok")));
});

test("Peer ask phải kèm bestGuess; Lead trả lời thì Peer nhận message", () => {
  const w = world();
  const t = runningTask(w);
  assert.equal(w.call(t.peer, "ask", { question: "làm tròn lên hay xuống?" }).code, "best_guess_required");
  must(w.call(t.peer, "ask", { question: "làm tròn lên hay xuống?", bestGuess: "xuống" }));
  const ask = Object.values(w.store.ledger.asks).at(-1)!;
  must(w.call(w.lead, "answer", { ask: ask.id, text: "xuống" }));
  assert.ok(mailsTo(w, t.peer).some((m) => m.topic === `answer:${ask.id}`));
});

test("Lead đọc diff của task", () => {
  const w = world();
  const t = checkedTask(w);
  assert.equal(must(w.call(w.lead, "diff", { task: t.task })).text, "diff base0..h1");
});

test("propose không dùng được với task không có vòng đề xuất", () => {
  const w = world();
  const t = checkedTask(w);
  assert.equal(w.call(t.peer, "propose", PROPOSAL).code, "bad_state");
});

test("khôi phục: task đang chạy CI, đã approve, squad đang report đều được làm lại sau restart", () => {
  const w = world();
  const a = runningTask(w);
  w.repo.heads.set(a.worktree, "h1");
  w.repo.ancestry.add("base0>h1");
  must(w.call(a.peer, "handoff", handoffArgs("h1")));
  assert.ok(w.desk.recover().some((e) => e.kind === "run_ci" && e.task === a.task && e.sha === "h1"));
  w.desk.event({ kind: "ci_done", task: a.task, sha: "h1", results: [{ command: "npm test", exit: 0, output: "ok" }] });
  must(w.call(w.lead, "verdict", { action: "approve", task: a.task, sha: "h1", read: ["src/cart.ts"] }));
  assert.ok(w.desk.recover().some((e) => e.kind === "fast_forward" && e.task === a.task && e.sha === "h1"));
  w.desk.event({ kind: "merged", task: a.task });
  must(w.call(w.lead, "report", { summary: "xong", approved: [a.task], open: [], risks: [] }));
  const effects = w.desk.recover();
  assert.ok(effects.some((e) => e.kind === "ci_squad" && e.squad === w.squad));
  assert.ok(!effects.some((e) => e.kind === "run_ci" || e.kind === "fast_forward"));
});

test("mã theo cấp: s1, s1-t1, s1-t1-d1, s1-a1; trong squad được viết tắt, Supervisor viết đủ hoặc gọi squad bằng tên", () => {
  const w = world();
  assert.equal(w.squad, "s1");
  assert.equal(w.lead, "s1-lead");
  const t = planTask(w);
  assert.equal(t.task, "s1-t1");
  assert.equal(w.store.ledger.tasks[t.task]!.branch, "task/s1-t1-discount-code");
  must(w.call(t.peer, "ask", { question: "VND có số lẻ không?", bestGuess: "không" }));
  assert.ok(w.store.ledger.asks["s1-a1"]);
  must(w.call(w.lead, "answer", { ask: "A1", text: "không có" }));
  assert.equal(w.call(w.lead, "diff", { task: "T1" }).ok, true);
  assert.match(w.call(w.sup, "status", { squad: "s1-discount" }).text, /s1-t1/);
  assert.match(w.call(w.sup, "status", { squad: "discount" }).text, /s1-t1/);
});

test("tên task và slug là tên ngắn: chữ thường nối bằng dấu -, chữ hoa và khoảng trắng được đổi giúp", () => {
  const w = world();
  assert.equal(w.call(w.lead, "plan", { briefs: [makeBrief({ title: "src/cart.ts: thêm giảm giá" })] }).code, "bad_args");
  must(w.call(w.lead, "plan", { briefs: [makeBrief({ title: "Stock Immutable" })] }));
  assert.equal(Object.values(w.store.ledger.tasks).at(-1)!.title, "stock-immutable");
  assert.equal(w.call(w.sup, "open_squad", { goal: "g", acceptance: ["a"] }).code, "bad_args");
});

test("tên ngắn không được giống id hay tên HC: slug t2, lead, s3-x và title d1 bị từ chối", () => {
  const w = world();
  for (const slug of ["t2", "lead", "s3-checkout", "supervisor"]) {
    assert.equal(w.call(w.sup, "open_squad", { goal: "g", acceptance: ["a"], slug }).code, "bad_args", slug);
  }
  for (const title of ["d1", "e2-format", "peer"]) {
    assert.equal(w.call(w.lead, "plan", { briefs: [makeBrief({ title })] }).code, "bad_args", title);
  }
  must(w.call(w.lead, "plan", { briefs: [makeBrief({ title: "api-v2" })] }));
});

test("số squad bỏ qua số đã có nhánh trong repo; nhánh task đã có thì plan bị từ chối", () => {
  const w = world();
  w.repo.refs.set("squad/s2-other", "x");
  w.repo.refs.set("task/s3-t1-other", "x");
  must(w.call(w.sup, "open_squad", { goal: "g", acceptance: ["a"], slug: "checkout" }));
  assert.equal(w.store.ledger.squads["s4"]?.name, "s4-checkout");
  assert.ok(!w.store.ledger.squads["s2"] && !w.store.ledger.squads["s3"]);
  w.repo.refs.set("task/s1-t1-discount-code", "x");
  assert.equal(w.call(w.lead, "plan", { briefs: [makeBrief()] }).code, "branch_taken");
});

test("file ngoài scope: * trong một thư mục, ** qua thư mục, đường dẫn kiểu Windows vẫn khớp", () => {
  assert.deepEqual(outOfScope(["src/a.ts", "src/cart/b.ts", "test\\a.ts", "README.md"], ["src/*.ts", "test/**"]), ["src/cart/b.ts", "README.md"]);
});
