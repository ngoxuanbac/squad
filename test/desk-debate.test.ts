import { test } from "node:test";
import assert from "node:assert/strict";
import { checkedTask, CMD_EVIDENCE, FILE_EVIDENCE, handoffArgs, mailsTo, makeBrief, must, planTask, PROPOSAL, runningTask, world, type Planned, type World } from "./helpers.ts";

const CHALLENGE = {
  target: { kind: "current", ref: "c1" },
  claim: "lưu phần trăm dạng float làm sai tiền",
  evidence: [CMD_EVIDENCE],
  impact: "đơn 10.000đ giảm 15% ra số lẻ",
  proposal: "dùng số nguyên",
  affects: ["s1"],
};

const LEAD_EVIDENCE = { kind: "file" as const, path: "docs/rules.md", lines: [3, 4] as [number, number], quote: "giá luôn chia hết cho 100" };

/** Plans, proposes and opens one debate. Returns the task and the debate id. */
function debating(w: World): Planned & { debate: string } {
  const t = planTask(w);
  must(w.call(t.peer, "propose", PROPOSAL));
  must(w.call(t.peer, "position", { agree: false, challenges: [CHALLENGE] }));
  const debate = Object.values(w.store.ledger.debates).at(-1)!.id;
  return { ...t, debate };
}

function deadlocked(w: World) {
  const t = debating(w);
  must(w.call(w.lead, "respond", { action: "hold", challenge: t.debate, counterEvidence: [LEAD_EVIDENCE], reason: "giá luôn tròn trăm" }));
  must(w.call(t.peer, "rebut", { challenge: t.debate, newEvidence: [FILE_EVIDENCE] }));
  must(w.call(w.lead, "respond", { action: "hold", challenge: t.debate, counterEvidence: [{ ...LEAD_EVIDENCE, lines: [5, 6] }], reason: "vẫn tròn trăm" }));
  return t;
}

test("propose trả về cách làm của Lead cạnh đề xuất của Peer", () => {
  const w = world();
  const t = planTask(w);
  const result = must(w.call(t.peer, "propose", PROPOSAL));
  assert.ok(result.text.includes("applyDiscount"));
  assert.ok(result.text.includes(PROPOSAL.approach));
  assert.ok(mailsTo(w, w.lead).some((m) => m.topic === `proposal:${t.task}`));
});

test("propose lần hai bị từ chối: đề xuất đã khóa", () => {
  const w = world();
  const t = planTask(w);
  must(w.call(t.peer, "propose", PROPOSAL));
  assert.equal(w.call(t.peer, "propose", { ...PROPOSAL, approach: "đổi ý sau khi xem đáp án" }).code, "bad_state");
});

test("đồng ý mà không có comparison bị từ chối", () => {
  const w = world();
  const t = planTask(w);
  must(w.call(t.peer, "propose", PROPOSAL));
  assert.equal(w.call(t.peer, "position", { agree: true, comparison: [] }).code, "bad_args");
  assert.equal(w.call(t.peer, "position", { agree: true, comparison: [{ point: "x", mine: "", lead: "y", whyLeadIsFine: "z" }] }).code, "bad_args");
});

test("phản biện thiếu impact hoặc thiếu bằng chứng bị từ chối", () => {
  const w = world();
  const t = planTask(w);
  must(w.call(t.peer, "propose", PROPOSAL));
  assert.equal(w.call(t.peer, "position", { agree: false, challenges: [{ ...CHALLENGE, impact: " " }] }).code, "bad_args");
  assert.equal(w.call(t.peer, "position", { agree: false, challenges: [{ ...CHALLENGE, evidence: [] }] }).code, "bad_args");
});

test("phản biện trỏ tới lựa chọn không có hoặc file không có thì bị từ chối", () => {
  const w = world();
  const t = planTask(w);
  must(w.call(t.peer, "propose", PROPOSAL));
  assert.equal(w.call(t.peer, "position", { agree: false, challenges: [{ ...CHALLENGE, target: { kind: "current", ref: "c9" } }] }).code, "bad_target");
  assert.equal(w.call(t.peer, "position", { agree: false, challenges: [{ ...CHALLENGE, evidence: [{ kind: "file", path: "src/nope.ts" }] }] }).code, "bad_evidence");
  assert.equal(w.call(t.peer, "position", { agree: false, challenges: [{ ...CHALLENGE, evidence: [{ kind: "file", path: "src/price.ts", lines: [30, 90] }] }] }).code, "bad_evidence");
});

test("giữ ý mà phản chứng chỉ trỏ lại brief bị từ chối", () => {
  const w = world();
  const t = debating(w);
  const result = w.call(w.lead, "respond", { action: "hold", challenge: t.debate, counterEvidence: [{ kind: "doc", ref: "brief T2", quote: "lưu dạng float" }], reason: "brief đã chốt" });
  assert.equal(result.code, "brief_is_not_evidence");
});

test("phản bác lặp lại bằng chứng cũ bị từ chối", () => {
  const w = world();
  const t = debating(w);
  must(w.call(w.lead, "respond", { action: "hold", challenge: t.debate, counterEvidence: [LEAD_EVIDENCE], reason: "giá luôn tròn trăm" }));
  const result = w.call(t.peer, "rebut", { challenge: t.debate, newEvidence: [CMD_EVIDENCE] });
  assert.equal(result.code, "no_new_evidence");
});

test("giữ ý lần ba bị từ chối; quyết trước khi bế tắc bị từ chối", () => {
  const w = world();
  const t = debating(w);
  assert.equal(w.call(w.lead, "respond", { action: "decide", challenge: t.debate, side: "lead", reason: "tôi quyết", answers: "E1" }).code, "not_deadlocked");
  must(w.call(w.lead, "respond", { action: "hold", challenge: t.debate, counterEvidence: [LEAD_EVIDENCE], reason: "giá luôn tròn trăm" }));
  must(w.call(t.peer, "rebut", { challenge: t.debate, newEvidence: [FILE_EVIDENCE] }));
  must(w.call(w.lead, "respond", { action: "hold", challenge: t.debate, counterEvidence: [{ ...LEAD_EVIDENCE, lines: [5, 6] }], reason: "vẫn tròn trăm" }));
  assert.equal(w.store.ledger.debates[t.debate]!.state, "deadlocked");
  assert.equal(w.call(w.lead, "respond", { action: "hold", challenge: t.debate, counterEvidence: [{ ...LEAD_EVIDENCE, lines: [7, 8] }], reason: "lần ba" }).code, "no_more_holds");
  assert.equal(w.call(t.peer, "rebut", { challenge: t.debate, newEvidence: [{ ...CMD_EVIDENCE, output: "khác" }] }).code, "no_more_rounds");
});

test("handoff bị chặn khi còn tranh luận mở", () => {
  const w = world();
  const t = debating(w);
  w.repo.heads.set(t.worktree, "h1");
  w.repo.ancestry.add("base0>h1");
  assert.equal(w.call(t.peer, "handoff", handoffArgs("h1")).code, "open_debate");
});

test("Lead nhận sai: brief lên phiên bản mới, Peer nhận brief mới, handoff mở lại", () => {
  const w = world();
  const t = debating(w);
  const newApproach = { ...makeBrief().leadApproach, current: [{ id: "c1", text: "lưu tiền bằng số nguyên" }] };
  must(w.call(w.lead, "respond", { action: "concede", challenge: t.debate, patch: { leadApproach: newApproach } }));
  const task = w.store.ledger.tasks[t.task]!;
  assert.equal(task.briefs.length, 2);
  assert.equal(w.store.ledger.debates[t.debate]!.state, "conceded");
  assert.ok(mailsTo(w, t.peer).some((m) => m.text.includes("lưu tiền bằng số nguyên")));
  w.repo.heads.set(t.worktree, "h1");
  w.repo.ancestry.add("base0>h1");
  must(w.call(t.peer, "handoff", handoffArgs("h1")));
});

test("nhận sai mà bỏ acceptance của mục tiêu squad thì phải leo thang", () => {
  const w = world();
  const t = debating(w);
  const result = w.call(w.lead, "respond", { action: "concede", challenge: t.debate, patch: { acceptance: ["chỉ cần build pass"] } });
  assert.equal(result.code, "must_escalate");
});

test("bế tắc: Lead quyết phải đáp bằng chứng mới nhất của Peer; bất đồng vào handoff và thống kê", () => {
  const w = world();
  const t = deadlocked(w);
  const debate = w.store.ledger.debates[t.debate]!;
  const latest = debate.evidence.filter((e) => e.by === t.peer).at(-1)!.id;
  assert.equal(w.call(w.lead, "respond", { action: "decide", challenge: t.debate, side: "lead", reason: "x", answers: "E1" }).code, "answer_latest_evidence");
  must(w.call(w.lead, "respond", { action: "decide", challenge: t.debate, side: "lead", reason: "giá trong hệ thống luôn tròn trăm, float đủ", answers: latest }));
  assert.equal(w.store.ledger.debates[t.debate]!.state, "decided");
  w.repo.heads.set(t.worktree, "h1");
  w.repo.ancestry.add("base0>h1");
  must(w.call(t.peer, "handoff", handoffArgs("h1")));
  const unknowns = w.store.ledger.tasks[t.task]!.handoff!.unknowns;
  assert.ok(unknowns.some((u) => u.includes(t.debate) && u.includes("float")));
  const effects = w.desk.event({ kind: "ci_done", task: t.task, sha: "h1", results: [{ command: "npm test", exit: 0, output: "ok" }] });
  const done = effects.find((e) => e.kind === "mail" && e.to === w.lead);
  assert.ok(done && done.kind === "mail" && done.text.includes("Lead quyết sau 2 vòng 1"));
  assert.match(w.call(w.sup, "status", {}).text, /giữ 2, quyết 1/);
});

test("quyết theo Peer phải kèm patch", () => {
  const w = world();
  const t = deadlocked(w);
  const latest = w.store.ledger.debates[t.debate]!.evidence.filter((e) => e.by === t.peer).at(-1)!.id;
  assert.equal(w.call(w.lead, "respond", { action: "decide", challenge: t.debate, side: "peer", reason: "Peer đúng", answers: latest }).code, "patch_required");
});

test("Peer rút được cả khi đã bế tắc", () => {
  const w = world();
  const t = deadlocked(w);
  must(w.call(t.peer, "withdraw", { challenge: t.debate, reason: "chấp nhận" }));
  assert.equal(w.store.ledger.debates[t.debate]!.state, "withdrawn");
});

test("leo thang: Supervisor trả lời có nguồn thì tranh luận đóng và câu trả lời thành ràng buộc bắt buộc", () => {
  const w = world();
  const t = debating(w);
  must(w.call(w.lead, "respond", { action: "escalate", challenge: t.debate, why: "đổi kiểu tiền là quyết định sản phẩm" }));
  const ask = Object.values(w.store.ledger.asks).at(-1)!;
  assert.equal(ask.to, w.sup);
  assert.equal(w.call(w.sup, "answer", { ask: ask.id, text: "dùng số nguyên" }).code, "source_required");
  must(w.call(w.sup, "answer", { ask: ask.id, text: "dùng số nguyên", source: "human" }));
  const task = w.store.ledger.tasks[t.task]!;
  assert.equal(w.store.ledger.debates[t.debate]!.state, "decided");
  assert.deepEqual(task.briefs.at(-1)!.required.at(-1), { text: "dùng số nguyên", source: { kind: "human", ref: ask.id } });
});

test("lo ngại về ràng buộc bắt buộc tự thành câu hỏi lên Supervisor", () => {
  const w = world();
  const t = planTask(w, makeBrief({ required: [{ text: "dùng VND", source: { kind: "human", ref: "C1" } }] }));
  must(w.call(t.peer, "propose", { ...PROPOSAL, requiredConcerns: [{ index: 0, concern: "VND không có số lẻ nhưng API trả USD", evidence: [FILE_EVIDENCE] }] }));
  const ask = Object.values(w.store.ledger.asks).at(-1)!;
  assert.equal(ask.to, w.sup);
  assert.deepEqual(ask.concern, { task: t.task, index: 0 });
});

test("Lead nhắn tranh luận của squad khác thì không thấy", () => {
  const w = world();
  const t = debating(w);
  must(w.call(w.sup, "open_squad", { goal: "khác", acceptance: ["x"], slug: "other" }));
  const otherLead = Object.values(w.store.ledger.squads).at(-1)!.lead;
  w.desk.event({ kind: "hc_filled", hc: otherLead, agentId: "agent-lead2" });
  assert.equal(w.call(otherLead, "respond", { action: "concede", challenge: t.debate, patch: { scope: ["src/**"] } }).code, "unknown_debate");
});

test("giữ ý phải có phản chứng mới: không dùng lại phản chứng cũ hay bằng chứng của Peer", () => {
  const w = world();
  const t = debating(w);
  assert.equal(w.call(w.lead, "respond", { action: "hold", challenge: t.debate, counterEvidence: [CMD_EVIDENCE], reason: "chính output đó" }).code, "no_new_evidence");
  must(w.call(w.lead, "respond", { action: "hold", challenge: t.debate, counterEvidence: [LEAD_EVIDENCE], reason: "giá luôn tròn trăm" }));
  must(w.call(t.peer, "rebut", { challenge: t.debate, newEvidence: [FILE_EVIDENCE] }));
  assert.equal(w.call(w.lead, "respond", { action: "hold", challenge: t.debate, counterEvidence: [LEAD_EVIDENCE], reason: "như đã nói" }).code, "no_new_evidence");
  assert.equal(w.store.ledger.debates[t.debate]!.state, "rebutted");
});

test("nhận sai phải đổi brief, và đổi đúng chỗ bị phản biện", () => {
  const w = world();
  const t = debating(w);
  const same = makeBrief().leadApproach;
  assert.equal(w.call(w.lead, "respond", { action: "concede", challenge: t.debate, patch: { leadApproach: same } }).code, "no_change");
  const otherStep = { ...same, steps: [{ id: "s1", text: "viết applyDiscount" }, { id: "s2", text: "gọi từ checkout và order" }] };
  assert.equal(w.call(w.lead, "respond", { action: "concede", challenge: t.debate, patch: { leadApproach: otherStep } }).code, "patch_misses_target");
  assert.equal(w.store.ledger.tasks[t.task]!.briefs.length, 1);
  must(w.call(w.lead, "respond", { action: "concede", challenge: t.debate, patch: { leadApproach: { ...same, current: [] } } }));
});

test("câu trích trong bằng chứng file phải có thật ở các dòng được trỏ tới", () => {
  const w = world();
  const t = planTask(w);
  must(w.call(t.peer, "propose", PROPOSAL));
  const quoted = (lines: [number, number] | undefined, quote: string) => ({ ...CHALLENGE, evidence: [{ kind: "file", path: "docs/rules.md", lines, quote }] });
  assert.equal(w.call(t.peer, "position", { agree: false, challenges: [quoted([3, 4], "giá có thể lẻ")] }).code, "bad_quote");
  assert.equal(w.call(t.peer, "position", { agree: false, challenges: [quoted([1, 2], "giá luôn chia hết cho 100")] }).code, "bad_quote");
  must(w.call(t.peer, "position", { agree: false, challenges: [quoted(undefined, "chia hết  cho 100 kể cả")] }));
});

test("brief đổi làm tranh luận khác trỏ vào chỗ không còn: Peer được báo, tranh luận vẫn chờ Lead", () => {
  const w = world();
  const t = planTask(w);
  must(w.call(t.peer, "propose", PROPOSAL));
  const onStep = { ...CHALLENGE, target: { kind: "step", ref: "s2" }, claim: "checkout không qua cart" };
  must(w.call(t.peer, "position", { agree: false, challenges: [CHALLENGE, onStep] }));
  const [first, second] = Object.values(w.store.ledger.debates).map((d) => d.id);
  const merged = { ...makeBrief().leadApproach, steps: [{ id: "s1", text: "viết price.ts dùng số nguyên" }], current: [{ id: "c1", text: "lưu tiền bằng số nguyên" }] };
  must(w.call(w.lead, "respond", { action: "concede", challenge: first, patch: { leadApproach: merged } }));
  const stale = mailsTo(w, t.peer).filter((m) => m.topic.startsWith("stale:"));
  assert.deepEqual(stale.map((m) => m.topic), [`stale:${second}`]);
  assert.equal(w.store.ledger.debates[second!]!.state, "open");
  must(w.call(w.lead, "respond", { action: "concede", challenge: second, patch: { leadApproach: { ...merged, approach: "tính trong price.ts, cart chỉ gọi" } } }));
});

test("amend_task: Lead sửa brief ngoài tranh luận, Peer phải position lại", () => {
  const w = world();
  const t = runningTask(w);
  const better = { ...makeBrief().leadApproach, approach: "Tách price.ts tính bằng số nguyên cent như Peer đề xuất" };
  assert.equal(w.call(w.lead, "amend_task", { task: t.task, patch: { leadApproach: makeBrief().leadApproach }, reason: "x" }).code, "no_change");
  must(w.call(w.lead, "amend_task", { task: t.task, patch: { leadApproach: better }, reason: "đề xuất của Peer hay hơn" }));
  const task = w.store.ledger.tasks[t.task]!;
  assert.equal(task.state, "debating");
  assert.equal(task.briefs.length, 2);
  assert.ok(mailsTo(w, t.peer).some((m) => m.topic === `amended:${t.task}` && m.text.includes("số nguyên cent")));
  w.repo.heads.set(t.worktree, "h1");
  w.repo.ancestry.add("base0>h1");
  assert.equal(w.call(t.peer, "handoff", handoffArgs("h1")).code, "debate_first");
  must(w.call(t.peer, "position", { agree: false, challenges: [CHALLENGE] }));
  assert.equal(w.call(w.lead, "amend_task", { task: t.task, patch: { scope: ["src/**"] }, reason: "y" }).code, "debate_open");
  assert.match(w.call(w.sup, "status", {}).text, /Lead tự sửa brief 1/);
});

test("amend_task trước khi Peer đề xuất: cách làm vẫn niêm phong; sau handoff thì không sửa được", () => {
  const w = world();
  const t = planTask(w);
  must(w.call(w.lead, "amend_task", { task: t.task, patch: { leadApproach: { ...makeBrief().leadApproach, approach: "cách khác" } }, reason: "nghĩ lại" }));
  assert.equal(w.store.ledger.tasks[t.task]!.state, "proposing");
  assert.ok(!mailsTo(w, t.peer).some((m) => m.topic === `amended:${t.task}`));
  must(w.call(w.lead, "amend_task", { task: t.task, patch: { acceptance: ["test giảm giá pass", "không đổi API"] }, reason: "thêm điều kiện" }));
  const mail = mailsTo(w, t.peer).find((m) => m.topic === `amended:${t.task}`)!;
  assert.ok(mail.text.includes("không đổi API") && !mail.text.includes("cách khác"));
  const other = world();
  const done = checkedTask(other);
  assert.equal(other.call(other.lead, "amend_task", { task: done.task, patch: { scope: ["src/x.ts"] }, reason: "z" }).code, "bad_state");
});

test("bằng chứng gần trùng không tính là mới: khoảng dòng đã nộp, cùng lệnh khác output, cùng đoạn tài liệu", () => {
  const w = world();
  const t = debating(w);
  must(w.call(w.lead, "respond", { action: "hold", challenge: t.debate, counterEvidence: [{ kind: "file", path: "docs/rules.md", lines: [1, 10] }], reason: "luật nói rõ" }));
  assert.equal(w.call(t.peer, "rebut", { challenge: t.debate, newEvidence: [{ ...CMD_EVIDENCE, output: "0.3" }] }).code, "no_new_evidence");
  assert.equal(w.call(t.peer, "rebut", { challenge: t.debate, newEvidence: [{ kind: "file", path: "./docs/rules.md", lines: [3, 4], quote: "chia hết cho 100" }] }).code, "no_new_evidence");
  must(w.call(t.peer, "rebut", { challenge: t.debate, newEvidence: [{ kind: "file", path: "docs/rules.md", lines: [9, 12] }] }));
  assert.equal(w.call(w.lead, "respond", { action: "hold", challenge: t.debate, counterEvidence: [{ kind: "file", path: "docs/rules.md", lines: [5, 5], quote: "giá luôn chia hết cho 100" }], reason: "vẫn vậy" }).code, "no_new_evidence");
});
