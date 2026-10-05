# Backend: RPC, lifecycle hooks, SDK, attachment sources, logging

Contents: SDK vs RPC · RPC contracts · Lifecycle hooks (events, before-hooks, ordering, context) ·
Recipes · Paseo SDK in the client · Multi-host · Attachment sources · Logging

## SDK vs RPC — decide first

| Work | Use |
| --- | --- |
| Create/rename workspaces, create/send to agents, terminals, providers, daemon config | Paseo SDK: `usePaseo()` (client), `{ paseo }` (server/callbacks) |
| Vendor API, daemon-local files/processes, credentials kept off the client | Plugin RPC: `defineRpc` + `server.handle` + `useRpc`/`rpc` |
| Current workspace/agent data | `useWorkspace` / `useAgent` (cached, no RPC) |

## RPC contracts

```ts
// shared/greeting.ts
import { defineRpc } from "@getpaseo/plugin";
import { z } from "zod";
export const greeting = defineRpc({
  name: "greeting.create", // lowercase start; [a-z0-9._-]
  input: z.object({ name: z.string() }),
  output: z.object({ message: z.string() }),
});

// server/greeting.ts
import type { RpcInput } from "@getpaseo/plugin";
export function createGreeting({ name }: RpcInput<typeof greeting>) {
  return { message: `Hello, ${name}` };
}

// index.server.ts
server.handle(greeting, createGreeting);

// client/greeting.tsx
const createGreeting = useRpc(greeting); // typed async fn; wrap in TanStack Query for state/caching
```

- Inputs and outputs are validated on **both** sides; a reject usually means a schema mismatch or a
  handler error (check `paseo plugin logs`).
- Handlers receive `PluginHandlerContext` with `{ paseo }` — a subprocess-owned connection that
  closes when the plugin stops and subscribes to nothing until you do.
- Command Center / slash command / button callbacks get `rpc(contract, input)` directly.

## Lifecycle hooks (`index.server.ts`)

Run on the daemon while the plugin is enabled, even with no app connected.

| Register | Callback | Return |
| --- | --- | --- |
| `server.on(name, cb)` | `(event, { paseo, signal })` | `void \| Promise<void>` |
| `server.before(name, cb)` | `({ request }, { paseo, signal })` | modified request, or `undefined` to keep |

### Events

| Name | Fields | Trigger |
| --- | --- | --- |
| `agent.created` | `agent` | Ordinary creation (not import/resume) |
| `agent.turn_started` | `agent`, `turnId` | Live turn starts |
| `agent.turn_ended` | `agent`, `turnId`, `outcome`, `timeline` | Completes, fails, or canceled |
| `agent.permission_requested` | `agent`, `request` | Permission/question pending |
| `agent.permission_resolved` | `agent`, `requestId`, `resolution` | Answered or cleared |
| `agent.archived` | `agent`, `archivedAt` | Archive saved |
| `workspace.created` | `workspace` | Record created; directory available |
| `workspace.archived` | `workspace` | Archive saved |

- Agent events exclude internal utility agents. Archive events may precede cleanup.
  `workspace.created` is **not** a setup barrier before agent startup.
- `turnId`: string or `null`, can repeat after reopen. `timeline`: full readonly snapshot (text can
  span items). `request.kind`: `tool | plan | question | mode | other`.
- `PluginHookAgent { id, workspaceId|null, parentAgentId|null, provider, cwd, title|null }`;
  `PluginHookWorkspace { id, projectId, cwd, name|null, archivedAt|null }`;
  `PluginTurnOutcome = {kind:"completed"} | {kind:"failed", error:{message, code?}} | {kind:"canceled", reason}`.

### Before-hooks

| Name | Request fields | Editable |
| --- | --- | --- |
| `agent.create` | `config` (`AgentSessionConfig`), optional `env` | config except `cwd`/`internal`; `env` |
| `agent.session_open` | `agentId`, `workspaceId`, `provider`, `cwd`, `reason` (`create\|resume\|refresh\|import`), `purpose` (`interactive\|history`), `env` | only `env` |
| `workspace.create` | `source`, optional `title`, `firstAgentContext` | entire request |

`AgentSessionConfig`: `provider`, `model` (changing provider may require changing model/mode/options),
`modeId`, `thinkingOptionId`, `featureValues`, `title`, `systemPrompt`, `providerOptions` (opaque),
`mcpServers`, `toolPolicy`, `cwd` (fixed), `internal` (daemon-owned).

`env` is a launch override map (not the inherited env). Replace the map to add/replace/remove.

### Ordering and returned values

```text
creation request
  → agent.create hooks (plugin-ID order; registration order within a plugin)
  → resolve defaults, validate provider config
  → derive launch config (runtime tools, daemon prompt)
  → agent.session_open hooks (same order; env only)
  → set PASEO_AGENT_ID, PASEO_AGENT_CWD → open provider session, save config
```

| Callback returns | Next callback sees |
| --- | --- |
| `{ ...request, env: { ...request.env, REGION: "eu" } }` | REGION added/replaced |
| `{ ...request, env: { REGION: "eu" } }` | whole override map replaced |
| `undefined` | unchanged |
| throws / invalid data | operation fails; later callbacks skipped |

No deep merge — always spread. Agent config is saved; env overrides are not persisted.

### Context and guarantees

| Contract | Behavior |
| --- | --- |
| `context.paseo` | SDK connected to this daemon |
| `context.signal` | Aborted on timeout or plugin stop — pass to `fetch` etc. |
| Input data | Detached snapshot; change state via returned request or SDK |
| Registration | Returns idempotent remover |
| Unknown hook name | Registration fails |
| Timeout | 30 s; before-hook fails the operation, event handler logs an error |
| Event handler error | Logged; original operation continues |
| Delivery | Live, best effort; no replay/persistence/retry |
| Concurrency | Events may overlap; completion order not guaranteed |

## Recipes

Inject MCP + Codex options:

```ts
server.before("agent.create", ({ request }) => {
  if (request.config.provider !== "codex") return request;
  return { ...request, config: { ...request.config,
    providerOptions: { ...request.config.providerOptions, sandbox_mode: "workspace-write", approval_policy: "on-request" },
    mcpServers: { ...request.config.mcpServers, company: { type: "http", url: "https://tools.example.com/mcp" } },
  } };
});
```

Env on every session open (create/resume/refresh/import; use `agent.create` for create-only):

```ts
server.before("agent.session_open", ({ request }) => ({ ...request, env: { ...request.env, COMPANY_ENV: "development" } }));
```

Force worktree isolation for explicit directory creation:

```ts
server.before("workspace.create", ({ request }) =>
  request.source.kind !== "directory" ? request
    : { ...request, source: { kind: "worktree", cwd: request.source.path, action: "branch-off" } });
```

Follow-up after a turn (copy `server/inspect.ts` from `plugin-examples/lifecycle-actions`; it needs
`@getpaseo/protocol` as a dev dependency at the SDK's version). **Add limits** — persistent matches
keep sending:

```ts
server.on("agent.turn_ended", async (event, { paseo }) => {
  if (event.outcome.kind === "canceled") return;
  if (/out of credits/i.test(latestOutputText(event.timeline))) {
    await paseo.agents.ref(event.agent.id).send("Try again.");
  }
});
```

Permission policy (the regex is an example, not a shell parser; unmatched requests stay pending
for the user; answering an already-resolved request fails):

```ts
server.on("agent.permission_requested", async (event, { paseo }) => {
  const command = shellCommand(event.request);
  if (command === null) return;
  const agent = paseo.agents.ref(event.agent.id);
  if (/\brm\s+-rf\b/.test(command)) {
    await agent.respondToPermission({ requestId: event.request.id,
      response: { behavior: "deny", message: "Recursive deletion is blocked." } });
  } else if (command.trim() === "git status") {
    await agent.respondToPermission({ requestId: event.request.id, response: { behavior: "allow" } });
  }
});
```

Examples: `lifecycle-logger` (all hooks, redacted JSON logs), `lifecycle-actions`,
`agent-configuration` in https://github.com/getpaseo/paseo/tree/main/plugin-examples.

## Paseo SDK in the client

`usePaseo()` is the plugin's single client (same as `client.paseo`) for the selected host. Never
create another client. Release subscriptions in cleanup; Paseo disposes everything on unload.

```tsx
const paseo = usePaseo();
useEffect(() => {
  const observation = paseo.observeEvents(["project.update"]);
  observation.subscribe({ snapshot() {}, update: () => setChanges((c) => c + 1) });
  return () => void observation.release();
}, [paseo]);

const ws = await paseo.workspaces.create({ title: "Review PR 42", source: { kind: "worktree",
  cwd: "/abs/repo", action: "checkout", checkoutSource: { kind: "change_request", forge: "github", number: 42 } } });
await ws.agents.create({ config: { provider: "codex/gpt-5.5" }, prompt: "Review PR #42." });
```

API covers projects, workspaces, agents, terminals, providers, daemon config (see
https://paseo.sh/docs/sdk/reference). No connection lifecycle methods — Paseo owns the connection.

## Multi-host

- `useHosts()` → `readonly { serverId, label, status: "idle"|"connecting"|"online"|"offline"|"error" }[]`
  (includes offline; no URLs/credentials).
- `getPaseoClient(serverId)` borrows that host's app connection (no new socket, plugin need not be
  installed there). Acquire at action time.
- Unknown ID → throws `Unknown Paseo host: <id>`; offline → `Paseo host is disconnected: <id>`.
  Same-connection reconnect keeps APIs alive; connection switch/failover releases them (re-acquire,
  re-subscribe). Plugin unload releases all borrowed APIs.
- `usePaseo()` follows host selection; an explicitly acquired API keeps its target.
- Example: `plugin-examples/hosts`.

## Attachment sources

Search external resources and attach a stable text snapshot to the prompt. Credentials and vendor
calls stay server-side.

```ts
// shared/issues.ts
export const searchIssues = defineRpc({
  name: "issues.search",
  input: z.object({ query: z.string() }),
  output: z.object({ items: z.array(z.object({
    id: z.string(), identifier: z.string(), title: z.string(), subtitle: z.string().optional(),
    url: z.string().url(), text: z.string(), resourceType: z.string(),
  })) }),
});
export const issues = defineAttachmentSource({
  id: "issues", title: "Acme issue", icon: "CircleDot",
  pickerTitle: "Attach Acme issue", searchPlaceholder: "Search by identifier or title",
  search: searchIssues,
});
// index.client.tsx → client.addAttachmentSource(issues);
// index.server.ts  → server.handle(searchIssues, search);
```

Paseo owns the composer menu, picker, pill, draft, submission. `text` is the complete snapshot sent
to the agent.

## Logging

- `console.log` / `console.error` in server code are captured (protocol uses a separate channel, so
  logging can't corrupt RPC). Client logs stay in the app.
- Read: Settings → Plugins → ⋯ → Logs, or `paseo plugin logs <id> [--json]`
  (`paseo --host <url> plugin logs <id>` for remote). Snapshot, not follow.
- Includes `[paseo]` loading/ready/stopping/stopped entries and compile/load failures.
- Retention: 500 entries / 256 KiB per plugin in memory, 16 KiB per line; survives reload and
  crashes; cleared on removal; daemon restart starts fresh. Also in `$PASEO_HOME/daemon.log`.
- **Never log secrets** — connected users can read the tail and the daemon log persists it.
