# Project structure and runtime rules

Contents: Scaffold · Manifest · Requirements · Entries · Directories and imports · Runtime modules ·
Cross-platform rules · IDs · Entry cleanup and lifecycle · Hosts

## Scaffold

```bash
paseo plugin init /absolute/path/to/my-plugin   # writes a strict TS project; does NOT run npm
cd /absolute/path/to/my-plugin && npm install    # dev deps for typecheck/tests only
```

```text
my-plugin/
  paseo-plugin.json      # id + supported Paseo versions
  index.client.tsx       # runs in the Paseo app (every connected client)
  index.server.ts        # runs in a daemon subprocess
  client/greeting.tsx    # screen component
  client/web.ts          # the ONLY file allowed to touch browser APIs (gated by Platform.OS)
  server/greeting.ts     # RPC handler
  shared/greeting.ts     # RPC contract imported by both
  package.json
  tsconfig.json          # no DOM lib, on purpose
```

Paseo supplies `@getpaseo/plugin`, React, React Native, TanStack Query and Zod at runtime; they
stay in `devDependencies`. Consumers never install them.

## Manifest — `paseo-plugin.json`

```json
{
  "id": "my-plugin",
  "description": "Reviews changes before merge",
  "requirements": { "paseo": ">=0.8.0" },
  "build": [["npm", "ci", "--omit=dev"]]
}
```

| Field | Required | Behavior |
| --- | --- | --- |
| `id` | Yes | Default installation ID (CLI `--id` can override at install). |
| `description` | No | Non-empty summary under the ID in Settings → Plugins. |
| `requirements` | No* | Only key: `paseo` (npm semver range). *Effectively required for 0.8+. |
| `build` | No | Preparation argv arrays; omit unless needed (see sources-and-distribution.md). |

## Requirements

- Omitted `requirements.paseo` ⇒ treated as `<0.8.0` ⇒ rejected by 0.8+ with a migration link.
- Empty strings, invalid ranges, unknown requirement keys are rejected.
- Prereleases satisfy a range their stable core satisfies (`0.8.0-beta.1` ⊨ `>=0.8.0`).
- `init` writes `>=<current CLI version>` and pins the matching SDK. Raise the minimum when adopting
  newer APIs; add an upper bound (`>=0.8.3 <0.9.0`) when a later release is known incompatible.
- Daemon checks before install / preparation / load, and again on startup, enable, reload. A rejected
  update keeps the installed revision.
- Each app checks its own version before evaluating client code. A compatible daemon does not make
  an old app compatible. A plugin with no client entry doesn't care about app version.

| Range | Means |
| --- | --- |
| `>=0.8.0` | 0.8.0 and everything later, including future breaking releases |
| `^0.8.0` | 0.8.x only |
| `>=0.8.3 <0.9.0` | 0.8.3 through last 0.8 patch |

## Entries

| Entry | Runtime | Receives | Needed when |
| --- | --- | --- | --- |
| `index.client.tsx` | Paseo app, per client | `PluginClientContext` | Any UI, client callback, theme, attachment source |
| `index.server.ts` | Daemon subprocess | `PluginServerContext` | RPC handlers, hooks, settings persistence, providers, usage sources |

Both accept `.ts` or `.tsx`. At least one is required. Each default-exports one function that
registers contributions and returns cleanup:

```tsx
import type { PluginClientContext } from "@getpaseo/plugin/client";
export default function contribute(client: PluginClientContext) {
  client.addScreen({ id: "main", title: "My plugin", Component: Main });
  return () => {};
}
```

```ts
import type { PluginServerContext } from "@getpaseo/plugin/server";
export default function contribute(server: PluginServerContext) {
  server.handle(greetingRpc, createGreeting);
  return () => {};
}
```

## Directories and imports

| Directory | Compiled into | Put here |
| --- | --- | --- |
| `client/` | App bundle only | React, React Native, hooks, styles, surfaces, panels, callbacks |
| `server/` | Daemon bundle only | Node APIs, local resources, credentials, RPC handlers |
| `shared/` | Both | Zod RPC contracts, `defineSettings`, `defineAttachmentSource`, plain values |

Nesting inside these is fine. Compile errors:

- client bundle importing anything under `server/`, or any Node module (`node:*`);
- server bundle importing anything under `client/`, React, React Native, or client SDK entries;
- `shared/` importing Node, React, runtime-specific SDK entries or runtime-specific **types**;
- a code module at the plugin root (other than the entries).

Rules apply to type imports and transitive dependencies. Never import a component into the server
entry "just to wire it" — that registration belongs in the client entry.

## Runtime modules

**Client** (exact specifiers; anything else host-provided fails with
`Module "<name>" is not available in plugin client code`):

| Module | Use |
| --- | --- |
| `@getpaseo/plugin` | Shared data, `defineRpc`, `defineSettings`, `defineAttachmentSource`, `RpcInput`, `RpcOutput` |
| `@getpaseo/plugin/client` | Contribution contexts/types, `usePaseo`, `useRpc`, `useSettings`, `useWorkspace`, `useAgent`, `useHosts`, `getPaseoClient`, `openExternalUrl` |
| `@getpaseo/plugin/client/react-native` | `Modal`, `Icon`, `useToast`, `copyText`, `ScrollView`, `FlatList`, `TextInput`, `useRevealedText` |
| `@getpaseo/plugin/client/ui` | `SidebarRow`, `SidebarSeparator`, `Settings*` components, `ExternalLink` |
| `@tanstack/react-query`, `react`, `react/jsx-runtime`, `react-native`, `zod` | as usual |

`@getpaseo/plugin/client/host` is private — never import it. The host pins React/renderer versions.

**Server:** `@getpaseo/plugin`, `@getpaseo/plugin/server`, `@getpaseo/plugin/server/provider`,
`@getpaseo/plugin/server/acp`, `@getpaseo/plugin/server/usage` (0.11+), `zod`, plus Node APIs and
dependencies installed in the plugin directory.

## Cross-platform rules

Client code runs on iOS, Android, and web (React Native Web). "Works in my browser, crashes on the
phone" is the #1 plugin bug.

| Do | Don't |
| --- | --- |
| `View`, `Text`, `Pressable`, `ScrollView`, `TextInput` from `react-native` | `<div>`, `<span>`, `<button>`, any HTML element |
| `style` objects from `theme.colors` and `layout.compact` | `className`, CSS strings, hardcoded colors |
| `onPress` | `onClick`, `onMouseEnter`, DOM handlers |
| `Linking`, RN APIs | `window`, `document`, `localStorage`, `navigator`, `location` |
| `icon: "Scan"` (Lucide name) / `<Icon name=… />` | importing `lucide-react-native`, `react-native-svg`, DOM libs, bottom-sheet libs |

- Do not add `"DOM"` to `tsconfig` `lib` or `/// <reference lib="dom" />`.
- Browser-only code lives in `client/web.ts`, gated on `Platform.OS === "web"` with a native fallback.
- `layout.platform` equals `Platform.OS` (`ios` | `android` | `web`).

## IDs

- Plugin, screen, sidebar item, workspace panel, Command Center item, attachment source, slash
  command: `^[a-z][a-z0-9-]*$`.
- Header-button / composer-pill / menu-entry IDs: same format, unique per target.
- RPC names: start lowercase, then lowercase letters, digits, `.`, `-`, `_` (e.g. `issues.search`).
- Settings document IDs: `^[a-z][a-z0-9_-]*$`.

## Entry cleanup and lifecycle

- Client registrations return idempotent removers, **except** header buttons and composer pills,
  which return `{ update, remove }`.
- Entry cleanup runs first; Paseo then removes remaining registrations, unmounts surfaces, rejects
  pending RPCs, closes the plugin's daemon session and stops its subprocess — on reload, disable,
  removal, disconnect, or daemon shutdown.
- Cleanup may be async. Release timers, watchers, sockets, observations, subscriptions.
- Source edits take effect only after `paseo plugin reload <id>` (stop → cleanup → compile → start).

## Hosts

- Plugins install per daemon. The same contribution on several connected hosts shows one sidebar
  item plus a host picker; the selected host supplies bundle, Paseo API, RPC transport, query cache.
- Calls never fall through to another host when the selected host is offline.
- Attachment sources are scoped to each composer's host. Panels and Command Center items are scoped
  to the active host and exact cached context.
- Disable/removal/disconnect/evaluation failure removes Command Center items and clears the
  installation's query state. Restored panel tabs show "unavailable" until the contribution returns.
- Client contributions run on every client of that daemon, including mobile.
