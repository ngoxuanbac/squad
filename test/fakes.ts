import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import type { AgentInfo, CreateAgent, Host } from "../server/host/host.ts";
import { Runtime } from "../server/runtime.ts";
import type { McpCaller } from "../server/mcp/http.ts";
import { realCatalog, readHarness, tempDir } from "./helpers.ts";

/** Stands in for the harness binary in provider commands; tests never start it. */
export const FAKE_CLAUDE = "C:/fake/claude.exe";

export class FakeHost implements Host {
  config: Record<string, unknown> = { providers: { codex: { enabled: false } } };
  patches: Record<string, unknown>[] = [];
  created: (CreateAgent & { id: string })[] = [];
  sent: { agentId: string; text: string }[] = [];
  archived: string[] = [];
  statuses = new Map<string, string>();
  workspaces = new Map<string, string>();
  denied: string[] = [];
  connected() {
    return true;
  }
  async readConfig() {
    return this.config;
  }
  async patchConfig(patch: Record<string, unknown>) {
    this.patches.push(patch);
    this.config = { ...this.config, providers: { ...(this.config.providers as object), ...(patch.providers as object) } };
  }
  async createAgent(input: CreateAgent) {
    const id = `agent-${this.created.length + 1}`;
    this.created.push({ ...input, id });
    this.statuses.set(id, "running");
    return id;
  }
  async send(agentId: string, text: string) {
    this.sent.push({ agentId, text });
    this.statuses.set(agentId, "running");
  }
  async archive(agentId: string) {
    this.archived.push(agentId);
  }
  async status(agentId: string) {
    return this.statuses.get(agentId) ?? null;
  }
  async listAgents(): Promise<AgentInfo[]> {
    return this.created.map((c) => ({ id: c.id, labels: c.labels, status: this.statuses.get(c.id) ?? "idle", workspaceId: c.ws, archived: this.archived.includes(c.id) }));
  }
  gone = new Set<string>();
  async agentGone(agentId: string) {
    return this.gone.has(agentId) || this.archived.includes(agentId);
  }
  async denyPermission(agentId: string) {
    this.denied.push(agentId);
  }
  async workspaceDirectory(ws: string) {
    return [...this.workspaces].find(([, id]) => id === ws)?.[0] ?? null;
  }
  async workspaceForPath(dir: string) {
    return this.workspaces.get(dir) ?? null;
  }
  createdWorkspaces: { dir: string; projectId: string; title: string; id: string }[] = [];
  archivedWorkspaces: string[] = [];
  async createWorkspace(dir: string, projectId: string, title: string) {
    if (!fs.existsSync(dir)) throw new Error(`Directory not found: ${dir}`);
    const id = `wks_task_${this.createdWorkspaces.length + 1}`;
    this.createdWorkspaces.push({ dir, projectId, title, id });
    return id;
  }
  async workspaceProject(ws: string) {
    return ws === "wks_repo" ? "prj_repo" : null;
  }
  async archiveWorkspace(ws: string) {
    this.archivedWorkspaces.push(ws);
  }
  async openWorkspace(dir: string) {
    if (!fs.existsSync(dir)) throw new Error(`Directory not found: ${dir}`);
    const id = `wks_${this.workspaces.size + 1}`;
    this.workspaces.set(dir, id);
    return id;
  }
  /** Mail and kickoffs an HC has received, newest last. */
  inbox(agentId: string): string[] {
    return [...this.created.filter((c) => c.id === agentId).map((c) => c.prompt), ...this.sent.filter((s) => s.agentId === agentId).map((s) => s.text)];
  }
}

export function git(cwd: string, ...args: string[]): string {
  return execFileSync("git", ["-c", "user.email=t@t", "-c", "user.name=t", ...args], { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
}

export function tempRepo(files: Record<string, string> = { "a.txt": "a\n" }): string {
  const dir = tempDir("repo");
  git(dir, "init", "-q", "-b", "main");
  git(dir, "config", "core.autocrlf", "false");
  for (const [name, content] of Object.entries(files)) fs.writeFileSync(path.join(dir, name), content);
  git(dir, "add", ".");
  git(dir, "commit", "-qm", "init");
  return dir;
}

export async function setup(files?: Record<string, string>) {
  const repo = tempRepo(files);
  const state = tempDir("state");
  const host = new FakeHost();
  host.workspaces.set(repo, "wks_repo");
  const logs: string[] = [];
  const runtime = new Runtime({ host, state, port: 6999, catalog: realCatalog(), harness: readHarness(), binary: FAKE_CLAUDE, log: (l) => logs.push(l) });
  await runtime.connected();
  return { repo, state, host, runtime, logs };
}

export type Env = Awaited<ReturnType<typeof setup>>;

/** Creates the Supervisor through the hook, as when the Human picks "Squad · Supervisor". */
export async function supervisor(env: Env) {
  const request = await env.runtime.beforeCreate({ config: { provider: "squad-claude-supervisor", cwd: env.repo } });
  assert.ok(request);
  env.runtime.agentCreated({ id: "agent-sup", provider: "squad-claude-supervisor", workspaceId: "wks_repo" });
  env.host.statuses.set("agent-sup", "idle");
  const url = (request.config.mcpServers as { squad: { url: string } }).squad.url;
  const secret = url.split("/mcp/")[1]!;
  return { request, secret, caller: env.runtime.caller(secret) as McpCaller };
}

/** The MCP caller of the agent desk created last for `hc` (or last overall). */
export function hcCaller(env: Env, hcPrefix?: string): { caller: McpCaller; agent: string; hc: string } {
  const made = [...env.host.created].reverse().find((c) => !hcPrefix || c.labels["squad.hc"]!.startsWith(hcPrefix))!;
  return { caller: env.runtime.caller(made.mcpUrl.split("/mcp/")[1]!) as McpCaller, agent: made.id, hc: made.labels["squad.hc"]! };
}

export async function ok(caller: McpCaller, tool: string, args: unknown): Promise<string> {
  const result = await caller.call(tool, args);
  assert.equal(result.isError, false, `${tool}: ${result.text}`);
  return result.text;
}
