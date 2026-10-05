import http from "node:http";
import { toolDescriptors } from "./tools.ts";

export interface McpCaller {
  /** Tool names this caller may use. */
  tools: string[];
  call(tool: string, args: unknown): Promise<{ text: string; isError: boolean }>;
}

export interface McpBackend {
  /** Resolves the secret in the URL to a caller, or null for an unknown or revoked secret. */
  caller(secret: string): McpCaller | null;
}

const MAX_BODY = 2 * 1024 * 1024;

function rpcResult(id: unknown, result: unknown) {
  return { jsonrpc: "2.0", id, result };
}

function rpcError(id: unknown, code: number, message: string) {
  return { jsonrpc: "2.0", id, error: { code, message } };
}

/** Handles one JSON-RPC message from an HC. Returns null for notifications. */
export async function handleRpc(backend: McpBackend, secret: string, message: { id?: unknown; method?: string; params?: { name?: string; arguments?: unknown; protocolVersion?: string } }) {
  const { id, method, params } = message;
  if (id === undefined || id === null) return null;
  if (method === "initialize") {
    return rpcResult(id, {
      protocolVersion: params?.protocolVersion ?? "2025-06-18",
      capabilities: { tools: { listChanged: false } },
      serverInfo: { name: "squad", version: "0.1.0" },
    });
  }
  if (method === "ping") return rpcResult(id, {});
  const caller = backend.caller(secret);
  if (!caller) return rpcError(id, -32001, "secret không hợp lệ hoặc đã bị thu hồi");
  if (method === "tools/list") return rpcResult(id, { tools: toolDescriptors(caller.tools) });
  if (method === "tools/call") {
    const name = params?.name ?? "";
    const outcome = await caller.call(name, params?.arguments ?? {});
    return rpcResult(id, { content: [{ type: "text", text: outcome.text }], isError: outcome.isError });
  }
  return rpcError(id, -32601, `không hỗ trợ ${method}`);
}

/** MCP over plain HTTP POST on 127.0.0.1. The URL carries the HC's secret; it is never logged. */
export function startMcpServer(port: number, backend: McpBackend, log: (line: string) => void): Promise<http.Server> {
  const server = http.createServer((req, res) => {
    const match = /^\/mcp\/([A-Za-z0-9_-]{16,})$/.exec(req.url ?? "");
    if (!match) {
      res.writeHead(404).end();
      return;
    }
    if (req.method !== "POST") {
      res.writeHead(405, { allow: "POST" }).end();
      return;
    }
    const secret = match[1]!;
    if (backend.caller(secret) === null) {
      res.writeHead(401, { "content-type": "application/json" }).end(JSON.stringify(rpcError(null, -32001, "unauthorized")));
      return;
    }
    let size = 0;
    const chunks: Buffer[] = [];
    req.on("data", (chunk: Buffer) => {
      size += chunk.length;
      if (size > MAX_BODY) req.destroy();
      else chunks.push(chunk);
    });
    req.on("end", async () => {
      try {
        const body = JSON.parse(Buffer.concat(chunks).toString("utf8")) as unknown;
        const messages = Array.isArray(body) ? body : [body];
        const replies = (await Promise.all(messages.map((m) => handleRpc(backend, secret, m as never)))).filter(Boolean);
        if (!replies.length) {
          res.writeHead(202).end();
          return;
        }
        res.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify(Array.isArray(body) ? replies : replies[0]));
      } catch (error) {
        log(`mcp: yêu cầu lỗi: ${error instanceof Error ? error.message : String(error)}`);
        res.writeHead(400, { "content-type": "application/json" }).end(JSON.stringify(rpcError(null, -32700, "parse error")));
      }
    });
  });
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, "127.0.0.1", () => {
      server.off("error", reject);
      resolve(server);
    });
  });
}
