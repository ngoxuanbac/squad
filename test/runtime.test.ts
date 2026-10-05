import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import type http from "node:http";
import type { Runtime } from "../server/runtime.ts";
import { FAKE_CLAUDE, setup, supervisor } from "./fakes.ts";
import { startMcpServer } from "../server/mcp/http.ts";
import { toolDescriptors } from "../server/mcp/tools.ts";
import { ROOT } from "./helpers.ts";

test("lần có API đầu tiên ghi 3 provider squad tắt tool Paseo; lần sau không ghi lại", async () => {
  const env = await setup();
  const providers = env.host.config.providers as Record<string, { extends: string; paseoTools: { enabled: boolean } }>;
  for (const role of ["supervisor", "lead", "peer"]) {
    assert.equal(providers[`squad-claude-${role}`]!.extends, "claude");
    assert.equal(providers[`squad-claude-${role}`]!.paseoTools.enabled, false);
  }
  assert.ok(providers.codex);
  await env.runtime.connected();
  assert.equal(env.host.patches.length, 1);
});

test("provider chỉ liệt kê model của vai trò và chặn tool cấm ở mức provider", async () => {
  const env = await setup();
  const providers = env.host.config.providers as Record<string, { models: { id: string; isDefault?: boolean }[]; disallowedTools: string[] }>;
  const ids = (role: string) => providers[`squad-claude-${role}`]!.models.map((m) => m.id);
  assert.deepEqual(ids("supervisor"), ["claude-sonnet-5-5"]);
  assert.deepEqual(ids("lead"), ["claude-opus-5-5", "claude-fable-5-1"]);
  assert.deepEqual(ids("peer"), ["claude-sonnet-5-5", "claude-haiku-4-5"]);
  assert.equal(providers["squad-claude-lead"]!.models[0]!.isDefault, true);
  assert.equal(providers["squad-claude-lead"]!.models[1]!.isDefault, undefined);
  assert.ok(providers["squad-claude-peer"]!.disallowedTools.includes("Agent"));
});

test("provider khởi động HC qua launcher; session_open của HC squad mới có dấu cấp", async () => {
  const env = await setup();
  const providers = env.host.config.providers as Record<string, { command: string[]; env: Record<string, string> }>;
  const lead = providers["squad-claude-lead"]!;
  assert.equal(lead.command.length, 3);
  assert.match(lead.command[1]!.replaceAll("\\", "/"), /bin\/hc-launcher\.mjs$/);
  assert.equal(lead.command[2], FAKE_CLAUDE);
  assert.ok(fs.existsSync(lead.command[1]!));
  assert.equal(lead.env.SQUAD_GRANT, "");
  assert.equal(lead.command[0], "node");
  const opened = env.runtime.sessionEnv({ provider: "squad-claude-supervisor", cwd: env.repo, env: {} })!;
  assert.ok(opened.SQUAD_GRANT && opened.SQUAD_GRANT.length >= 30);
  assert.equal(env.runtime.sessionEnv({ provider: "claude", cwd: env.repo, env: {} }), undefined);
  assert.ok((await env.runtime.openSession({ provider: "squad-claude-lead", cwd: env.repo, env: {} }))?.SQUAD_GRANT);
  env.runtime.serveOn(Promise.resolve(false));
  assert.equal(await env.runtime.openSession({ provider: "squad-claude-lead", cwd: env.repo, env: {} }), undefined);
});

test("Supervisor luôn chạy Sonnet dù request chọn model khác", async () => {
  const env = await setup();
  const asked = await env.runtime.beforeCreate({ config: { provider: "squad-claude-supervisor/claude-opus-5-5", cwd: env.repo } });
  assert.equal(asked!.config.provider, "squad-claude-supervisor");
  assert.equal(asked!.config.model, "claude-sonnet-5-5");
});

test("Lead chạy opus, fable khi Supervisor chọn; model ngoài danh sách bị từ chối", async () => {
  const env = await setup();
  const { secret } = await supervisor(env);
  const caller = env.runtime.caller(secret) as NonNullable<ReturnType<Runtime["caller"]>>;
  const refused = await caller.call("open_squad", { goal: "g", acceptance: ["a"], slug: "alpha", model: "sonnet" });
  assert.match(refused.text, /bad_model/);
  assert.equal((await caller.call("open_squad", { goal: "g", acceptance: ["a"], slug: "plain" })).isError, false);
  assert.equal((await caller.call("open_squad", { goal: "g", acceptance: ["a"], slug: "hard", model: "fable" })).isError, false);
  await env.runtime.idle();
  const leads = env.host.created.filter((c) => c.labels["squad.role"] === "lead");
  assert.deepEqual(leads.map((l) => l.model), ["claude-opus-5-5", "claude-fable-5-1"]);
});

test("Supervisor: hook gắn prompt, MCP có secret, tool được phép và mode default", async () => {
  const env = await setup();
  const { request, secret } = await supervisor(env);
  const options = request.config.providerOptions as { extraArgs: { tools: string } };
  assert.equal(options.extraArgs.tools, "Read,Grep,Glob,TaskCreate,TaskUpdate,TaskList,TaskGet");
  assert.equal(request.config.modeId, "default");
  assert.equal(request.config.title, "supervisor");
  assert.match(String(request.config.systemPrompt), /Supervisor/);
  assert.ok(secret.length >= 30);
  const project = JSON.parse(fs.readFileSync(path.join(env.state, "workspaces", "wks_repo", "project.json"), "utf8"));
  assert.equal(project.base, "main");
  await assert.rejects(env.runtime.beforeCreate({ config: { provider: "squad-claude-supervisor", cwd: env.repo } }), /đã có Supervisor/);
});

test("tạo Lead hay Peer bằng tay bị hook từ chối; provider khác squad thì bỏ qua", async () => {
  const env = await setup();
  await assert.rejects(env.runtime.beforeCreate({ config: { provider: "squad-claude-lead", cwd: env.repo } }), /chỉ được tạo bởi squad/);
  assert.equal(await env.runtime.beforeCreate({ config: { provider: "claude", cwd: env.repo } }), undefined);
});

test("secret sai thì không có caller; Supervisor gọi status qua MCP", async () => {
  const env = await setup();
  const { secret } = await supervisor(env);
  assert.equal(env.runtime.caller("x".repeat(32)), null);
  const caller = env.runtime.caller(secret);
  assert.ok(caller);
  assert.ok(caller.tools.includes("open_squad"));
  const result = await caller.call("status", {});
  assert.equal(result.isError, false);
  assert.equal(result.text, "Chưa có squad nào.");
  const refused = await caller.call("plan", {});
  assert.equal(refused.isError, true);
  assert.match(refused.text, /Bị từ chối \(not_in_toolset\)/);
});

test("open_squad tạo nhánh squad và Lead thật với provider, label, tool và secret riêng", async () => {
  const env = await setup();
  const { secret } = await supervisor(env);
  const caller = env.runtime.caller(secret) as NonNullable<ReturnType<Runtime["caller"]>>;
  const result = await caller.call("open_squad", { goal: "Hỗ trợ mã giảm giá", acceptance: ["test pass"], slug: "discount" });
  assert.equal(result.isError, false, result.text);
  await env.runtime.idle();
  assert.match(execFileSync("git", ["branch", "--list", "squad/s1-discount"], { cwd: env.repo, encoding: "utf8" }), /squad\/s1-discount/);
  const lead = env.host.created.at(-1)!;
  assert.equal(lead.provider, "squad-claude-lead");
  assert.notEqual(lead.ws, "wks_repo");
  assert.equal(lead.title, "s1 lead discount");
  assert.equal(lead.parent, "agent-sup");
  assert.match(lead.cwd!.replaceAll("\\", "/"), /worktrees\/squad-repo-[^/]+\/s1-discount\/lead$/);
  const head = (cwd: string, ref: string) => execFileSync("git", ["rev-parse", ref], { cwd, encoding: "utf8" }).trim();
  assert.equal(head(lead.cwd!, "HEAD"), head(env.repo, "squad/s1-discount"));
  assert.equal(lead.labels["squad.role"], "lead");
  assert.equal(lead.env.SQUAD_HC, lead.labels["squad.hc"]);
  assert.equal((lead.options.extraArgs as { tools: string }).tools, "Read,Grep,Glob,TaskCreate,TaskUpdate,TaskList,TaskGet");
  assert.ok(lead.squadTools.includes("plan") && !lead.squadTools.includes("open_squad"));
  assert.match(lead.prompt, /Hỗ trợ mã giảm giá/);
  assert.match(lead.prompt, /Bước tiếp: plan/);
  assert.ok(!lead.mcpUrl.includes(secret));
  assert.equal(await env.runtime.beforeCreate({ config: { provider: "squad-claude-lead", cwd: env.repo }, env: lead.env }), undefined);
  const leadCaller = env.runtime.caller(lead.mcpUrl.split("/mcp/")[1]!);
  assert.ok(leadCaller && leadCaller.tools.includes("plan"));
});

test("message không chen vào lượt đang chạy: giữ tới khi hết lượt, hoặc đi kèm câu trả lời tool", async () => {
  const env = await setup();
  const { secret } = await supervisor(env);
  const caller = env.runtime.caller(secret) as NonNullable<ReturnType<Runtime["caller"]>>;
  await caller.call("open_squad", { goal: "g", acceptance: ["a"], slug: "alpha" });
  await env.runtime.idle();
  const lead = env.host.created.at(-1)!;
  const leadCaller = env.runtime.caller(lead.mcpUrl.split("/mcp/")[1]!) as NonNullable<ReturnType<Runtime["caller"]>>;
  env.host.statuses.set("agent-sup", "running");
  env.runtime.turnStarted("agent-sup");
  await leadCaller.call("ask", { question: "base là main à?" });
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(env.host.sent.length, 0);
  const reply = await caller.call("status", {});
  assert.match(reply.text, /Message đang chờ:\n1 message: 1 ask:/);
  assert.match(reply.text, /Next: answer\{ask:/);
  await leadCaller.call("ask", { question: "thêm một câu" });
  env.host.statuses.set("agent-sup", "idle");
  await env.runtime.turnEnded("agent-sup", "completed", "");
  assert.equal(env.host.sent.length, 1);
  assert.equal(env.host.sent[0]!.agentId, "agent-sup");
  assert.match(env.host.sent[0]!.text, /thêm một câu/);
});

test("HC squad bị archive bên ngoài thì secret bị thu hồi", async () => {
  const env = await setup();
  const { secret } = await supervisor(env);
  env.runtime.agentArchived("agent-sup");
  assert.equal(env.runtime.caller(secret), null);
  assert.ok(env.runtime.isSquadAgent("x", "squad-claude-peer"));
  assert.ok(!env.runtime.isSquadAgent("x", "claude"));
});

test("MCP HTTP: GET 405, secret lạ 401, tools/list chỉ trả tool của HC, schema gốc luôn là object", async () => {
  const env = await setup();
  const { secret } = await supervisor(env);
  const server: http.Server = await startMcpServer(0, env.runtime, () => {});
  const port = (server.address() as { port: number }).port;
  try {
    const url = `http://127.0.0.1:${port}/mcp/${secret}`;
    assert.equal((await fetch(url)).status, 405);
    assert.equal((await fetch(`http://127.0.0.1:${port}/mcp/${"z".repeat(32)}`, { method: "POST", body: "{}" })).status, 401);
    const post = (body: unknown) => fetch(url, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) }).then((r) => r.json() as Promise<{ result: { tools: { name: string }[]; content: { text: string }[] } }>);
    const init = (await post({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-06-18" } })) as unknown as { result: { protocolVersion: string } };
    assert.equal(init.result.protocolVersion, "2025-06-18");
    const list = await post({ jsonrpc: "2.0", id: 2, method: "tools/list" });
    assert.deepEqual(list.result.tools.map((t) => t.name).sort(), ["amend_squad", "answer", "close_squad", "message", "open_squad", "status"]);
    const call = await post({ jsonrpc: "2.0", id: 3, method: "tools/call", params: { name: "status", arguments: {} } });
    assert.equal(call.result.content[0]!.text, "Chưa có squad nào.");
  } finally {
    server.close();
  }
  for (const tool of toolDescriptors(["respond", "verdict", "position", "plan", "handoff"])) {
    assert.equal(tool.inputSchema.type, "object", tool.name);
    assert.ok(!("oneOf" in tool.inputSchema) && !("anyOf" in tool.inputSchema), tool.name);
  }
  const respond = toolDescriptors(["respond"])[0]!.inputSchema as { properties: { action: { enum: string[] } } };
  assert.deepEqual(respond.properties.action.enum.sort(), ["concede", "decide", "escalate", "hold"]);
});

test("nội dung roles và peers nhúng trong code khớp với file", async () => {
  const { render } = (await import(path.join(ROOT, "scripts", "embed-content.mjs").replaceAll("\\", "/").replace(/^([A-Z]):/, "file:///$1:"))) as { render(): string };
  assert.equal(fs.readFileSync(path.join(ROOT, "server/hc/content.ts"), "utf8"), render(), "chạy node scripts/embed-content.mjs");
});

test("position: schema gửi Claude khai agree là boolean; server nhận cả \"true\" dạng chuỗi", async () => {
  const position = toolDescriptors(["position"])[0]!.inputSchema as { properties: { agree: { type: string; enum: unknown[] } } };
  assert.equal(position.properties.agree.type, "boolean");
  assert.deepEqual(position.properties.agree.enum.sort(), [false, true]);
  const { TOOL_SCHEMAS } = await import("../shared/protocol.ts");
  const parsed = TOOL_SCHEMAS.position.safeParse({ agree: "true", comparison: [{ point: "a", mine: "b", lead: "c", whyLeadIsFine: "d" }] });
  assert.equal(parsed.success, true);
  assert.equal((parsed.data as { agree: boolean }).agree, true);
});

test("khôi phục: Lead chưa kịp tạo trước khi plugin dừng thì được tạo lại sau restart với đúng message giao việc", async () => {
  const env = await setup();
  const { caller } = await supervisor(env);
  env.host.createAgent = async () => {
    throw new Error("daemon chết giữa chừng");
  };
  env.host.listAgents = async () => [];
  await caller.call("open_squad", { goal: "Hỗ trợ mã giảm giá", acceptance: ["test pass"], slug: "delta" });
  await env.runtime.idle();
  const { FakeHost } = await import("./fakes.ts");
  const { Runtime } = await import("../server/runtime.ts");
  const { realCatalog, readHarness } = await import("./helpers.ts");
  const host2 = new FakeHost();
  host2.workspaces.set(env.repo, "wks_repo");
  const ledgerFile = path.join(env.state, "workspaces", "wks_repo", "ledger.json");
  const before = JSON.parse(fs.readFileSync(ledgerFile, "utf8"));
  const lead = Object.values(before.hcs as Record<string, { role: string; state: string; id: string }>).find((s) => s.role === "lead")!;
  assert.equal(lead.state, "closed");
  before.hcs[lead.id].state = "open";
  fs.writeFileSync(ledgerFile, JSON.stringify(before));
  const runtime2 = new Runtime({ host: host2, state: env.state, port: 6999, catalog: realCatalog(), harness: readHarness(), binary: FAKE_CLAUDE, log: () => {} });
  await runtime2.connected();
  await runtime2.idle();
  const made = host2.created.find((c) => c.labels["squad.hc"] === lead.id);
  assert.ok(made, "Lead được tạo lại");
  assert.match(made.prompt, /Hỗ trợ mã giảm giá/);
});

test("khôi phục: sau restart HC cũ được nối lại theo label, secret cũ vẫn dùng được, message tồn được giao", async () => {
  const env = await setup();
  const { caller, secret } = await supervisor(env);
  await caller.call("open_squad", { goal: "g", acceptance: ["a"], slug: "alpha" });
  await env.runtime.idle();
  const leadAgent = env.host.created.at(-1)!;
  const leadCaller = env.runtime.caller(leadAgent.mcpUrl.split("/mcp/")[1]!) as NonNullable<ReturnType<Runtime["caller"]>>;
  env.host.statuses.set("agent-sup", "running");
  env.runtime.turnStarted("agent-sup");
  await leadCaller.call("ask", { question: "câu hỏi trước khi restart" });
  const { FakeHost } = await import("./fakes.ts");
  const { Runtime: R2 } = await import("../server/runtime.ts");
  const { realCatalog, readHarness } = await import("./helpers.ts");
  const host2 = new FakeHost();
  host2.workspaces.set(env.repo, "wks_repo");
  host2.created = [{ ...leadAgent }];
  host2.statuses.set(leadAgent.id, "idle");
  host2.statuses.set("agent-sup", "idle");
  host2.listAgents = async () => [
    { id: leadAgent.id, labels: leadAgent.labels, status: "idle", workspaceId: "wks_repo", archived: false },
    { id: "agent-sup", labels: {}, status: "idle", workspaceId: "wks_repo", archived: false },
  ];
  const runtime2 = new R2({ host: host2, state: env.state, port: 6999, catalog: realCatalog(), harness: readHarness(), binary: FAKE_CLAUDE, log: () => {} });
  const early = runtime2.caller(secret);
  assert.ok(early, "trước khi có API vẫn liệt kê được tool, để Claude không bỏ server");
  assert.ok(early.tools.includes("status"));
  const pendingCall = early.call("status", {});
  await runtime2.connected();
  await runtime2.idle();
  assert.equal((await pendingCall).isError, false, "lời gọi đến sớm chờ squad sẵn sàng rồi chạy");
  const again = runtime2.caller(secret);
  assert.ok(again);
  assert.equal((await again.call("status", {})).isError, false);
  assert.ok(host2.sent.some((s) => s.agentId === "agent-sup" && s.text.includes("câu hỏi trước khi restart")));
  assert.equal(host2.created.length, 1);
});

test("restart: agent cùng mã HC của workspace khác không bị nhận nhầm", async () => {
  const env = await setup();
  const { caller } = await supervisor(env);
  await caller.call("open_squad", { goal: "g", acceptance: ["a"], slug: "alpha" });
  await env.runtime.idle();
  const lead = env.host.created.at(-1)!;
  const { FakeHost } = await import("./fakes.ts");
  const { Runtime: R2 } = await import("../server/runtime.ts");
  const { realCatalog, readHarness } = await import("./helpers.ts");
  const host2 = new FakeHost();
  host2.workspaces.set(env.repo, "wks_repo");
  const foreign = { "squad.ws": "wks_other_repo", "squad.hc": lead.labels["squad.hc"]!, "squad.role": "lead" };
  host2.listAgents = async () => [
    { id: "agent-other-repo", labels: foreign, status: "idle", workspaceId: "wks_other", archived: false },
    { id: lead.id, labels: lead.labels, status: "idle", workspaceId: "wks_repo", archived: false },
  ];
  const runtime2 = new R2({ host: host2, state: env.state, port: 6999, catalog: realCatalog(), harness: readHarness(), binary: FAKE_CLAUDE, log: () => {} });
  await runtime2.connected();
  await runtime2.idle();
  const ledger = JSON.parse(fs.readFileSync(path.join(env.state, "workspaces", "wks_repo", "ledger.json"), "utf8"));
  assert.equal(ledger.hcs[lead.labels["squad.hc"]!].agentId, lead.id);
  assert.notEqual(runtime2.caller(lead.mcpUrl.split("/mcp/")[1]!), null);
});

test("restart: agent đang nghỉ không có trong danh sách của Paseo thì vẫn giữ HC; chỉ agent đã mất thật mới bị coi là mất", async () => {
  const env = await setup();
  const { caller, secret } = await supervisor(env);
  await caller.call("open_squad", { goal: "g", acceptance: ["a"], slug: "alpha" });
  await env.runtime.idle();
  const lead = env.host.created.at(-1)!;
  const { FakeHost } = await import("./fakes.ts");
  const { Runtime: R2 } = await import("../server/runtime.ts");
  const { realCatalog, readHarness } = await import("./helpers.ts");
  const host2 = new FakeHost();
  host2.workspaces.set(env.repo, "wks_repo");
  host2.listAgents = async () => [];
  host2.gone.add(lead.id);
  const runtime2 = new R2({ host: host2, state: env.state, port: 6999, catalog: realCatalog(), harness: readHarness(), binary: FAKE_CLAUDE, log: () => {} });
  await runtime2.connected();
  await runtime2.idle();
  const sup = runtime2.caller(secret);
  assert.ok(sup, "Supervisor đang nghỉ vẫn giữ secret");
  assert.equal(runtime2.caller(lead.mcpUrl.split("/mcp/")[1]!), null, "Lead đã bị archive thật thì mất HC");
  assert.equal((await sup.call("status", {})).isError, false);
});
