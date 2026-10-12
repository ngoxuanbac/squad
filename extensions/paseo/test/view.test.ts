import assert from "node:assert/strict";
import { test } from "node:test";
import { agoText, eventColour, findMember, isBusy, noticeColour, parseJson, readTail, readView, statusColour, treeOf, VIEW_VERSION, type View } from "../shared/view.ts";

/** A ps --view document as internal/view writes it, plus a taskforce, a solo with its own taskforce, and a closed team. */
const member = (name: string, extra: Record<string, unknown> = {}) => ({
  id: `p_${name}`,
  name,
  role: "lead",
  kind: "worker",
  status: "idle",
  model: "sonnet",
  reports_to: "api",
  task: "",
  unread: 0,
  cwd: "C:\\code\\api",
  last_active: "2026-10-11T08:05:00Z",
  ...extra,
});
const team = (id: string, name: string, members: unknown[], extra: Record<string, unknown> = {}) => ({
  id,
  name,
  template: "slp",
  root: "C:\\code\\api",
  status: "open",
  gate: name,
  parent: "",
  created_at: "2026-10-11T08:01:00Z",
  members,
  ...extra,
});
const doc = {
  version: 1,
  daemon: { version: "0.1.0", pid: 1234, started_at: "2026-10-11T08:00:00Z" },
  summary: { teams: 3, sessions: 1, workers: 3, working: 1 },
  teams: [
    team("t_api", "api", [member("api", { kind: "session", role: "supervisor" }), member("lead-1", { status: "working", task: "First line", unread: 2 })]),
    team("t_fix", "fix", [member("fix-1")], { parent: "lead-1" }),
    team("t_solo", "probe", [member("probe-1")], { parent: "me" }),
    team("t_lost", "lost", [], { parent: "nobody" }),
    team("t_old", "old", [member("old-1", { status: "gone" })], { status: "closed" }),
  ],
  solos: [member("me", { kind: "session", role: "", reports_to: "" })],
  events: [{ at: "2026-10-11T08:02:00Z", kind: "spawn", team: "api", text: "api spawned lead-1 (lead)" }],
};

const view = (): View => {
  const r = readView(doc);
  assert.ok(r.ok);
  return r.value;
};

test("reads ps --view of the version it knows", () => {
  const v = view();
  assert.equal(v.version, VIEW_VERSION);
  assert.equal(v.daemon.pid, 1234);
  assert.equal(v.teams[0].members[1].unread, 2);
  assert.equal(v.events[0].kind, "spawn");
  assert.equal(isBusy(v), true);
});

test("refuses another version, saying which side to update", () => {
  const older = readView({ ...doc, version: 0 });
  const newer = readView({ ...doc, version: 2 });
  const none = readView({ teams: [] });
  assert.deepEqual([older.ok || older.code, newer.ok || newer.code, none.ok || none.code], ["older", "newer", "older"]);
  assert.match(older.ok ? "" : older.error, /Update squad/);
  assert.match(newer.ok ? "" : newer.error, /Reinstall the plugin/);
  for (const bad of [null, [], "x", 1]) assert.equal(readView(bad).ok || (readView(bad) as { code: string }).code, "failed");
});

test("fills missing fields and drops malformed entries", () => {
  const r = readView({ version: 1, teams: [{ name: "bare", members: [null, { name: "m" }] }, "junk"], solos: "nope" });
  assert.ok(r.ok);
  assert.deepEqual(r.value.summary, { teams: 0, sessions: 0, workers: 0, working: 0 });
  assert.equal(r.value.teams.length, 1);
  assert.deepEqual(r.value.teams[0].members.map((m) => [m.name, m.status, m.unread]), [["m", "", 0]]);
  assert.deepEqual(r.value.solos, []);
  assert.equal(isBusy(r.value), false);
});

test("taskforces hang under their caller; an unknown caller stays at the top; closed teams are apart", () => {
  const tree = treeOf(view());
  assert.deepEqual(tree.teams.map((t) => t.team.name), ["api", "lost"]);
  const lead = tree.teams[0].members.find((m) => m.member.name === "lead-1");
  assert.deepEqual(lead?.taskforces.map((t) => t.team.name), ["fix"]);
  assert.deepEqual(tree.solos[0].taskforces.map((t) => t.team.name), ["probe"]);
  assert.deepEqual(tree.closed.map((t) => t.name), ["old"]);
});

test("taskforces that call each other do not vanish", () => {
  const r = readView({
    version: 1,
    teams: [team("t_a", "a", [member("a-1")], { parent: "b-1" }), team("t_b", "b", [member("b-1")], { parent: "a-1" }), team("t_c", "c", [member("c-1")], { parent: "c-1" })],
  });
  assert.ok(r.ok);
  assert.deepEqual(treeOf(r.value).teams.map((t) => t.team.name), ["a", "b", "c"]);
});

test("finds a member or solo by id", () => {
  const v = view();
  assert.equal(findMember(v, "p_lead-1")?.team?.name, "api");
  assert.equal(findMember(v, "p_me")?.team, null);
  assert.equal(findMember(v, "p_none"), null);
});

test("reads tail --view, keeping rest only when given", () => {
  const r = readTail({ version: 1, lines: [{ kind: "tool", text: "Bash", rest: "go test ./..." }, { kind: "assistant", text: "hi" }, 3] });
  assert.ok(r.ok);
  assert.deepEqual(r.value, [{ kind: "tool", text: "Bash", rest: "go test ./..." }, { kind: "assistant", text: "hi" }]);
  const newer = readTail({ version: 2, lines: [] });
  assert.equal(newer.ok || newer.code, "newer");
});

test("parseJson refuses text that is not JSON", () => {
  assert.equal(parseJson('{"version":1}', "ps").ok, true);
  const bad = parseJson("NAME STATUS", "ps");
  assert.equal(bad.ok || bad.code, "failed");
});

test("words and colours", () => {
  const now = Date.parse("2026-10-11T09:00:00Z");
  assert.equal(agoText("2026-10-11T08:59:50Z", now), "just now");
  assert.equal(agoText("2026-10-11T08:39:00Z", now), "21m ago");
  assert.equal(agoText("2026-10-11T06:00:00Z", now), "3h ago");
  assert.equal(agoText("", now), "");
  assert.equal(agoText("garbage", now), "");
  assert.equal(statusColour("working"), "statusSuccess");
  assert.equal(statusColour("something-new"), "foregroundMuted");
});

test("reads notices, newest first as squad gives them, and colours new states", () => {
  const r = readView({
    version: VIEW_VERSION,
    notices: [
      { at: "2026-10-11T08:00:00Z", age: "2m", where: "api", kind: "failed", body: "lead (gate of api) failed a turn on mail" },
      { at: "2026-10-11T07:00:00Z", age: "1h", where: "-", kind: "reply", body: "solo finished a turn" },
      "not a notice",
    ],
  });
  assert.ok(r.ok);
  assert.equal(r.value.notices.length, 2);
  assert.deepEqual(r.value.notices[0], { at: "2026-10-11T08:00:00Z", age: "2m", where: "api", kind: "failed", body: "lead (gate of api) failed a turn on mail" });
  const none = readView({ version: VIEW_VERSION });
  assert.ok(none.ok);
  assert.deepEqual(none.value.notices, []);
  assert.equal(noticeColour("failed"), "statusDanger");
  assert.equal(noticeColour("gate_lost"), "statusDanger");
  assert.equal(noticeColour("settled"), "statusSuccess");
  assert.equal(noticeColour("reply"), "accent");
  assert.equal(statusColour("permission"), "statusWarning");
  assert.equal(statusColour("parked"), "statusWarning");
  assert.equal(eventColour("held"), "statusWarning");
});
