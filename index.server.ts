import type { PluginServerContext } from "@getpaseo/plugin/server";
import type http from "node:http";
import { loadCatalog, loadHarness } from "./server/hc/catalog.ts";
import { CONTENT } from "./server/hc/content.ts";
import { PaseoHost } from "./server/host/host.ts";
import { startMcpServer } from "./server/mcp/http.ts";
import { Runtime, type CreateRequest } from "./server/runtime.ts";
import { readConfig, stateDir } from "./server/state.ts";

const RESCAN_MS = 30_000;

function lastAssistantText(timeline: readonly unknown[]): string {
  for (let i = timeline.length - 1; i >= 0; i -= 1) {
    const item = timeline[i] as { type?: string; text?: unknown };
    if (item?.type === "assistant_message" && typeof item.text === "string") return item.text;
  }
  return "";
}

export default function contribute(server: PluginServerContext) {
  const log = (line: string) => console.log(`[squad] ${line}`);
  const harness = loadHarness(CONTENT, "claude");
  const catalog = loadCatalog(CONTENT, harness);
  const state = stateDir();
  const { port, maxPeers, ciAtOnce } = readConfig(state);
  const host = new PaseoHost();
  const runtime = new Runtime({ host, state, port, catalog, harness, log, limits: { peers: maxPeers, ci: ciAtOnce } });

  let connecting: Promise<void> | undefined;
  const bind = (api: unknown) => {
    host.bind(api);
    connecting ??= runtime.connected();
    return connecting;
  };

  let mcp: http.Server | undefined;
  runtime.serveOn(
    startMcpServer(port, runtime, log)
      .then((s) => {
        mcp = s;
        log(`MCP trên 127.0.0.1:${port}`);
        return true;
      })
      .catch((error: Error) => {
        log(`không mở được cổng ${port}: ${error.message}. Đổi port trong ${state}/config.json rồi reload plugin.`);
        return false;
      }),
  );

  server.before("agent.create", async ({ request }, ctx) => {
    await bind(ctx.paseo);
    return (await runtime.beforeCreate(request as unknown as CreateRequest)) as typeof request | undefined;
  });
  server.before("agent.session_open", async ({ request }, ctx) => {
    void bind(ctx.paseo);
    const env = await runtime.openSession(request);
    return env ? { ...request, env } : undefined;
  });
  server.on("agent.created", ({ agent }, ctx) => {
    void bind(ctx.paseo);
    runtime.agentCreated(agent);
  });
  server.on("agent.turn_started", ({ agent }, ctx) => {
    void bind(ctx.paseo);
    runtime.turnStarted(agent.id);
  });
  server.on("agent.turn_ended", async ({ agent, outcome, timeline }, ctx) => {
    await bind(ctx.paseo);
    await runtime.turnEnded(agent.id, outcome.kind, lastAssistantText(timeline));
  });
  server.on("agent.archived", ({ agent }, ctx) => {
    void bind(ctx.paseo);
    runtime.agentArchived(agent.id);
  });
  server.on("agent.permission_requested", async ({ agent, request }, ctx) => {
    await bind(ctx.paseo);
    if (!runtime.isSquadAgent(agent.id, agent.provider)) return;
    await host.denyPermission(agent.id, request.id, "HC squad chỉ dùng tool được cấp. Cần người khác quyết thì dùng tool ask của squad.");
  });

  const timer = setInterval(() => {
    if (host.connected()) void runtime.tick();
  }, RESCAN_MS);

  return () => {
    clearInterval(timer);
    mcp?.close();
  };
}
