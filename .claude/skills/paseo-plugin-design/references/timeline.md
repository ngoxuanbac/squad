# Timeline items

Three producers, one renderer contract: a **client transformer** rewrites existing rows, the
**daemon** appends plugin-owned rows, a **provider** emits plugin items. All render through
`addTimelineRenderer` keyed by `kind` + `version`.

## Transformer + renderer (client)

```tsx
import type { PluginClientContext, PluginTimelineItemProps } from "@getpaseo/plugin/client";
import { Text } from "react-native";
import { z } from "zod";

const schema = z.object({ label: z.string() });

function Card({ item, theme }: PluginTimelineItemProps<z.output<typeof schema>>) {
  return <Text style={{ color: theme.colors.foreground }}>{item.data.label}</Text>;
}

export default function contribute(client: PluginClientContext) {
  client.addTimelineTransformer({
    id: "command-card",
    query: { itemType: "tool_call" },        // coarse, stable selector
    transform({ item, phase }) {             // phase: "streaming" | "complete"
      return { items: [{ type: "plugin", kind: "command-card", version: 1, data: { label: item.name, phase } }] };
    },
  });
  client.addTimelineRenderer({ kind: "command-card", version: 1, schema, Component: Card });
  return () => {};
}
```

Transformer rules:

- Return `undefined` → keep original; `items: [...]` → replace; `items: []` → remove.
- Inspect the item inside `transform` for provider/tool-specific recognition.
- `data` must be JSON-compatible. Optional plugin-local `id` per replacement (default: index).
- Runs on every live streaming update, **before** Markdown splitting and Overview tool grouping.
  Assistant/reasoning callbacks get the full accumulated text each time. A claimed assistant
  message stays one source item; return `undefined` until it's recognizable.
- Keep it **synchronous and deterministic**. Results are memoized by source-item reference;
  replacement identity derives from the source row, so streaming updates don't remount.

Renderer: receives `agentId`, `item`, `timestamp`, `theme`, `host`, `layout`. Paseo validates
`item.data` with `schema` before rendering. Use `useRevealedText(text, phase)` to pace streaming
text like built-in assistant rows. Missing renderer → Paseo shows its "unavailable" row.

## Append from the daemon (server)

```ts
import type { PluginHandlerContext } from "@getpaseo/plugin/server";

async function publishReview(agentId: string, { paseo }: PluginHandlerContext) {
  await paseo.agents.ref(agentId).timeline.append({
    type: "plugin", id: "review", kind: "review-result", version: 1, data: { verdict: "ready" },
  });
}
```

| Field | Rule |
| --- | --- |
| `type` | `"plugin"` |
| `id` | Stable plugin-local identity; reusing it **replaces** the row (latest value kept) |
| `kind` | Selects the renderer |
| `version` | Positive integer; renderer contract version |
| `data` | JSON, ≤ 64 KiB serialized (rejected, never truncated) |

- Daemon stamps `pluginId`; rejected from non-plugin sessions. Row appears live and survives
  refetches. Feature flag: `server_info.features.pluginTimelineItems`.

## Provider-emitted items

Providers emit `{ type: "plugin", id, pluginId, kind, version, data }` via `timeline.item`; the
client entry registers the renderer independently (see providers-and-usage.md). A renderer-only
plugin (no provider) is valid — see `plugin-examples/inline-thinking`.

## Versioning advice

Bump `version` when the `data` shape changes incompatibly and keep registering the old renderer
until old rows are gone; appended rows persist in canonical history.
