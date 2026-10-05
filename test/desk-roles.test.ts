import { test } from "node:test";
import assert from "node:assert/strict";
import { agentOptions, CatalogError, kindOf, loadCatalog, pickModel } from "../server/hc/catalog.ts";
import { checkedTask, content, mailsTo, makeBrief, must, readHarness, realCatalog, runningTask, world } from "./helpers.ts";

const PATHS = { state: "C:\\data\\squad", home: "C:\\Users\\me" };
type Options = { allowedTools: string[]; extraArgs: Record<string, string | null>; settings: { permissions: { deny: string[] } } };

test("catalog thật: đúng 3 vai trò, chưa có biến thể nào", () => {
  const catalog = realCatalog();
  assert.deepEqual(Object.keys(catalog.kinds).sort(), ["lead", "peer", "supervisor"]);
  assert.equal(kindOf(catalog, { role: "peer" }).name, "peer");
  assert.ok(catalog.kinds.peer!.squadTools.includes("handoff"));
  assert.deepEqual(catalog.forbidden, ["Agent", "Task", "AskUserQuestion", "SendMessage"]);
});

test("Lead và Supervisor không có Bash; tool ngoài allow không tồn tại nhờ --tools", () => {
  const catalog = realCatalog();
  for (const role of ["supervisor", "lead"]) {
    const options = agentOptions(catalog, readHarness(), catalog.kinds[role]!, PATHS) as Options;
    assert.equal(options.extraArgs.tools, "Read,Grep,Glob,TaskCreate,TaskUpdate,TaskList,TaskGet");
    assert.ok(!options.allowedTools.includes("Bash"));
    assert.ok("strict-mcp-config" in options.extraArgs);
  }
});

test("Peer có Bash kèm deny git push", () => {
  const catalog = realCatalog();
  const peer = agentOptions(catalog, readHarness(), catalog.kinds.peer!, PATHS) as Options;
  assert.ok(peer.extraArgs.tools!.split(",").includes("Bash"));
  assert.ok(peer.settings.permissions.deny.includes("Bash(git push:*)"));
  assert.ok(peer.settings.permissions.deny.includes("Read(C:/data/squad/workspaces/**)"));
  assert.ok(peer.settings.permissions.deny.includes("Agent"));
  assert.ok(!peer.settings.permissions.deny.some((d) => d.includes("worktrees")));
});

test("server/harness/claude ra đúng tham số Claude cho từng loại HC", () => {
  const catalog = realCatalog();
  const common = [
    "Agent",
    "Task",
    "AskUserQuestion",
    "SendMessage",
    "Read(C:/data/squad/workspaces/**)",
    "Edit(C:/data/squad/workspaces/**)",
    "Read(C:/data/squad/config.json)",
    "Edit(C:/data/squad/config.json)",
    "Read(C:/Users/me/.paseo/**)",
    "Edit(C:/Users/me/.paseo/**)",
  ];
  const git = ["push", "pull", "fetch", "checkout", "switch", "merge", "rebase", "reset --hard", "worktree"].map((c) => `Bash(git ${c}:*)`);
  const expected: Record<string, { allow: string[]; deny: string[] }> = {
    supervisor: { allow: ["Read", "Grep", "Glob", "TaskCreate", "TaskUpdate", "TaskList", "TaskGet"], deny: common },
    lead: { allow: ["Read", "Grep", "Glob", "TaskCreate", "TaskUpdate", "TaskList", "TaskGet"], deny: common },
    peer: { allow: ["Read", "Grep", "Glob", "Edit", "Write", "NotebookEdit", "TaskCreate", "TaskUpdate", "TaskList", "TaskGet", "Bash"], deny: [...common, ...git] },
  };
  for (const [name, want] of Object.entries(expected)) {
    const options = agentOptions(catalog, readHarness(), catalog.kinds[name]!, PATHS) as Options;
    assert.deepEqual(options.allowedTools, want.allow, name);
    assert.deepEqual(options.extraArgs, { tools: want.allow.join(","), "strict-mcp-config": null, "setting-sources": "project" }, name);
    assert.deepEqual(options.settings.permissions.deny, want.deny, name);
  }
  assert.deepEqual(readHarness().models.sonnet, { id: "claude-sonnet-5-5", label: "Sonnet 5.5" });
});

/** Loads the real files with one of them changed. */
function loadWith(change: (files: Record<string, string>) => void) {
  return () => {
    const files = content();
    change(files);
    return loadCatalog(files, readHarness());
  };
}

function editJson(files: Record<string, string>, file: string, edit: (value: any) => void) {
  const value = JSON.parse(files[file]!);
  edit(value);
  files[file] = JSON.stringify(value);
}

const PEER_SETTINGS = "server/harness/claude/settings/peer.settings.json";
const fails = (pattern: RegExp) => (e: Error) => e instanceof CatalogError && pattern.test(e.message);

test("settings sai thì catalog không nạp: tool bị cấm, gõ sai tên, mẫu trong allow, thiếu file", () => {
  assert.throws(loadWith((f) => editJson(f, PEER_SETTINGS, (s) => s.permissions.allow.push("AskUserQuestion"))), fails(/tool bị cấm trong allow: AskUserQuestion/));
  assert.throws(loadWith((f) => editJson(f, PEER_SETTINGS, (s) => s.permissions.allow.push("Webfetch"))), fails(/không có tool Webfetch \(ý là WebFetch\?\)/));
  assert.throws(loadWith((f) => editJson(f, PEER_SETTINGS, (s) => s.permissions.allow.push("Bash(git diff:*)"))), fails(/mẫu chỉ dùng trong deny/));
  assert.throws(loadWith((f) => editJson(f, PEER_SETTINGS, (s) => s.permissions.deny.push("Bsh(git push:*)"))), fails(/không có tool Bsh/));
  assert.throws(loadWith((f) => delete f[PEER_SETTINGS]), fails(/thiếu file .*peer\.settings\.json/));
});

/** The real files plus a Peer variant, the way a later kind such as a scout would be added. */
function withScout(files: Record<string, string>, edit: (r: any) => void = () => {}) {
  editJson(files, "roles.json", (j) => j.roles.push({ role: "scout", like: "peer", label: "Scout", squadTools: ["ask"], prompt: "content/prompts/scout.md" }));
  editJson(files, "roles.json", (j) => edit(j.roles.find((r: any) => r.role === "scout")));
  files["content/prompts/scout.md"] = "You read the repo and report.";
  files["server/harness/claude/settings/scout.settings.json"] = files[PEER_SETTINGS]!;
}

test("biến thể like: peer chạy trên provider Peer, nhận models và chữ cấm của Peer", () => {
  const catalog = loadWith((f) => withScout(f))();
  assert.equal(catalog.kinds.scout!.role, "peer");
  assert.deepEqual(catalog.kinds.scout!.models, catalog.kinds.peer!.models);
});

test("roles.json sai thì catalog không nạp: vai trò thứ tư, model ngoài provider, tool squad lạ, prompt lộ chữ", () => {
  const role = (name: string, edit: (r: any) => void) => loadWith((f) => editJson(f, "roles.json", (j) => edit(j.roles.find((r: any) => r.role === name))));
  assert.throws(loadWith((f) => withScout(f, (r) => (r.like = "lead"))), fails(/chỉ có 3 vai trò/));
  assert.throws(loadWith((f) => withScout(f, (r) => (r.models = ["opus"]))), fails(/model opus ngoài models của peer/));
  assert.throws(role("peer", (r) => r.squadTools.push("open_squadx")), fails(/không có tool squad open_squadx/));
  assert.throws(loadWith((f) => (withScout(f), (f["content/prompts/scout.md"] += "\nTell the Supervisor."))), fails(/không được nhắc tới supervisor/));
  for (const missing of ["lead", "peer"]) {
    assert.throws(loadWith((f) => editJson(f, "roles.json", (j) => (j.roles = j.roles.filter((r: any) => r.role !== missing)))), fails(new RegExp(`thiếu ${missing}$`)));
  }
});

test("model: chọn trong models của loại HC; desk từ chối model ngoài vai trò", () => {
  const catalog = realCatalog();
  assert.equal(pickModel(catalog.kinds.peer!, undefined), "sonnet");
  assert.equal(pickModel(catalog.kinds.peer!, "haiku"), "haiku");
  assert.equal(pickModel(catalog.kinds.lead!, "sonnet"), "opus");
  const w = world();
  assert.equal(w.call(w.lead, "plan", { briefs: [makeBrief({ model: "opus" })] }).code, "bad_model");
  must(w.call(w.lead, "plan", { briefs: [makeBrief({ model: "haiku" })] }));
});

test("tool ngoài tool set bị từ chối: Peer gọi verdict, tool không có, hay message", () => {
  const w = world();
  const t = checkedTask(w);
  assert.equal(w.call(t.peer, "verdict", { action: "approve", task: t.task, sha: "h1", read: ["x"] }).code, "not_in_toolset");
  assert.equal(w.call(t.peer, "findings", {}).code, "not_in_toolset");
  assert.equal(w.call(t.peer, "message", { to: w.lead, text: "hi" }).code, "not_in_toolset");
  assert.equal(w.call(w.lead, "open_squad", { goal: "x", acceptance: ["y"] }).code, "not_in_toolset");
});

test("mỗi workspace chỉ một Supervisor còn sống", () => {
  const w = world();
  const again = w.desk.supervisor("ws1");
  assert.equal(again.ok, false);
});

test("Supervisor mất rồi được thay vẫn ngồi HC supervisor: câu hỏi đang chờ vẫn trả lời được", () => {
  const w = world();
  must(w.call(w.lead, "ask", { question: "base là main à?" }));
  w.desk.event({ kind: "hc_lost", hc: w.sup });
  assert.equal(w.store.ledger.hcs[w.sup]!.state, "open", "chỗ ngồi còn, chỉ trống người");
  assert.equal(w.store.ledger.asks["s1-a1"]!.state, "open");
  assert.deepEqual(w.desk.supervisor("ws1"), { ok: true, hc: w.sup });
  w.desk.event({ kind: "hc_filled", hc: w.sup, agentId: "agent-sup-2" });
  must(w.call(w.sup, "answer", { ask: "s1-a1", text: "đúng, base là main" }));
  assert.equal(w.store.ledger.squads[w.squad]!.supervisor, w.sup);
});

test("tin Supervisor gửi Peer tự kèm cho Lead; Lead chỉ nhắn trong cây của mình", () => {
  const w = world();
  const t = runningTask(w);
  must(w.call(w.sup, "message", { to: t.peer, text: "làm tới đâu rồi?" }));
  assert.ok(mailsTo(w, t.peer).some((m) => m.text.includes("làm tới đâu rồi?")));
  assert.ok(mailsTo(w, w.lead).some((m) => m.topic === `cc:${t.peer}`));
  must(w.call(w.sup, "open_squad", { goal: "khác", acceptance: ["x"], slug: "other" }));
  const other = Object.values(w.store.ledger.squads).at(-1)!;
  w.desk.event({ kind: "hc_filled", hc: other.lead, agentId: "agent-lead2" });
  assert.equal(w.call(other.lead, "message", { to: t.peer, text: "chen ngang" }).code, "not_your_tree");
});

test("HC đã archive không gọi được tool", () => {
  const w = world();
  const t = checkedTask(w);
  must(w.call(w.lead, "verdict", { action: "approve", task: t.task, sha: "h1", read: ["src/cart.ts"] }));
  w.desk.event({ kind: "merged", task: t.task });
  assert.equal(w.call(t.peer, "ask", { question: "?", bestGuess: "x" }).code, "unknown_hc");
});
