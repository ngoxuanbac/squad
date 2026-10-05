# Provider plugins and usage sources (server)

Contents: Choose a path · Direct provider · Catalog · Session open · First prompt · Composer
controls · Persistence and replay · Commands and steering · Timeline items · ACP adapter · Icon
rules · Test checklist · Usage sources

## Choose a path

| Agent | Path |
| --- | --- |
| Already speaks ACP | `runAcpProvider()` + narrow vendor `transformers` only |
| Has TS SDK / JSON-RPC / custom process protocol | Implement `ProviderRegistration` directly |

Examples: `provider-direct`, `provider-acp-transformer`, `inline-thinking` (renderer only) in
https://github.com/getpaseo/paseo/tree/main/plugin-examples.

## Direct provider

```ts
// index.server.ts
server.registerProvider(createProvider());

// server/provider.ts
import { negotiateProviderCapabilities, type ProviderRegistration } from "@getpaseo/plugin/server/provider";
const supported = ["prompt.message"] as const;
export function createProvider(): ProviderRegistration {
  return {
    id: "my-agent", label: "My agent", icon: "icon.svg",
    async connect(request) {
      return createConnection(negotiateProviderCapabilities(request.capabilities, supported));
    },
  };
}
```

Connection = three operations:

- `send(input)` — accept work and return; **results are events**, not return values.
- `onEvent(listener)` — publish session state and **complete** timeline snapshots.
- `close()` — stop every session, release processes, subscriptions, pending work.

Keep the native SDK/process/stream inside the connection; convert output to `ProviderEvent`s.

## Catalog

Paseo asks for `catalog` before creating a session:

```ts
if (input.type === "catalog") emit({ type: "catalog", requestId: input.requestId, catalog: {
  models: [{ id: "agent-large", label: "Agent Large" }], modes: [{ id: "build", label: "Build" }],
  thinkingOptions: [{ id: "standard", label: "Standard" }],
  defaultModel: "agent-large", defaultMode: "build", defaultThinkingOption: "standard",
} });
```

Empty arrays for categories you don't expose. Session-specific controls come later via `session.config`.

## Session open

`session.open` carries the full launch config: cwd, env, system prompt, MCP servers (host tools
arrive as MCP servers), tool policy, model, mode, settings, opaque `providerOptions` (configured
defaults + per-agent overrides — validate yourself), persistence preference. Read plugin settings
via `server.registerSettings()` handle (`read()` / `subscribe()`).

```ts
emit({ type: "session.opened", requestId, sessionId, capabilities: ["prompt.message"], restoration: "core", cwd });
emit({ type: "session.config", sessionId, config: { model, mode, thinkingOption, models, modes, thinkingOptions, settings: [] } });
emit({ type: "session.ready", requestId, sessionId });
```

`session.config` = values the native agent **actually committed**, not a copy of the request.

## First prompt

All input arrives as `session.prompt`. Advertise `prompt.message` first:

```ts
emit({ type: "timeline.item", sessionId, item: { type: "user_message", id: `user:${turnId}`, text, clientMessageId: prompt.clientMessageId } });
emit({ type: "session.prompt_result", sessionId, clientMessageId: prompt.clientMessageId, result: { type: "turn", turnId } });
emit({ type: "session.turn", sessionId, turnId, state: "started" });
emit({ type: "timeline.item", sessionId, item: { type: "assistant_message", id: `assistant:${turnId}`, text: completeText } });
emit({ type: "session.turn", sessionId, turnId, state: "completed" }); // or failed / canceled
```

- Exactly **one** `session.prompt_result` per `clientMessageId`; copy `clientMessageId` onto the
  user item so Paseo replaces its optimistic message.
- Every started turn needs one terminal `completed` / `failed` / `canceled`.

## Composer controls

Only if users must change settings after open. Publish toggle/select descriptors in
`session.config.settings`; a change arrives as `session.configure` → apply → publish committed
`session.config` **before** `request.completed`. Controls may change at runtime (e.g. after model
change). `settings` = user-facing; `providerOptions` = opaque, not rendered (see
https://paseo.sh/docs/sdk/provider-options).

## Persistence and replay

- Advertise `session.persistence` if sessions can reopen. No `session.open.persistence` → new
  native session; present → resume it. Return an opaque value in `session.opened` or
  `session.persistence`.
- `history: "replay"` → publish existing `timeline.item` snapshots before `session.ready`;
  `"skip"` → don't.
- Refresh = close + open with current config/persistence; re-read credentials, env, global config,
  MCP servers in `session.open`. No separate reload op.
- `restoration: "core"` when Paseo can reopen from persistence. Provider-owned children:
  `restoration: "parent"`, emitted as `session.opened` with `parentSessionId`, recreated with parent.

## Commands and steering

- `input.type: "message"` (text, images, attachments) or `"command"` (command + args).
- `delivery: "steer"` → add to active turn; `"auto"` → start a turn or complete a command without one.
- Publish `session.commands` to add structured composer commands; a command may complete as a side
  effect with a `completed` prompt result.
- Advertise only capabilities you implement; without `prompt.steer`, Paseo replaces the active turn.

## Timeline items

Publish complete snapshots; reuse item `id` for streamed text, running tools, todos. Built-ins:
user/assistant messages, reasoning, tools, todos, errors, notifications, compaction. For anything
else emit `{ type: "plugin", id, pluginId, kind, version, data }` and register a renderer in
`index.client.tsx` (see timeline.md).

## ACP adapter

```ts
import { runAcpProvider } from "@getpaseo/plugin/server/acp";
server.registerProvider(runAcpProvider({ id: "vendor-agent", label: "Vendor agent", icon: "icon.svg", command: ["vendor-agent", "acp"] }));
```

The shim owns process, capabilities, lifecycle, prompts, permissions, timeline conversion — don't
reimplement. `transformers` (`AcpTransformer`: discovery, configuration, notification, `toolCall`)
only for vendor differences; validate with Zod `safeParse` and return unrelated/malformed input
unchanged.

## Icon rules (providers and usage sources)

`icon` is a path relative to the plugin dir → regular SVG ≤ 64 KiB, self-contained. Rejected:
scripts, styles, `foreignObject`, event-handler attributes, `javascript:` URLs, external
`href`/`xlink:href`. Fragment refs (`#mark`) OK. Never inline SVG or URL. Include it in npm `files`.

## Test checklist

1. Install as a directory source.
2. SVG, models, modes, settings appear.
3. Create a session and complete a real prompt (real agent, not only mocks).
4. Permissions, steering, cancellation, persistence, replay (if supported).
5. Every custom timeline item on desktop and mobile widths.
6. Reload and remove while a session is active → session terminates.

## Usage sources (Paseo 0.11+)

```ts
import { hashAccountKey, unavailable } from "@getpaseo/plugin/server/usage";
const input = z.object({ path: z.string() }).strict();
server.registerUsageSource({
  id: "example-usage", label: "Example", icon: "icon.svg", input,
  discover: async () => (await findLoginStores()).map((path) => ({ key: hashAccountKey(path), input: { path } })),
  fetch: async (value) => {
    const { path } = input.parse(value);
    const login = await readLogin(path);           // re-read every time (token rotation)
    if (login.expiresAt <= Date.now())
      return unavailable({ kind: "expired", expiresAt: new Date(login.expiresAt).toISOString(), refreshedBy: "example" });
    return readQuota(login);                         // { status: "available", planLabel?, windows, balances?, details? }
  },
});
```

`UsageReport`: `available` | `{ status: "unavailable", problem }` | `{ status: "error", error }`.
`UsageProblem`: `expired {expiresAt, refreshedBy?}` | `rejected {status, refreshedBy?}` | `no_quota {detail}`.

Rules:

- `discover()` takes no args, returns every login on the machine (including expired), `[]` if none,
  independent of agent sessions/provider names, in preference order.
- Inputs **name credential stores** — never contain credentials; same for reports.
- Account `key`: 1–128 chars `[A-Za-z0-9._-]`, stable across token rotation; never a credential or
  raw email → `hashAccountKey()` or a store locator. Same key ⇒ one card; inputs tried in order
  until `available`; otherwise card carries last report.
- Never redeem refresh tokens or write credential stores. Store vanished after discovery → throw.
- `refreshedBy` = CLI name (`claude`, `codex`, `opencode`, `omp`, `pi`); Paseo writes the remedy
  sentence. `no_quota.detail` is shown verbatim.
- Window names come from provider data. Numeric durations → `windowFromReportedDuration()`; named
  periods → `windowFromUsedPct()`. Unknown → neutral name ("Rolling", "Primary limit").
- Window IDs = quota scope + period (never position/utilization/reset instant); keep them stable.
- `summary: true` selects source defaults until the user customizes pins.
- Reports cached 5 min (`forceRefresh` bypasses). Clients gate on `server_info.features.usageSources`.
