# Troubleshooting and migration

Contents: Debug loop · Load failures · Missing UI · Migration from pre-0.8 (`index.ts`)

## Debug loop

```bash
node <skill-dir>/scripts/lint-plugin.mjs <plugin-dir>
npm run typecheck
paseo plugin reload <id>       # or install <abs-path> the first time
paseo plugin ls                # status + error
paseo plugin logs <id>         # daemon-side output, compile/load failures, crash tail
```

Client-side `console.*` stays in the app runtime — not in plugin logs.

## Load failures

| Symptom | Fix |
| --- | --- |
| `This plugin was made for an older version of Paseo` | Only legacy `index.ts` exists → migrate (below). |
| `This plugin has no requirements.paseo` | Add `"requirements": { "paseo": ">=0.8.0" }` after migrating. |
| `Plugin "x" requires Paseo >=A. Your daemon is B.` | Use a compatible plugin revision or update the runtime. |
| `Plugin entry points are missing: expected index.client.ts(x) and/or index.server.ts(x)` | Add at least one entry with that exact name. |
| `server-only module cannot be imported into the plugin client bundle: <file>` | Move work behind an RPC; import only its contract from `shared/`. |
| `client-only module cannot be imported into the plugin server bundle: <file>` | Move that registration/import to the client entry. |
| `Plugin modules belong in client/, server/, or shared/: <file>` | Move root module into the matching directory; fix imports. |
| `Node module cannot be imported into the plugin client bundle: node:<x> imported by <file>` | Move to `server/`, expose RPC in `shared/`. |
| `Module "<name>" is not available in plugin client code` | Import only the host-provided client modules. |
| `client.addTheme is not a function` | App predates themes → update client / raise `requirements.paseo`. |
| RPC rejects | Check both Zod schemas and the handler error in logs. |
| Edited code doesn't appear | `npm run typecheck`, then `paseo plugin reload <id>`. |
| Reload fails | Read `ls` + `logs`, fix, reload again. Paseo does **not** restore the previous bundle. |
| Plugin exits unexpectedly | `paseo plugin logs <id>` keeps init, cleanup, stderr, crash tail. |
| Update rejected | Installed revision stays; check `requirements` and preparation output. |

## Missing UI

- Sidebar item missing: **Enable plugins** on; plugin `running`; item not hidden in Settings →
  Sidebar; component doesn't throw; client viewing the installation's host.
- Command Center item missing: wrong context — workspace items need an active cached workspace,
  agent items need a focused agent tab or agent-context panel.
- Slash command missing: name collides with a built-in/alias or an earlier plugin; composer has attachments.
- Panel shows "unavailable": plugin/panel/context/workspace/agent gone or not loaded yet.
- Works on web, crashes on phone: DOM globals, HTML elements, browser-only libs → run the lint script.

## Migration from pre-0.8 (`index.ts`)

Do not keep a compatibility entry. Steps, in order:

1. **Classify.** Create only the entries needed: components/client callbacks → `index.client.tsx`;
   RPC handlers/Node APIs → `index.server.ts`.
2. **Move files.** `name.client.ts(x)` → `client/name.ts(x)`, `name.server.ts(x)` → `server/…`,
   `name.shared.ts(x)` → `shared/…`; keep nested feature dirs; fix relative imports; keep
   `paseo-plugin.json`, `package.json`, `tsconfig.json` at root; **delete** old `index.ts`.
   Filename suffixes no longer mean anything.
3. **Move registrations:**

| Old (root `index.ts`) | New |
| --- | --- |
| `plugin.handle(c, h)` | `server.handle(c, h)` in `index.server.ts` |
| `plugin.addSurface(id, C)` | `client.addScreen({ id, title, Component })` |
| `plugin.addSidebarItem({ id, title, icon, surface })` | `client.addSidebarHeaderItem({ id, title, Component })` + screen with the same `id` (keeps `/plugin/<plugin>/sidebar/<id>` links) |
| `plugin.addWorkspacePanel / addCommandCenterItem / addAttachmentSource / addTheme / addTimelineTransformer / addTimelineRenderer` | same method on `client` in `index.client.tsx` |
| `plugin.addClientSlashCommand(cmd)` | `client.addSlashCommand(cmd)` |
| `plugin.addClientSide(fn)` | delete wrapper; move body of `fn` into client entry (or `return fn(client)`) |
| old `addComposerPill({ Component, title, onPress })` | `addComposerPill({ id, workspaceId, agentId, button: { title, icon, label, behavior: { kind: "action", onPress } } })`; cleanup via `.remove()`; dynamic text via `update()` |
| `defineRpc`, `defineAttachmentSource` from `@getpaseo/plugin/server` | from `@getpaseo/plugin` |
| `ZodOutput<typeof contract.input>` | `RpcInput<typeof contract>` / `RpcOutput` |

4. **Separate imports.** Hooks (`usePaseo`, `useRpc`, `useSettings`, `useAgent`, `useWorkspace`) and
   client types → `@getpaseo/plugin/client`; `Icon` → `/client/react-native`; server contexts →
   `/server`; `PluginContext` → `PluginClientContext` / `PluginServerContext`. Subpaths:
   `/react-native` → `/client/react-native`, `/ui` → `/client/ui`, `/provider` → `/server/provider`,
   `/acp` → `/server/acp`. The `@paseo/plugin` scope is gone.
5. **Declare** `"requirements": { "paseo": ">=0.8.0" }`; update the `@getpaseo/plugin` dev dependency
   and reinstall (an old pinned SDK typechecks against the old contract; install/reload doesn't typecheck).
6. **Verify:** `npm run typecheck`, `paseo plugin reload <id>`, `paseo plugin ls` → `running`, no
   error. Exercise every contribution; for RPCs call from the client and check the server result;
   for client-only plugins confirm no server process; call stored removers twice (second is a no-op).

Worked example: `plugin-examples/local-plugin` — only entry files and import paths change;
component and handler bodies move unchanged.
