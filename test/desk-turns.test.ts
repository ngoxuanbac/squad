import { test } from "node:test";
import assert from "node:assert/strict";
import { makeBrief, planTask, runningTask, world } from "./helpers.ts";

test("lượt lỗi thì báo Lead ngay", () => {
  const w = world();
  const t = runningTask(w);
  const effects = w.desk.event({ kind: "turn_ended", hc: t.peer, outcome: "failed", lastText: "rate limit" });
  assert.ok(effects.some((e) => e.kind === "mail" && e.to === w.lead && e.topic.startsWith("turn_failed:")));
});

test("status cho Supervisor thấy task, tranh luận mở và thống kê", () => {
  const w = world();
  const t = runningTask(w);
  const text = w.call(w.sup, "status", {}).text;
  assert.ok(text.includes(`${w.squad} [open]`));
  assert.ok(text.includes(`${t.task} [running] `));
  assert.match(text, /Peer đồng ý 1, phản biện 0/);
});

test("amend_squad: quyết định giữa chừng của Human thành ràng buộc có id, Lead dùng làm nguồn được", () => {
  const w = world();
  const result = w.call(w.sup, "amend_squad", { squad: w.squad, note: "Human cho phép đổi RULES.md", constraints: [{ text: "cho phép toFixed(6)", source: { kind: "human", ref: "chat 10:30" } }], acceptance: ["RULES.md ghi luật mới"] });
  assert.equal(result.ok, true, result.text);
  assert.match(result.text, /c2/);
  const mail = w.effects.find((e) => e.kind === "mail" && e.to === w.lead && e.topic === `amend:${w.squad}`);
  assert.ok(mail && mail.kind === "mail" && mail.text.includes("c2  cho phép toFixed(6)") && mail.text.includes("acceptance:1"));
  const t = planTask(w, makeBrief({ scope: ["RULES.md"], required: [{ text: "cho phép toFixed(6)", source: { kind: "human", ref: "C2" } }] }));
  assert.equal(w.store.ledger.tasks[t.task]!.state, "proposing");
  assert.equal(w.call(w.lead, "amend_squad", { squad: w.squad, note: "x", acceptance: ["y"] }).code, "not_in_toolset");
  assert.equal(w.call(w.sup, "amend_squad", { squad: w.squad, note: "rỗng" }).code, "bad_args");
});
