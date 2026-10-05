# Settings screens and persisted values

Contents: Settings screen · UI components · Persisted documents · Client hook states · Server handle ·
Save patterns · Guarantees

## Settings screen

```tsx
client.addSettingsScreen({ id: "display", title: "Display", icon: "SlidersHorizontal", Component: DisplaySettings });
client.openSettings("display"); // also available as openSettings(id) in Command Center callbacks
```

- Appears under **Settings → Plugins → your plugin** on that host. `id` unique per installation.
- Component receives `PluginSurfaceProps`. Paseo owns header, back, safe areas, scrolling, centered
  column; compact pushes a full-screen detail. Disabled/removed plugin → unavailable screen with Back.
- Each installation (per host) has its own values and route.

## UI components (`@getpaseo/plugin/client/ui`)

Work with your own state or RPCs; no form wrapper required.

| Component | Props |
| --- | --- |
| `SettingsSection` / `SettingsGroup` | `title`, `children`; opt `info`, `trailing`, `testID` |
| `SettingsCard` | `children`, `testID`; draws dividers between direct children (stable keys!) |
| `SettingsRow` | `label`; opt `hint`, `error`, `children`, `testID` — wrap any custom control |
| `SettingsSwitch` | row props + `value: boolean`, `onValueChange`, opt `disabled` |
| `SettingsSelect` | row props + string `value`, `options: {label,value}[]`, `onValueChange`, opt `disabled` |
| `SettingsInput` | row props + `onChangeText`; opt `initialValue`, `placeholder`, `disabled`, `secureTextEntry`, `ref` (`focus`, `blur`, `getText`, `replaceText`) |
| `SettingsAction` | row props + `actionLabel`, `onPress`, opt `disabled` |

`SettingsInput` owns in-progress text; keep drafts outside persisted values until Save.

## Persisted documents

Define in `shared/`:

```ts
import { defineSettings } from "@getpaseo/plugin";
import { z } from "zod";

export const preferences = defineSettings({
  id: "display",          // ^[a-z][a-z0-9_-]*$
  scope: "host",          // required; the only scope — shared by all authorized clients of the host
  version: 1,             // schema version, independent of write revision
  schema: z.object({ showMetadata: z.boolean().default(true) }), // defaults so parse({}) is complete
  // migrate: async (values, fromVersion) => newValues, // optional; output must pass schema
});
```

No per-user, device-local, or cross-host sync.

## Client hook states — `useSettings(preferences)`

| `status` | Data |
| --- | --- |
| `loading` | Pending; don't render defaults as if saved |
| `ready` | Typed `values` + opaque `revision` |
| `invalid` | `error` + `revision`; stored data preserved |
| `error` | Read/connection `error` |

Every state also exposes `saving`, `saveError`, and:

- `save(values, revision): Promise<boolean>` — full document; returns `false` + sets `saveError` on
  validation/conflict/transport failure (never throws).
- `reset(): Promise<boolean>` — replace with schema defaults (recovers invalid data).
- `reload(): Promise<void>` — clear error and re-read; does not discard your draft.

## Server handle

Register in `index.server.ts` only when server code needs the values (or a provider reads them):

```ts
const settings = server.registerSettings(preferences);
const current = await settings.read();          // same ready/invalid shape + revision
const stop = settings.subscribe((next) => {     // fires after successful save/reset/migration
  if (next.status === "ready") console.log("Settings changed", next.revision);
});
```

A screen using only its own data can stay client-only.

## Save patterns

- **Immediate toggle:** `state.save({ ...state.values, showMetadata }, state.revision)`.
- **Draft editor:** capture `values` *and* `revision` when opening; keep that revision until save
  succeeds or user discards. A stale revision rejects the save, preserving draft and newer values.

## Guarantees

- Writes are atomic and validated on the host; connected clients update live.
- Survive daemon restart, reload, disable, update. **Removal deletes them**; reinstall starts from defaults.
- Missing doc → defaults. Invalid data / failed migration / newer unsupported version → `invalid`
  (never silently reset). Successful migration persists once.
- Plain host-side JSON — **not a credential vault**. Uses the `daemon.manage` permission.
- Example: `plugin-examples/settings` (immediate controls, validated draft editor, custom content,
  Command Center navigation).
