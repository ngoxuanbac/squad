---
name: paseo-plugin-design
description: Design, scaffold, implement, review, debug, migrate, install, and publish Paseo plugins (paseo-plugin.json, index.client.tsx / index.server.ts, client/ server/ shared/ boundaries, @getpaseo/plugin SDK). Covers screens, sidebar items, workspace and agent panels, Command Center items, slash commands, header buttons, composer pills, timeline transformers and renderers, themes, settings, attachment sources, RPC handlers, lifecycle hooks, provider and usage-source plugins, and plugin sources (directory, GitHub, Git, npm, subdirectory). Use whenever the user mentions Paseo plugins or @getpaseo/plugin, wants to extend the Paseo app or daemon, or says things like "design a Paseo plugin", "build a plugin for Paseo", "install a plugin from GitHub/npm", or "my plugin won't load".
---

# Paseo plugin design

Built from the Paseo plugin docs (quickstart, reference, publishing, providers, migration) as of
2026-10. Targets the 0.8+ runtime-entry format; usage sources need 0.11. If something here
conflicts with the live docs, the docs win. Fetch raw markdown with
`curl -sL https://paseo.sh/docs/plugins/reference.md` (also `plugins.md`, `plugins/publishing.md`,
`plugins/providers.md`, `plugins/migration.md`).

## Mental model

- A plugin is a TypeScript project installed into **one daemon**. It is trusted and unsandboxed.
- It has two runtimes, and each is optional (at least one is required):
  - `index.client.tsx` runs in **every app connected to that daemon**: desktop, web, iOS, and
    Android. It uses React Native.
  - `index.server.ts` runs in a **daemon subprocess** with Node and full machine access. If you
    omit it, no subprocess starts.
- **The directory is the compile boundary.** `client/` code goes into the app bundle, `server/`
  code into the daemon bundle, and `shared/` into both. A code module at the root is an error.
- Paseo owns the chrome: routes, headers, sheets, scrolling, error boundaries, query cache, and
  connections. The plugin owns only the body it renders, or the handler it runs.
- For ordinary Paseo operations (workspaces, agents, terminals, config), call the SDK through
  `usePaseo()` or `ctx.paseo`. Add a plugin RPC only for work Paseo can't do: vendor APIs,
  daemon-local files, and credentials.

## Workflow

1. **Capture intent.** Fill in the brief in [assets/design-brief.md](assets/design-brief.md). Ask
   the user only about things that change the design: which surface, which host, whether secrets
   are involved, and how it will be distributed.
2. **Map each need to the smallest contribution** using the table below.
3. **Decide the runtime split.** You need `index.server.ts` only for RPC handlers, lifecycle
   hooks, settings persistence read by the server, providers, usage sources, or timeline appends.
   Otherwise ship a client-only plugin.
4. **Lay out files, IDs, and contracts.** Put RPC, settings, and attachment-source definitions in
   `shared/`. List every ID, then check it against the ID rules in rule 1.
5. **Load only the references for the chosen surfaces** (see the routing table), then implement.
   Start from `paseo plugin init /absolute/path` instead of hand-writing the scaffold.
6. **Verify:**
   `node <skill-dir>/scripts/lint-plugin.mjs <plugin-dir>` → `npm run typecheck` →
   `paseo plugin install <abs-path>` (or `reload <id>`) → `paseo plugin ls` shows `running` →
   exercise every contribution → `paseo plugin logs <id>`.
7. **Distribute** using [references/sources-and-distribution.md](references/sources-and-distribution.md).

If the user asks only for a *design*, deliver the filled brief and stop before writing code.

## Choose the contribution

| User need | Contribution (runtime) | Reference |
| --- | --- | --- |
| A full page of plugin UI | `addScreen` plus `addSidebarHeaderItem`/`addSidebarFooterItem` (client) | ui-surfaces |
| A tool next to agents, terminals, and files | `addWorkspacePanel` with `context: "workspace" \| "agent"` (client) | ui-surfaces |
| A keyboard-driven action (⌘K / Ctrl+K) | `addCommandCenterItem`, context `global` / `workspace` / `agent` (client) | ui-surfaces |
| `/name args` typed in the composer, never sent to the agent | `addSlashCommand` (client) | ui-surfaces |
| A button on the workspace header | `addHeaderButton` → `{ update, remove }` (client) | ui-surfaces |
| Per-agent status or action beside the composer | `addComposerPill` → `{ update, remove }` (client) | ui-surfaces |
| User-editable configuration | `addSettingsScreen` + `defineSettings` (+ `server.registerSettings`) | settings |
| A new look | `addTheme` (client, data only) | ui-kit-and-theming |
| Restyling or replacing agent timeline rows | `addTimelineTransformer` + `addTimelineRenderer` (client) | timeline |
| A plugin-owned result row added by the daemon | `paseo.agents.ref(id).timeline.append` (server) + renderer (client) | timeline |
| Attaching an external resource to a prompt | `defineAttachmentSource` + `server.handle` | backend |
| Calling a vendor API, local files, or secrets | `defineRpc` + `server.handle` + `useRpc` | backend |
| Reacting to or enforcing agent/workspace lifecycle; injecting env, MCP servers, or worktrees | `server.on` / `server.before` | backend |
| Connecting a new coding agent | `server.registerProvider` / `runAcpProvider` | providers-and-usage |
| Showing quota or usage for accounts | `server.registerUsageSource` (0.11+) | providers-and-usage |

## Non-negotiable rules

Each of these breaks loading, breaks mobile, or leaks data if ignored. The details are in the
references.

1. **Manifest.** `paseo-plugin.json` contains `id` and `requirements.paseo` (a semver range such as
   `>=0.8.0`). A missing `requirements.paseo` means `<0.8.0`, and Paseo rejects the plugin. Plugin,
   screen, sidebar, panel, Command Center, attachment-source, and slash-command IDs match
   `^[a-z][a-z0-9-]*$`. RPC names may also contain `.` and `_`.
2. **Entries.** Use exactly `index.client.ts(x)` and/or `index.server.ts(x)`. Each one
   default-exports `contribute(ctx)` and returns a cleanup function. A leftover legacy `index.ts`
   fails to load.
3. **Imports.** `client/` never imports from `server/`, and `server/` never imports from
   `client/`. `shared/` imports only shared code. Client code imports no `node:*` modules. Import
   client hooks from `@getpaseo/plugin/client`, server contexts from `@getpaseo/plugin/server`, and
   UI from `/client/react-native` or `/client/ui`. These rules apply to type imports too.
4. **Cross-platform UI.** Use React Native primitives, `onPress`, and styles built from
   `theme.colors.*` and `layout.compact`. Don't use HTML elements, `className`, `onClick`, DOM
   globals, hardcoded colors, `lucide-react-native`, or `react-native-svg`. For `icon`, pass a
   Lucide icon name as a string.
5. **Data access.** Read the current workspace or agent with
   `useWorkspace(id, selector)` / `useAgent(id, selector)`, which needs a selector. Don't call an
   RPC to discover it.
6. **Secrets.** Keep secrets in `server/` only. Never put them in settings (settings are plain
   JSON), logs (connected users can read logs), RPC outputs, or usage inputs.
7. **Cleanup.** Release timers, watchers, sockets, and subscriptions in the returned cleanup.
   Removers are idempotent.
8. **Hooks.** Every hook has a 30 s timeout, and you should pass `signal` to outbound requests.
   Before-hooks spread the request (there's no deep merge) and return `undefined` to keep it
   unchanged. Event handlers are best-effort, with no replay or retry.

## References (load on demand)

| File | Read it when |
| --- | --- |
| [project-structure.md](references/project-structure.md) | Setting up the manifest, version ranges, entries, directories, allowed modules, cross-platform rules, cleanup, or multi-host behavior |
| [ui-surfaces.md](references/ui-surfaces.md) | Building screens, sidebar items, panels, Command Center items, slash commands, header buttons, or composer pills |
| [ui-kit-and-theming.md](references/ui-kit-and-theming.md) | Using Modal, scrolling, toasts, icons, or copy/paste; applying theme tokens; contributing a theme; opening external links or the workspace browser |
| [settings.md](references/settings.md) | Building settings screens, `defineSettings`, `useSettings`, revisions, or migrations |
| [backend.md](references/backend.md) | Writing RPCs, lifecycle events and before-hooks, recipes, the SDK (`usePaseo`, hosts), attachment sources, or logging |
| [timeline.md](references/timeline.md) | Writing timeline transformers, renderers, or daemon appends |
| [providers-and-usage.md](references/providers-and-usage.md) | Building provider plugins (direct or ACP) or usage sources |
| [sources-and-distribution.md](references/sources-and-distribution.md) | Choosing source identifiers and their resolution order, the CLI, updates, `build`, or npm/Git publishing |
| [troubleshooting-and-migration.md](references/troubleshooting-and-migration.md) | Investigating load errors, a missing sidebar item, a failed reload, or migrating from the pre-0.8 `index.ts` |

## Bundled tools

- `scripts/lint-plugin.mjs <plugin-dir> [--json]` statically checks the manifest, entries, root
  modules, import boundaries, the client module allowlist, DOM/HTML usage, hardcoded colors, ID
  formats, and hook names. It needs no dependencies, and it exits 1 on errors. Run it before
  `typecheck`. It catches the boundary and mobile mistakes that typecheck alone misses.
- `assets/design-brief.md` is the design-document template for step 1.
