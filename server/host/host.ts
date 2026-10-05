export interface CreateAgent {
  ws: string;
  cwd?: string;
  provider: string;
  model?: string;
  modeId: string;
  systemPrompt: string;
  options: Record<string, unknown>;
  mcpUrl: string;
  squadTools: string[];
  labels: Record<string, string>;
  env: Record<string, string>;
  title: string;
  /** Agent that started this one; Paseo shows the HCs as a tree and keeps attention on the root. */
  parent?: string;
  prompt: string;
  idempotencyKey: string;
}

export interface AgentInfo {
  id: string;
  labels: Record<string, string>;
  status: string;
  workspaceId: string | null;
  archived: boolean;
}

/** Everything the plugin asks of Paseo. Paseo's API arrives with the first hook or event. */
export interface Host {
  connected(): boolean;
  readConfig(): Promise<Record<string, unknown>>;
  patchConfig(patch: Record<string, unknown>): Promise<void>;
  createAgent(input: CreateAgent): Promise<string>;
  send(agentId: string, text: string): Promise<void>;
  archive(agentId: string): Promise<void>;
  status(agentId: string): Promise<string | null>;
  listAgents(): Promise<AgentInfo[]>;
  /** True only when Paseo says the agent is archived or does not exist; any doubt answers false. */
  agentGone(agentId: string): Promise<boolean>;
  denyPermission(agentId: string, requestId: string, message: string): Promise<void>;
  workspaceDirectory(ws: string): Promise<string | null>;
  workspaceForPath(directory: string): Promise<string | null>;
  openWorkspace(directory: string): Promise<string>;
  /** Opens `directory` as a workspace of an existing project, so no new project appears in Paseo. */
  createWorkspace(directory: string, projectId: string, title: string): Promise<string>;
  workspaceProject(ws: string): Promise<string | null>;
  archiveWorkspace(ws: string): Promise<void>;
}

/** The slice of Paseo's API the adapter uses; kept narrow so SDK changes surface here only. */
interface Api {
  config: { get(): Promise<{ config: Record<string, unknown> }>; patch(p: unknown): Promise<unknown> };
  workspaces: {
    list(o?: unknown): Promise<{ entries: { id: string; projectId?: string | null; directory?: string | null; archivingAt?: string | null }[] }>;
    create(o: unknown): Promise<{ id: string }>;
    archive(id: string): Promise<unknown>;
    ref(id: string): { agents: { create(o: unknown): Promise<{ id: string }> }; refresh(): Promise<{ directory?: string | null } | null> };
    open(path: string): Promise<{ id: string }>;
  };
  agents: {
    list(o?: unknown): Promise<{ entries: { agent: { id: string; labels?: Record<string, string>; status: string; workspaceId?: string | null; archivedAt?: string | null } }[] }>;
    ref(id: string): {
      send(text: string): Promise<void>;
      archive(): Promise<unknown>;
      refresh(): Promise<{ agent: { status: string } } | null>;
      respondToPermission(o: unknown): Promise<void>;
    };
  };
}

function sameDir(a: string, b: string): boolean {
  const norm = (p: string) => p.replaceAll("\\", "/").replace(/\/+$/, "").toLowerCase();
  return norm(a) === norm(b);
}

export class PaseoHost implements Host {
  private api: Api | undefined;
  private arrived: () => void = () => {};
  readonly arrival = new Promise<void>((resolve) => (this.arrived = resolve));

  bind(api: unknown): void {
    if (this.api) return;
    this.api = api as Api;
    this.arrived();
  }

  connected(): boolean {
    return this.api !== undefined;
  }

  private reach(): Api {
    if (!this.api) throw new Error("squad đang khởi động: chưa có Paseo API");
    return this.api;
  }

  async readConfig() {
    return (await this.reach().config.get()).config;
  }

  async patchConfig(patch: Record<string, unknown>) {
    await this.reach().config.patch(patch);
  }

  async createAgent(input: CreateAgent): Promise<string> {
    const handle = await this.reach().workspaces.ref(input.ws).agents.create({
      idempotencyKey: input.idempotencyKey,
      title: input.title,
      ...(input.parent ? { parent: input.parent } : {}),
      labels: input.labels,
      env: input.env,
      config: {
        provider: input.model ? `${input.provider}/${input.model}` : input.provider,
        modeId: input.modeId,
        systemPrompt: input.systemPrompt,
        mcpServers: { squad: { type: "http", url: input.mcpUrl, alwaysLoad: true } },
        toolPolicy: { preapproved: input.squadTools.map((tool) => ({ kind: "mcp", server: "squad", tool })) },
        options: input.options,
      },
      prompt: input.prompt,
    });
    return handle.id;
  }

  async send(agentId: string, text: string) {
    await this.reach().agents.ref(agentId).send(text);
  }

  async archive(agentId: string) {
    await this.reach().agents.ref(agentId).archive();
  }

  async status(agentId: string) {
    const fresh = await this.reach().agents.ref(agentId).refresh();
    return fresh?.agent.status ?? null;
  }

  async listAgents(): Promise<AgentInfo[]> {
    const result = await this.reach().agents.list({});
    return result.entries.map(({ agent }) => ({
      id: agent.id,
      labels: agent.labels ?? {},
      status: agent.status,
      workspaceId: agent.workspaceId ?? null,
      archived: !!agent.archivedAt,
    }));
  }

  async agentGone(agentId: string) {
    try {
      const fresh = (await this.reach().agents.ref(agentId).refresh()) as { agent?: { archivedAt?: string | null } } | null;
      if (fresh === null) return true;
      return !!fresh.agent?.archivedAt;
    } catch (error) {
      return /not found|unknown agent/i.test(error instanceof Error ? error.message : String(error));
    }
  }

  async denyPermission(agentId: string, requestId: string, message: string) {
    await this.reach().agents.ref(agentId).respondToPermission({ requestId, response: { behavior: "deny", message } });
  }

  async workspaceDirectory(ws: string) {
    const entry = (await this.reach().workspaces.list({})).entries.find((w) => w.id === ws);
    return entry?.directory ?? null;
  }

  async workspaceForPath(directory: string) {
    const entries = (await this.reach().workspaces.list({})).entries;
    return entries.find((w) => w.directory && !w.archivingAt && sameDir(w.directory, directory))?.id ?? null;
  }

  async openWorkspace(directory: string) {
    return (await this.reach().workspaces.open(directory)).id;
  }

  async createWorkspace(directory: string, projectId: string, title: string) {
    return (await this.reach().workspaces.create({ source: { kind: "directory", path: directory, projectId }, title })).id;
  }

  async workspaceProject(ws: string) {
    const entry = (await this.reach().workspaces.list({})).entries.find((w) => w.id === ws);
    return entry?.projectId ?? null;
  }

  async archiveWorkspace(ws: string) {
    await this.reach().workspaces.archive(ws);
  }
}
