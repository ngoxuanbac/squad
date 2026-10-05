import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { LedgerStore } from "./ledger/store.ts";
import type { Ledger, Role, Hc } from "./ledger/types.ts";
import { Desk, type CallResult, type DeskEvent } from "./desk/desk.ts";
import type { DeskContext, Effect, RepoPort } from "./desk/context.ts";
import { commitAuthor, hcTitle, SUPERVISOR } from "./desk/names.ts";
import { agentOptions, kindOf, pickModel, type Catalog, type Harness } from "./hc/catalog.ts";
import { modelId, modelName, providerId, providerPatch, roleOfProvider } from "./hc/providers.ts";
import { hashSecret, newSecret } from "./hc/secrets.ts";
import type { Host } from "./host/host.ts";
import type { McpBackend, McpCaller } from "./mcp/http.ts";
import { compose, Mailbox } from "./outbox/mailbox.ts";
import { git, RealRepo, currentBranch } from "./git/repo.ts";
import { bringBranchIn, commitsBehind, findBash, lockWorktree, unlockWorktree, syncDetached, createDetachedWorktree, createWorktree, deleteBranch, fastForward, removeWorktree, revParse, runCommand } from "./git/ops.ts";
import { findRealGit, hcEnv, writeShims } from "./git/shim.ts";
import { findBinary, launchCommand, writeLauncher } from "./hc/launcher.ts";

const CI_TIMEOUT_MS = 10 * 60 * 1000;
const READY_WAIT_MS = 25_000;

import { DEFAULT_MAX_PEERS, readProject, worktreePath, writeProject, wsDir, type Project } from "./state.ts";

/**
 * Whether an agent's labels name this HC of this workspace. HC ids repeat across workspaces ("s1-lead" in every
 * repo), so the label alone would hand one repo's agent to another's ledger.
 */
function labelled(agent: { labels: Record<string, string> }, ws: string, hcId: string): boolean {
  return agent.labels["squad.ws"] === ws && agent.labels["squad.hc"] === hcId;
}

export interface RuntimeOptions {
  host: Host;
  state: string;
  port: number;
  catalog: Catalog;
  harness: Harness;
  log: (line: string) => void;
  repoFor?: (dir: string) => RepoPort;
  /** The harness binary; found on PATH when omitted. */
  binary?: string;
  /** Peers working at once per workspace, and checks runs at once on the machine. */
  limits?: { peers: number; ci: number };
}

interface Workspace {
  ws: string;
  desk: Desk;
  store: LedgerStore;
  /** Messages waiting for each HC, and what it was sent, to tell the Human's own messages apart. */
  mailbox: Mailbox;
  project: Project;
}

/** Request of Paseo's before("agent.create") hook, the part squad reads and changes. */
export interface CreateRequest {
  config: { provider: string; cwd: string; systemPrompt?: string; modeId?: string; model?: string; providerOptions?: unknown; toolPolicy?: unknown; mcpServers?: unknown; [key: string]: unknown };
  env?: Record<string, string>;
}

export class Runtime implements McpBackend {
  private readonly opts: RuntimeOptions;
  private readonly spaces = new Map<string, Workspace>();
  /** secret hash → HC, for every HC that has a secret. */
  private readonly secrets = new Map<string, { ws: string; hc: string }>();
  private readonly agents = new Map<string, { ws: string; hc: string }>();
  private readonly busy = new Set<string>();
  private ready = false;
  private becameReady: () => void = () => {};
  private readonly readiness = new Promise<void>((resolve) => (this.becameReady = resolve));
  private readonly pending = new Set<Promise<void>>();
  private binDir: string | undefined;
  private bash: string | undefined;
  /** Provider command through the HC launcher, or undefined when the harness binary was not found. */
  private launch: string[] | undefined;
  /** Marks a session squad's hook opened; the launcher refuses a squad HC without it. */
  private readonly grant = newSecret();
  /** CI runs share the machine: at most `limits.ci` at once, the rest wait their turn. */
  private readonly ci: Slots;
  /** Whether squad's MCP server listens. An HC opened without it would run with no squad tools. */
  private serving: Promise<boolean> = Promise.resolve(true);

  constructor(opts: RuntimeOptions) {
    this.opts = opts;
    this.ci = new Slots(opts.limits?.ci ?? Math.max(1, Math.floor(os.cpus().length / 2)));
    try {
      const bin = path.join(opts.state, "bin");
      const realGit = findRealGit(bin);
      writeShims(bin, process.execPath, realGit);
      this.binDir = bin;
      this.bash = findBash(realGit);
    } catch (error) {
      opts.log(`không dựng được git shim: ${error instanceof Error ? error.message : String(error)}`);
    }
    try {
      const binary = opts.binary ?? findBinary(opts.harness.binary);
      if (!binary) throw new Error(`không tìm thấy ${opts.harness.binary} trên PATH hay trong ~/.local/bin`);
      this.launch = launchCommand(writeLauncher(path.join(opts.state, "bin")), binary);
    } catch (error) {
      opts.log(`không dựng được launcher, HC squad sẽ chạy cả khi plugin tắt: ${error instanceof Error ? error.message : String(error)}`);
    }
    const root = path.join(opts.state, "workspaces");
    if (fs.existsSync(root)) {
      for (const ws of fs.readdirSync(root)) {
        try {
          this.space(ws);
        } catch (error) {
          opts.log(`workspace ${ws}: ${error instanceof Error ? error.message : String(error)}`);
        }
      }
    }
  }

  private space(ws: string): Workspace {
    const known = this.spaces.get(ws);
    if (known) return known;
    const project = readProject(this.opts.state, ws);
    if (!project) throw new Error(`workspace ${ws} chưa có project.json`);
    const store = new LedgerStore(path.join(wsDir(this.opts.state, ws), "ledger.json"));
    const ctx: DeskContext = {
      catalog: this.opts.catalog,
      repo: (this.opts.repoFor ?? ((dir) => new RealRepo(dir)))(project.repo),
      project: { base: project.base, rules: project.rules },
      limits: { peers: this.opts.limits?.peers ?? DEFAULT_MAX_PEERS },
      paths: { worktree: (_ws, slot) => worktreePath(this.opts.state, project.repo, slot) },
      now: () => new Date().toISOString(),
    };
    const dir = wsDir(this.opts.state, ws);
    const space: Workspace = { ws, desk: new Desk(store, ctx), store, mailbox: new Mailbox(path.join(dir, "mailbox.json")), project };
    this.spaces.set(ws, space);
    this.index(space, store.read());
    return space;
  }

  private index(space: Workspace, ledger: Ledger): void {
    for (const hc of Object.values(ledger.hcs)) {
      if (hc.state === "closed") continue;
      if (hc.secretHash) this.secrets.set(hc.secretHash, { ws: space.ws, hc: hc.id });
      if (hc.agentId) this.agents.set(hc.agentId, { ws: space.ws, hc: hc.id });
    }
  }

  private hc(ws: string, id: string): Hc | undefined {
    return this.space(ws).store.read().hcs[id];
  }

  // ---- Paseo API arrived -------------------------------------------------

  /** Called once Paseo's API is bound: sync providers, reattach HCs by label, deliver held mail. */
  async connected(): Promise<void> {
    const { host, harness, log } = this.opts;
    try {
      const patch = providerPatch(await host.readConfig(), harness, this.opts.catalog, this.launch);
      if (patch) {
        await host.patchConfig(patch);
        log("đã ghi 3 provider squad");
      }
    } catch (error) {
      log(`ghi provider lỗi: ${error instanceof Error ? error.message : String(error)}`);
    }
    try {
      const agents = await host.listAgents();
      for (const space of this.spaces.values()) {
        const ledger = space.store.read();
        for (const hc of Object.values(ledger.hcs)) {
          if (hc.state === "closed") continue;
          const found = agents.find((a) => !a.archived && a.id === hc.agentId) ?? agents.find((a) => !a.archived && labelled(a, space.ws, hc.id));
          if (found) {
            this.agents.set(found.id, { ws: space.ws, hc: hc.id });
            if (found.status === "running") this.busy.add(found.id);
            if (hc.agentId !== found.id) this.event(space.ws, { kind: "hc_filled", hc: hc.id, agentId: found.id });
          } else if (hc.state === "filled" && hc.agentId) {
            // A resting agent can be missing from the list while Paseo is still loading providers after a
            // reload; only Paseo saying it is archived or gone makes the HC lost.
            if (await host.agentGone(hc.agentId)) this.event(space.ws, { kind: "hc_lost", hc: hc.id });
            else this.agents.set(hc.agentId, { ws: space.ws, hc: hc.id });
          } else if (hc.state === "filled") {
            this.event(space.ws, { kind: "hc_lost", hc: hc.id });
          }
        }
      }
    } catch (error) {
      log(`nhận lại agent lỗi: ${error instanceof Error ? error.message : String(error)}`);
    }
    this.ready = true;
    this.becameReady();
    for (const space of this.spaces.values()) {
      try {
        this.track(this.run(space.ws, space.desk.recover()));
      } catch (error) {
        log(`khôi phục ${space.ws} lỗi: ${error instanceof Error ? error.message : String(error)}`);
      }
    }
    await this.tick();
  }

  // ---- hooks ---------------------------------------------------------------

  /** before("agent.create"): register the Supervisor HC, and refuse squad HCs desk did not register. */
  async beforeCreate(request: CreateRequest): Promise<CreateRequest | undefined> {
    const role = roleOfProvider(request.config.provider, this.opts.harness);
    if (!role) return undefined;
    if (role !== "supervisor") {
      const hcId = request.env?.SQUAD_HC;
      const owner = hcId ? [...this.spaces.values()].find((s) => s.store.read().hcs[hcId]) : undefined;
      const hc = owner ? owner.store.read().hcs[hcId!] : undefined;
      if (!hc || hc.role !== role || hc.state === "closed") {
        throw new Error(`HC ${role} của squad chỉ được tạo bởi squad. Hãy tạo "Squad · Supervisor" rồi nói mục tiêu với nó.`);
      }
      return undefined;
    }
    const ws = (await this.opts.host.workspaceForPath(request.config.cwd)) ?? (await this.opts.host.openWorkspace(request.config.cwd));
    if (!readProject(this.opts.state, ws)) {
      let base: string;
      try {
        base = currentBranch(request.config.cwd);
      } catch {
        throw new Error("Supervisor của squad cần chạy trong một repo git.");
      }
      writeProject(this.opts.state, ws, { repo: request.config.cwd, base, rules: [] });
    }
    const space = this.space(ws);
    const secret = newSecret();
    const result = space.desk.supervisor(ws, hashSecret(secret));
    if (!result.ok) throw new Error(result.text);
    this.secrets.set(hashSecret(secret), { ws, hc: result.hc });
    const { catalog, harness } = this.opts;
    // The provider only lists Sonnet, but a request can still name another model; it runs on the role's own.
    const [, picked] = request.config.provider.split("/");
    const kind = catalog.kinds.supervisor!;
    const model = modelId(harness, pickModel(kind, modelName(harness, request.config.model ?? picked)));
    return {
      ...request,
      config: {
        ...request.config,
        provider: providerId("supervisor", harness),
        // The Supervisor sits in the Human's own workspace, whose name squad leaves alone.
        title: request.config.title || SUPERVISOR,
        model,
        modeId: harness.modeId,
        systemPrompt: kind.prompt,
        providerOptions: agentOptions(catalog, harness, kind, this.paths()),
        toolPolicy: { preapproved: kind.squadTools.map((tool) => ({ kind: "mcp", server: "squad", tool })) },
        mcpServers: { squad: { type: "http", url: this.mcpUrl(secret), alwaysLoad: true } },
      },
    };
  }

  /** event("agent.created"): ties a new Supervisor agent to its pending hc. */
  agentCreated(agent: { id: string; provider: string; workspaceId: string | null }): void {
    if (roleOfProvider(agent.provider, this.opts.harness) !== "supervisor" || !agent.workspaceId) return;
    const space = this.spaces.get(agent.workspaceId);
    if (!space) return;
    const pending = Object.values(space.store.read().hcs).find((s) => s.role === "supervisor" && s.state === "open");
    if (!pending) return;
    this.agents.set(agent.id, { ws: space.ws, hc: pending.id });
    this.busy.add(agent.id);
    this.event(space.ws, { kind: "hc_filled", hc: pending.id, agentId: agent.id });
  }

  turnStarted(agentId: string): void {
    if (this.agents.has(agentId)) this.busy.add(agentId);
  }

  async turnEnded(agentId: string, outcome: "completed" | "failed" | "canceled", lastText: string): Promise<void> {
    this.busy.delete(agentId);
    const owner = this.agents.get(agentId);
    if (!owner) return;
    this.event(owner.ws, { kind: "turn_ended", hc: owner.hc, outcome, lastText });
    await this.deliver(owner.ws, owner.hc);
  }

  agentArchived(agentId: string): void {
    const owner = this.agents.get(agentId);
    if (!owner) return;
    this.agents.delete(agentId);
    this.busy.delete(agentId);
    const hc = this.hc(owner.ws, owner.hc);
    if (hc && hc.state !== "closed") this.event(owner.ws, { kind: "hc_lost", hc: owner.hc });
    this.revoke(owner.ws, owner.hc);
  }

  /** A squad HC never answers permission prompts itself: everything outside its tools is refused. */
  isSquadAgent(agentId: string, provider: string): boolean {
    return this.agents.has(agentId) || roleOfProvider(provider, this.opts.harness) !== null;
  }

  // ---- MCP backend --------------------------------------------------------

  /**
   * The HC behind a secret. Listing tools works at once, even while squad waits for Paseo's API after a reload:
   * a failed tools/list makes Claude drop the whole server for the session. A call waits for squad to be ready.
   */
  caller(secret: string): McpCaller | null {
    const owner = this.secrets.get(hashSecret(secret));
    if (!owner) return null;
    const hc = this.hc(owner.ws, owner.hc);
    if (!hc || hc.state === "closed" || hc.secretHash !== hashSecret(secret)) return null;
    return {
      tools: kindOf(this.opts.catalog, hc).squadTools,
      call: async (tool, args) => {
        if (!this.ready && !(await this.waitReady(READY_WAIT_MS))) {
          return { text: "squad đang khởi động và chưa nhận được Paseo API; gọi lại sau ít giây.", isError: true };
        }
        return this.callTool(owner.ws, owner.hc, tool, args);
      },
    };
  }

  async callTool(ws: string, hc: string, tool: string, args: unknown): Promise<{ text: string; isError: boolean }> {
    const space = this.space(ws);
    let result: CallResult;
    try {
      result = space.desk.call(hc, tool, args);
    } catch (error) {
      this.opts.log(`${tool} của ${hc} lỗi: ${error instanceof Error ? error.stack : String(error)}`);
      return { text: `squad gặp lỗi khi xử lý ${tool}: ${error instanceof Error ? error.message : String(error)}`, isError: true };
    }
    const text = result.ok ? result.text : [`Bị từ chối (${result.code}): ${result.text}`, ...(result.fix?.length ? ["Cách sửa:", ...result.fix.map((f) => `- ${f}`)] : [])].join("\n");
    this.track(this.run(ws, result.effects));
    const waiting = space.mailbox.take(hc);
    return { text: waiting.length ? `${text}\n\nMessage đang chờ:\n${compose(waiting)}` : text, isError: !result.ok };
  }

  // ---- effects --------------------------------------------------------------

  private event(ws: string, event: DeskEvent): void {
    const effects = this.space(ws).desk.event(event);
    this.track(this.run(ws, effects));
  }

  async run(ws: string, effects: Effect[]): Promise<void> {
    for (const effect of effects) {
      try {
        await this.apply(ws, effect);
      } catch (error) {
        this.opts.log(`${effect.kind} lỗi: ${error instanceof Error ? error.stack : String(error)}`);
      }
    }
  }

  private async apply(ws: string, effect: Effect): Promise<void> {
    const space = this.space(ws);
    switch (effect.kind) {
      case "mail":
        space.mailbox.add({ to: effect.to, topic: effect.topic, text: effect.text, next: effect.next });
        await this.deliver(ws, effect.to);
        return;
      case "create_branch":
        if (!new RealRepo(space.project.repo).revParse(effect.branch)) git(space.project.repo, ["branch", effect.branch, effect.from]);
        return;
      case "create_hc":
        await this.createHc(ws, effect.hc, effect.kickoff);
        return;
      case "archive_hc": {
        const hc = this.hc(ws, effect.hc);
        this.revoke(ws, effect.hc);
        space.mailbox.drop(effect.hc);
        if (hc?.agentId) {
          this.agents.delete(hc.agentId);
          // An agent archived earlier, by hand or by Paseo, may refuse a second archive; its workspace still goes.
          await this.opts.host.archive(hc.agentId).catch((error: unknown) => this.opts.log(`archive ${hc.agentId}: ${error instanceof Error ? error.message : String(error)}`));
        }
        if (hc?.workspace && hc.workspace !== ws) await this.opts.host.archiveWorkspace(hc.workspace);
        return;
      }
      case "open_worktree": {
        const { repo } = space.project;
        if (effect.branch) await createWorktree(repo, effect.path, effect.branch, effect.at);
        else if (fs.existsSync(effect.path)) await syncDetached(effect.path, await revParse(repo, effect.at));
        else await createDetachedWorktree(repo, effect.path, await revParse(repo, effect.at));
        await lockWorktree(repo, effect.path, `squad: the working copy of ${effect.hc}, which squad removes itself`);
        return;
      }
      case "remove_worktree":
        await this.releaseCopy(ws, effect.path);
        return;
      case "delete_branch":
        await deleteBranch(space.project.repo, effect.branch);
        return;
      case "run_ci":
        await this.runCi(ws, effect.task);
        return;
      case "fast_forward": {
        try {
          const expected = await revParse(space.project.repo, effect.branch);
          await fastForward(space.project.repo, effect.branch, effect.sha, expected);
          this.event(ws, { kind: "merged", task: effect.task });
        } catch (error) {
          this.event(ws, { kind: "fast_forward_failed", task: effect.task, reason: error instanceof Error ? error.message : String(error) });
        }
        return;
      }
      case "ci_squad":
        await this.ciSquad(ws, effect.squad);
        return;
    }
  }

  /**
   * Gives back a working copy. One still holding uncommitted work is the only copy of that work, so it stays,
   * unlocked, for the Human; the Supervisor hears where.
   */
  private async releaseCopy(ws: string, dir: string): Promise<void> {
    const space = this.space(ws);
    const ledger = space.store.read();
    const unsaved = fs.existsSync(dir) && new RealRepo(space.project.repo).dirty(dir);
    if (!unsaved) {
      await removeWorktree(space.project.repo, dir);
      return;
    }
    await unlockWorktree(space.project.repo, dir);
    this.opts.log(`working copy ${dir} còn thay đổi chưa commit, giữ lại cho Human`);
    const supervisor = Object.values(ledger.hcs).find((s) => s.role === "supervisor" && s.state !== "closed");
    if (supervisor) {
      space.mailbox.add({ to: supervisor.id, topic: `kept:${path.basename(dir)}`, text: `Working copy ${dir} còn thay đổi chưa commit nên squad giữ lại, không xóa. Human tự xem rồi xóa bằng git worktree remove.`, next: [] });
      await this.deliver(ws, supervisor.id);
    }
  }

  /** Brings the squad branch into the task, then reruns the brief's checks on the result. */
  private async runCi(ws: string, taskId: string): Promise<void> {
    const space = this.space(ws);
    const ledger = space.store.read();
    const task = ledger.tasks[taskId];
    const squad = task ? ledger.squads[task.squad] : undefined;
    if (!task || !squad || task.state !== "ci_running") return;
    const outcome = await bringBranchIn(space.project.repo, task.worktree, squad.branch);
    if ("conflict" in outcome) {
      this.event(ws, { kind: "conflict", task: taskId, files: outcome.conflict });
      return;
    }
    const commands = task.briefs[task.briefs.length - 1]!.checks;
    const results = await this.ci.run(async () => {
      const done = [];
      for (const command of commands) done.push(await runCommand(task.worktree, command, CI_TIMEOUT_MS, this.bash));
      return done;
    });
    this.event(ws, { kind: "ci_done", task: taskId, sha: outcome.sha, results });
  }

  /**
   * Reruns the checks of every merged task on the squad branch head, in a throwaway worktree. When the base
   * branch moved on since the squad started, the base is merged into that worktree first, so the run shows what
   * the Human's merge would give. The squad branch itself is not changed.
   */
  private async ciSquad(ws: string, squadId: string): Promise<void> {
    const space = this.space(ws);
    const ledger = space.store.read();
    const squad = ledger.squads[squadId];
    if (!squad) return;
    const { repo, base: baseBranch } = space.project;
    const dir = worktreePath(this.opts.state, repo, `ci-${squadId}-${Date.now()}`);
    const results: { task: string; command: string; exit: number; output: string }[] = [];
    let base: { branch: string; behind: number; conflict?: string[] } | undefined;
    try {
      const head = await revParse(repo, squad.branch);
      await createDetachedWorktree(repo, dir, head);
      const behind = new RealRepo(repo).revParse(baseBranch) ? await commitsBehind(repo, head, baseBranch) : 0;
      if (behind > 0) {
        const merged = await bringBranchIn(repo, dir, baseBranch).catch((error: unknown) => ({ conflict: [`(gộp lỗi: ${error instanceof Error ? error.message : String(error)})`] }));
        base = { branch: baseBranch, behind, ...("conflict" in merged ? { conflict: merged.conflict } : {}) };
      }
      if (!base?.conflict) await this.ci.run(async () => {
        for (const task of Object.values(ledger.tasks).filter((t) => t.squad === squadId && t.state === "merged")) {
          for (const command of task.briefs[task.briefs.length - 1]!.checks) {
            results.push({ task: task.id, ...(await runCommand(dir, command, CI_TIMEOUT_MS, this.bash)) });
          }
        }
      });
    } finally {
      await removeWorktree(repo, dir);
    }
    this.event(ws, { kind: "squad_ci_done", squad: squadId, results, base });
  }

  /** Tells the runtime whether the MCP server came up. */
  serveOn(listening: Promise<boolean>): void {
    this.serving = listening;
  }

  /** before("agent.session_open"): no grant while the MCP server is down, so the launcher refuses the hc. */
  async openSession(request: { provider: string; cwd: string; env: Record<string, string> }): Promise<Record<string, string> | undefined> {
    if (!roleOfProvider(request.provider, this.opts.harness)) return undefined;
    if (!(await this.serving)) {
      this.opts.log("MCP của squad không chạy nên HC squad không được mở; xem lỗi cổng ở trên");
      return undefined;
    }
    return this.sessionEnv(request);
  }

  /**
   * The env of a squad HC's session: the grant, the git shim first on PATH, and for a Peer its worktree and a
   * git identity of its own, so its commits read "squad s1-t2" instead of the Human's name.
   */
  sessionEnv(request: { provider: string; cwd: string; env: Record<string, string> }): Record<string, string> | undefined {
    const role = roleOfProvider(request.provider, this.opts.harness);
    if (!role) return undefined;
    const env: Record<string, string> = { ...request.env, SQUAD_GRANT: this.grant };
    if (role === "peer") {
      const { name, email } = commitAuthor(this.hcAt(request.cwd) ?? "peer");
      Object.assign(env, { GIT_AUTHOR_NAME: name, GIT_AUTHOR_EMAIL: email, GIT_COMMITTER_NAME: name, GIT_COMMITTER_EMAIL: email });
    }
    return this.binDir ? hcEnv(env, this.binDir, role === "peer" ? request.cwd : undefined) : env;
  }

  /** The Peer HC working in `cwd`, a task's worktree. */
  private hcAt(cwd: string): string | undefined {
    const norm = (p: string) => path.resolve(p).replaceAll("\\", "/").toLowerCase();
    for (const space of this.spaces.values()) {
      const ledger = space.store.read();
      const task = Object.values(ledger.tasks).find((t) => norm(t.worktree) === norm(cwd));
      if (task) return task.hc;
    }
    return undefined;
  }

  private async createHc(ws: string, hcId: string, kickoff: string): Promise<void> {
    const { host, catalog, harness } = this.opts;
    const space = this.space(ws);
    const ledger = space.store.read();
    const hc = ledger.hcs[hcId];
    if (!hc || hc.state !== "open") return;
    const squad = hc.squad ? ledger.squads[hc.squad] : undefined;
    const kind = kindOf(catalog, hc);
    const task = hc.task ? ledger.tasks[hc.task] : undefined;
    const cwd = hc.role === "lead" ? squad?.leadWorktree : task?.worktree;
    const title = hcTitle(ledger, hc);
    // The HC that called it into being is its parent, as seatworks does: Supervisor → Lead → Peer.
    const parentHc = hc.role === "lead" ? squad?.supervisor : squad?.lead;
    const parent = parentHc ? ledger.hcs[parentHc]?.agentId : undefined;
    let agentWs: string;
    try {
      if (!cwd) throw new Error("HC không có thư mục làm việc");
      const projectId = await host.workspaceProject(ws);
      if (!projectId) throw new Error(`không tìm thấy project của workspace ${ws}`);
      agentWs = await host.createWorkspace(cwd, projectId, title);
    } catch (error) {
      this.opts.log(`mở workspace cho ${hcId} lỗi: ${error instanceof Error ? error.message : String(error)}`);
      this.event(ws, { kind: "hc_lost", hc: hcId });
      return;
    }
    const secret = newSecret();
    const secretHash = hashSecret(secret);
    this.secrets.set(secretHash, { ws, hc: hcId });
    this.event(ws, { kind: "hc_secret", hc: hcId, secretHash });
    const role = hc.role as Role;
    const asked = role === "lead" ? squad?.model : task?.briefs[task.briefs.length - 1]?.model;
    const model = modelId(harness, pickModel(kind, asked));
    const labels: Record<string, string> = { "squad.ws": ws, "squad.role": role, "squad.kind": kind.name, "squad.hc": hcId };
    if (squad) labels["squad.id"] = squad.id;
    if (hc.task) labels["squad.task"] = hc.task;
    try {
      const agentId = await host.createAgent({
        ws: agentWs,
        cwd,
        provider: providerId(role, harness),
        model,
        modeId: harness.modeId,
        systemPrompt: kind.prompt,
        options: agentOptions(catalog, harness, kind, this.paths()),
        mcpUrl: this.mcpUrl(secret),
        squadTools: kind.squadTools,
        labels,
        env: { SQUAD_HC: hcId },
        title,
        parent,
        prompt: kickoff,
        idempotencyKey: `squad:${ws}:${hcId}`,
      });
      this.agents.set(agentId, { ws, hc: hcId });
      this.busy.add(agentId);
      this.event(ws, { kind: "hc_filled", hc: hcId, agentId, workspace: agentWs !== ws ? agentWs : undefined });
    } catch (error) {
      this.opts.log(`tạo HC ${hcId} lỗi: ${error instanceof Error ? error.message : String(error)}`);
      const found = (await host.listAgents().catch(() => [])).find((a) => !a.archived && labelled(a, ws, hcId));
      if (found) {
        this.agents.set(found.id, { ws, hc: hcId });
        this.event(ws, { kind: "hc_filled", hc: hcId, agentId: found.id });
        return;
      }
      this.event(ws, { kind: "hc_lost", hc: hcId });
    }
  }

  private revoke(ws: string, hcId: string): void {
    for (const [hash, owner] of this.secrets) {
      if (owner.ws === ws && owner.hc === hcId) this.secrets.delete(hash);
    }
  }

  // ---- mail delivery ---------------------------------------------------------

  /** Sends waiting mail to an HC when its agent is idle; never into a running turn. */
  async deliver(ws: string, hcId: string): Promise<void> {
    const space = this.space(ws);
    const hc = space.store.read().hcs[hcId];
    if (!hc) return;
    if (hc.state === "closed") {
      space.mailbox.drop(hcId);
      return;
    }
    if (!this.ready || hc.state !== "filled" || !hc.agentId || this.busy.has(hc.agentId)) return;
    const status = await this.opts.host.status(hc.agentId).catch(() => null);
    if (status !== "idle") {
      if (status === "running") this.busy.add(hc.agentId);
      return;
    }
    const letters = space.mailbox.take(hcId);
    if (!letters.length) return;
    try {
      this.busy.add(hc.agentId);
      const text = compose(letters);
      await this.opts.host.send(hc.agentId, text);
    } catch (error) {
      this.busy.delete(hc.agentId);
      space.mailbox.restore(letters);
      this.opts.log(`gửi message cho ${hcId} lỗi: ${error instanceof Error ? error.message : String(error)}`);
    }
  }

  /** Paseo events are not replayed, so held mail is retried on a timer. */
  async tick(): Promise<void> {
    for (const space of this.spaces.values()) {
      for (const hc of space.mailbox.recipients()) await this.deliver(space.ws, hc);
    }
  }

  // ---- helpers ---------------------------------------------------------------

  private waitReady(ms: number): Promise<boolean> {
    let timer: NodeJS.Timeout | undefined;
    const late = new Promise<boolean>((resolve) => (timer = setTimeout(() => resolve(false), ms)));
    return Promise.race([this.readiness.then(() => true), late]).finally(() => clearTimeout(timer));
  }

  private track(work: Promise<void>): void {
    this.pending.add(work);
    void work.finally(() => this.pending.delete(work));
  }

  /** Resolves once every effect started so far, and those they started, has finished. */
  async idle(): Promise<void> {
    while (this.pending.size) await Promise.all([...this.pending]);
  }

  private mcpUrl(secret: string): string {
    return `http://127.0.0.1:${this.opts.port}/mcp/${secret}`;
  }

  private paths() {
    return { state: this.opts.state, home: os.homedir() };
  }
}

/** A counting semaphore: `run` waits for a free slot, holds it while the work runs, then hands it on. */
export class Slots {
  private free: number;
  private readonly waiting: (() => void)[] = [];
  constructor(size: number) {
    this.free = size;
  }
  async run<T>(work: () => Promise<T>): Promise<T> {
    if (this.free > 0) this.free -= 1;
    else await new Promise<void>((resolve) => this.waiting.push(resolve));
    try {
      return await work();
    } finally {
      const next = this.waiting.shift();
      if (next) next();
      else this.free += 1;
    }
  }
}
