import { test } from "node:test";
import assert from "node:assert/strict";
import { makeBrief, must, world } from "./helpers.ts";

test("plan từ chối khi vượt số Peer làm cùng lúc của workspace; vừa đủ chỗ thì nhận", () => {
  const w = world();
  const briefs = ["a", "b", "c"].map((d) => makeBrief({ scope: [`${d}/**`] }));
  must(w.call(w.lead, "plan", { briefs }));
  const over = w.call(w.lead, "plan", { briefs: [makeBrief({ scope: ["d/**"] }), makeBrief({ scope: ["e/**"] })] });
  assert.equal(over.code, "too_many_peers");
  assert.match(over.text, /đã có 3 Peer đang làm, tối đa 4; lần plan này thêm 2/);
  must(w.call(w.lead, "plan", { briefs: [makeBrief({ scope: ["d/**"] })] }));
});

test("Lead bị từ chối vì hết Peer được báo khi có Peer xong; task của squad đã đóng không giữ chỗ", () => {
  const w = world();
  const briefs = ["a", "b", "c", "d"].map((d) => makeBrief({ scope: [`${d}/**`] }));
  must(w.call(w.lead, "plan", { briefs }));
  must(w.call(w.sup, "open_squad", { goal: "khác", acceptance: ["x"], slug: "other" }));
  const other = Object.values(w.store.ledger.squads).at(-1)!;
  w.desk.event({ kind: "hc_filled", hc: other.lead, agentId: "agent-lead2" });
  assert.equal(w.call(other.lead, "plan", { briefs: [makeBrief({ scope: ["e/**"] })] }).code, "too_many_peers");
  assert.equal(w.store.ledger.squads[other.id]!.waitingForHc, true);
  const t = Object.values(w.store.ledger.tasks)[0]!;
  w.store.ledger.tasks[t.id]!.state = "approved";
  const effects = w.desk.event({ kind: "merged", task: t.id });
  assert.ok(effects.some((e) => e.kind === "mail" && e.to === other.lead && e.topic === `hc_free:${other.id}` && e.text.includes("1 chỗ")));
  assert.equal(w.store.ledger.squads[other.id]!.waitingForHc, undefined);
  must(w.call(w.sup, "close_squad", { squad: w.squad }));
  w.repo.refs.set(other.branch, "base0");
  must(w.call(other.lead, "plan", { briefs: ["e", "f", "g", "h"].map((d) => makeBrief({ scope: [`${d}/**`] })) }));
});
