import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { git, ok, hcCaller, setup, supervisor, type Env } from "./fakes.ts";

/** This sandbox runs commands with a PATH that has no node; tests call it by full path. */
const NODE = JSON.stringify(process.execPath.replaceAll("\\", "/"));
const cmd = (file: string) => `${NODE} ${file}`;

const GREET = 'exports.greet = (name) => "Hello, " + name;\n';

function brief(overrides: Record<string, unknown> = {}) {
  return {
    title: "greet",
    objective: "greet(name) trả Hello, name",
    acceptance: ["node test.js thoát 0"],
    required: [],
    unknowns: [],
    scope: ["*.js"],
    checks: [cmd("test.js")],
    leadApproach: { approach: "viết greet.js", steps: [{ id: "s1", text: "viết greet.js" }], current: [{ id: "c1", text: "CommonJS" }], risks: [] },
    ...overrides,
  };
}

/** Opens a squad and returns the Lead's caller. */
async function openSquad(env: Env, slug = "greet") {
  const sup = await supervisor(env);
  await ok(sup.caller, "open_squad", { goal: "greet", acceptance: ["node test.js thoát 0"], slug });
  await env.runtime.idle();
  return { sup, lead: hcCaller(env, "s") };
}

/** Plans one brief and walks its Peer to running with an agreement. */
async function startTask(env: Env, lead: Awaited<ReturnType<typeof openSquad>>["lead"], b = brief()) {
  await ok(lead.caller, "plan", { briefs: [b] });
  await env.runtime.idle();
  const peer = hcCaller(env);
  const worktree = env.host.created.find((c) => c.id === peer.agent)!.cwd!;
  await ok(peer.caller, "propose", { approach: "viết greet.js", steps: ["viết"], risks: ["không"], assumptions: [], questions: [] });
  await ok(peer.caller, "position", { agree: true, comparison: [{ point: "module", mine: "CJS", lead: "CJS", whyLeadIsFine: "giống nhau" }] });
  return { peer, worktree, task: peer.hc };
}

function commit(worktree: string, files: Record<string, string>, message = "work"): string {
  for (const [name, content] of Object.entries(files)) fs.writeFileSync(path.join(worktree, name), content);
  git(worktree, "add", ".");
  git(worktree, "commit", "-qm", message);
  return git(worktree, "rev-parse", "HEAD").trim();
}

function handoff(sha: string, exit = 0) {
  return { outcome: "complete", sha, summary: "xong", checks: [{ command: cmd("test.js"), exit, output: exit ? "fail" : "ok" }], skipped: [], unknowns: [] };
}

function leadInbox(env: Env, lead: { agent: string }) {
  return env.host.inbox(lead.agent).join("\n=====\n") + "\n" + env.host.created.find((c) => c.id === lead.agent)!.prompt;
}

/** Mail sits in the outbox while the agent "runs"; this lets every squad agent take it. */
async function drainMail(env: Env) {
  const ids = ["agent-sup", ...env.host.created.map((c) => c.id)];
  for (const id of ids) env.host.statuses.set(id, "idle");
  for (const c of env.host.created.filter((c) => c.labels["squad.role"] !== "peer")) await env.runtime.turnEnded(c.id, "completed", "");
  await env.runtime.turnEnded("agent-sup", "completed", "");
  for (const c of env.host.created.filter((c) => c.labels["squad.role"] === "peer")) {
    env.host.statuses.set(c.id, "idle");
  }
  await env.runtime.idle();
}

/** A Peer's turn ends without it calling a tool; its held mail goes out. */
async function peerTurnEnds(env: Env, agent: string) {
  env.host.statuses.set(agent, "idle");
  await env.runtime.turnEnded(agent, "completed", "");
  await env.runtime.idle();
}

test("cả vòng trên git thật: plan, worktree, handoff, CI, approve, fast-forward, report", async () => {
  const env = await setup({ "README.md": "x\n" });
  const { sup, lead } = await openSquad(env);
  const { peer, worktree, task } = await startTask(env, lead);
  assert.ok(fs.existsSync(worktree));
  assert.equal(git(worktree, "rev-parse", "--abbrev-ref", "HEAD").trim(), `task/${task}-greet`);
  const sha = commit(worktree, { "greet.js": GREET, "test.js": 'if (require("./greet").greet("An") !== "Hello, An") process.exit(1);\n' });
  await ok(peer.caller, "handoff", handoff(sha));
  await env.runtime.idle();
  await drainMail(env);
  assert.match(leadInbox(env, lead), /ok {3}.+ test\.js {2}thật exit 0, Peer khai exit 0/);
  await ok(lead.caller, "verdict", { action: "approve", task, sha, read: ["greet.js", "test.js"] });
  await env.runtime.idle();
  assert.equal(git(env.repo, "rev-parse", "squad/s1-greet").trim(), sha);
  assert.ok(!fs.existsSync(worktree));
  assert.ok(env.host.archived.includes(peer.agent));
  await ok(lead.caller, "report", { summary: "greet xong", approved: [task], open: [], risks: [] });
  await env.runtime.idle();
  await drainMail(env);
  assert.match(env.host.inbox("agent-sup").join("\n"), /sẵn sàng[\s\S]*tất cả ok/);
  await ok(sup.caller, "close_squad", { squad: lead.hc.replace(/-lead$/, "") });
  await env.runtime.idle();
  assert.doesNotMatch(git(env.repo, "branch", "--list", "task/s1-t*"), /task\/s1-t/);
  assert.match(git(env.repo, "branch", "--list", "squad/s1-greet"), /squad\/s1-greet/);
});

test("Peer khai test pass nhưng server chạy lại thấy fail: Lead thấy chỗ lệch", async () => {
  const env = await setup({ "README.md": "x\n" });
  const { lead } = await openSquad(env);
  const { peer, worktree } = await startTask(env, lead);
  const sha = commit(worktree, { "greet.js": GREET, "test.js": "process.exit(3);\n" });
  await ok(peer.caller, "handoff", handoff(sha, 0));
  await env.runtime.idle();
  await drainMail(env);
  assert.match(leadInbox(env, lead), /FAIL .+ test\.js {2}thật exit 3, Peer khai exit 0 {2}← LỆCH/);
});

test("hai task song song gộp sạch nhưng làm hỏng nhau: task nộp sau fail CI trên code đã gộp", async () => {
  const env = await setup({ "greet.js": GREET });
  const { lead } = await openSquad(env);
  const a = await startTask(env, lead, brief({ title: "rename", scope: ["greet.js", "a.js"], checks: [cmd("a.js")] }));
  const b = await startTask(env, lead, brief({ title: "use-greet", scope: ["b.js"], checks: [cmd("b.js")] }));
  const shaA = commit(a.worktree, { "greet.js": 'exports.sayHello = (name) => "Hello, " + name;\n', "a.js": 'require("./greet").sayHello("x");\n' });
  await ok(a.peer.caller, "handoff", { ...handoff(shaA), checks: [{ command: cmd("a.js"), exit: 0, output: "" }] });
  await env.runtime.idle();
  await ok(lead.caller, "verdict", { action: "approve", task: a.task, sha: shaA, read: ["greet.js"] });
  await env.runtime.idle();
  const shaB = commit(b.worktree, { "b.js": 'if (require("./greet").greet("An") !== "Hello, An") process.exit(1);\n' });
  assert.equal(git(b.worktree, "-c", "core.autocrlf=false", "show", `${shaB}:b.js`).length > 0, true);
  await ok(b.peer.caller, "handoff", { ...handoff(shaB), checks: [{ command: cmd("b.js"), exit: 0, output: "" }] });
  await env.runtime.idle();
  await drainMail(env);
  const inbox = leadInbox(env, lead);
  assert.match(inbox, new RegExp(`đã gộp squad/s1-greet vào ${shaB.slice(0, 8)}`));
  assert.match(inbox, /FAIL .+ b\.js/);
});

test("conflict khi gộp (Peer sửa ra ngoài scope): Peer nhận danh sách file, task về running, dấu conflict nằm trong worktree", async () => {
  const env = await setup({ "greet.js": GREET });
  const { lead } = await openSquad(env);
  const a = await startTask(env, lead, brief({ scope: ["greet.js"] }));
  const b = await startTask(env, lead, brief({ scope: ["other.js"] }));
  const shaA = commit(a.worktree, { "greet.js": 'exports.greet = (n) => "Hi, " + n;\n' });
  await ok(a.peer.caller, "handoff", { ...handoff(shaA), checks: [] });
  await env.runtime.idle();
  await ok(lead.caller, "verdict", { action: "approve", task: a.task, sha: shaA, read: ["greet.js"], overFailed: "không có test" });
  await env.runtime.idle();
  const shaB = commit(b.worktree, { "greet.js": 'exports.greet = (n) => "Chào, " + n;\n' });
  await ok(b.peer.caller, "handoff", handoff(shaB));
  await env.runtime.idle();
  await drainMail(env);
  await peerTurnEnds(env, b.peer.agent);
  assert.match(env.host.inbox(b.peer.agent).join("\n"), /conflict[\s\S]*greet\.js/);
  assert.match(fs.readFileSync(path.join(b.worktree, "greet.js"), "utf8"), /<<<<<<</);
  const again = await b.peer.caller.call("handoff", handoff(shaB));
  assert.equal(again.isError, true);
  assert.match(again.text, /dirty|chưa commit/);
});

test("session_open: HC squad có shim đứng đầu PATH, Peer có SQUAD_WORKTREE, agent thường không đổi", async () => {
  const env = await setup();
  const peerEnv = env.runtime.sessionEnv({ provider: "squad-claude-peer", cwd: "/wt/T1", env: { PATH: "/usr/bin" } })!;
  assert.equal(peerEnv.SQUAD_WORKTREE, "/wt/T1");
  assert.ok(peerEnv.PATH!.startsWith(path.join(env.state, "bin")));
  const leadEnv = env.runtime.sessionEnv({ provider: "squad-claude-lead", cwd: "/repo", env: { PATH: "/usr/bin" } })!;
  assert.equal(leadEnv.SQUAD_WORKTREE, undefined);
  assert.equal(env.runtime.sessionEnv({ provider: "claude", cwd: "/repo", env: {} }), undefined);
});

test("workspace của Peer nằm dưới project của repo, không tạo project mới, và được archive khi task xong", async () => {
  const env = await setup({ "README.md": "x\n" });
  const { lead } = await openSquad(env);
  const { peer, worktree, task } = await startTask(env, lead);
  const made = env.host.createdWorkspaces.at(-1)!;
  assert.equal(made.projectId, "prj_repo");
  assert.equal(made.dir, worktree);
  assert.equal(made.title, `${task} peer greet`);
  assert.equal(made.title, env.host.created.find((c) => c.id === peer.agent)!.title, "workspace và agent cùng tên");
  assert.equal(env.host.created.find((c) => c.id === peer.agent)!.ws, made.id);
  const sha = commit(worktree, { "greet.js": GREET, "test.js": "\n" });
  await ok(peer.caller, "handoff", handoff(sha));
  await env.runtime.idle();
  await ok(lead.caller, "verdict", { action: "approve", task, sha, read: ["greet.js"] });
  await env.runtime.idle();
  assert.deepEqual(env.host.archivedWorkspaces, [made.id]);
});

test("Lead có workspace riêng ở đầu nhánh squad, thấy code của task vừa merge; id đếm riêng từ 1", async () => {
  const env = await setup({ "README.md": "x\n" });
  const { lead } = await openSquad(env);
  const leadAgent = env.host.created.find((c) => c.id === lead.agent)!;
  assert.equal(leadAgent.title, "s1 lead greet");
  assert.equal(leadAgent.parent, "agent-sup");
  assert.equal(env.host.createdWorkspaces[0]!.title, "s1 lead greet");
  assert.notEqual(leadAgent.ws, "wks_repo");
  const { peer, worktree, task } = await startTask(env, lead);
  assert.equal(task, "s1-t1");
  const peerAgent = env.host.created.find((c) => c.id === peer.agent)!;
  assert.equal(peerAgent.title, "s1-t1 peer greet");
  assert.equal(peerAgent.parent, lead.agent);
  const sha = commit(worktree, { "greet.js": GREET, "test.js": "\n" });
  await ok(peer.caller, "handoff", handoff(sha));
  await env.runtime.idle();
  await ok(lead.caller, "verdict", { action: "approve", task, sha, read: ["greet.js"] });
  await env.runtime.idle();
  assert.equal(git(leadAgent.cwd!, "rev-parse", "HEAD").trim(), sha);
  assert.ok(fs.existsSync(path.join(leadAgent.cwd!, "greet.js")));
});

test("working copy bị khoá khi đang dùng; đóng squad mà còn thay đổi chưa commit thì giữ lại cho Human", async () => {
  const env = await setup({ "README.md": "x\n" });
  const { sup, lead } = await openSquad(env);
  const { worktree } = await startTask(env, lead);
  assert.match(git(env.repo, "worktree", "list", "--porcelain"), new RegExp(`locked squad: the working copy of s1-t1`));
  fs.writeFileSync(path.join(worktree, "draft.js"), "// chưa commit\n");
  await ok(sup.caller, "close_squad", { squad: "s1" });
  await env.runtime.idle();
  assert.ok(fs.existsSync(path.join(worktree, "draft.js")), "working copy còn việc chưa commit phải được giữ");
  assert.doesNotMatch(git(env.repo, "worktree", "list", "--porcelain"), /locked squad: the working copy of s1-t1/);
  const leadDir = env.host.created.find((c) => c.id === lead.agent)!.cwd!;
  assert.ok(!fs.existsSync(leadDir), "working copy của Lead sạch thì bị xoá");
  await drainMail(env);
  assert.match(env.host.inbox("agent-sup").join("\n"), /chưa commit nên squad giữ lại/);
});

/** Accepts a task that adds greet.js and test.js, so the squad has one merged task. */
async function mergedTask(env: Env, lead: Awaited<ReturnType<typeof openSquad>>["lead"]) {
  const { peer, worktree, task } = await startTask(env, lead);
  const sha = commit(worktree, { "greet.js": GREET, "test.js": 'if (require("./greet").greet("An") !== "Hello, An") process.exit(1);\n' });
  await ok(peer.caller, "handoff", handoff(sha));
  await env.runtime.idle();
  await ok(lead.caller, "verdict", { action: "approve", task, sha, read: ["greet.js", "test.js"] });
  await env.runtime.idle();
  return { task, sha };
}

test("report khi base đã đi tiếp: chạy CI trên bản gộp thử, nhánh squad không đổi; conflict thì báo file và không chạy CI", async () => {
  const env = await setup({ "README.md": "x\n" });
  const base = git(env.repo, "rev-parse", "--abbrev-ref", "HEAD").trim();
  const { lead } = await openSquad(env);
  const { task, sha } = await mergedTask(env, lead);
  commit(env.repo, { "NOTES.md": "base đi tiếp\n" }, "base moves");
  await ok(lead.caller, "report", { summary: "xong", approved: [task], open: [], risks: [] });
  await env.runtime.idle();
  await drainMail(env);
  const clean = env.host.inbox("agent-sup").join("\n");
  assert.ok(clean.includes(`${base} đã đi tiếp 1 commit`));
  assert.match(clean, /không conflict[\s\S]*tất cả ok/);
  assert.equal(git(env.repo, "rev-parse", "squad/s1-greet").trim(), sha);

  const env2 = await setup({ "README.md": "x\n" });
  const second = await openSquad(env2);
  const merged = await mergedTask(env2, second.lead);
  commit(env2.repo, { "greet.js": "exports.greet = () => 'Hi';\n" }, "base adds greet");
  await ok(second.lead.caller, "report", { summary: "xong", approved: [merged.task], open: [], risks: [] });
  await env2.runtime.idle();
  await drainMail(env2);
  const conflicted = env2.host.inbox("agent-sup").join("\n");
  assert.match(conflicted, /bị conflict ở: greet\.js/);
  assert.doesNotMatch(conflicted, /CheckRun trên/);
  assert.equal(git(env2.repo, "rev-parse", "squad/s1-greet").trim(), merged.sha);
});

test("commit của Peer mang danh tính squad của HC, không phải của Human", async () => {
  const env = await setup({ "README.md": "x\n" });
  const { lead } = await openSquad(env);
  const { peer, worktree } = await startTask(env, lead);
  const hcEnv = env.runtime.sessionEnv({ provider: "squad-claude-peer", cwd: worktree, env: {} })!;
  assert.equal(hcEnv.GIT_AUTHOR_NAME, `squad ${peer.hc}`);
  assert.equal(hcEnv.GIT_COMMITTER_EMAIL, `squad.${peer.hc}@squad.local`);
  assert.equal(env.runtime.sessionEnv({ provider: "squad-claude-lead", cwd: env.repo, env: {} })!.GIT_AUTHOR_NAME, undefined);
});

test("Lead bị archive tay: câu hỏi của nó thôi chờ trả lời; đóng squad vẫn archive workspace của Lead", async () => {
  const env = await setup({ "README.md": "x\n" });
  const { sup, lead } = await openSquad(env);
  await ok(lead.caller, "ask", { question: "có cần dấu phân cách không?" });
  const leadWs = env.host.created.find((c) => c.id === lead.agent)!.ws;
  env.runtime.agentArchived(lead.agent);
  await env.runtime.idle();
  await drainMail(env);
  assert.match(env.host.inbox("agent-sup").join("\n"), /HC s1-lead không còn agent[\s\S]*không cần trả lời nữa: s1-a1/);
  const refused = await sup.caller.call("answer", { ask: "S1-A1", text: "có" });
  assert.match(refused.text, /asker_gone/);
  await ok(sup.caller, "close_squad", { squad: "s1" });
  await env.runtime.idle();
  assert.ok(env.host.archivedWorkspaces.includes(leadWs));
});
