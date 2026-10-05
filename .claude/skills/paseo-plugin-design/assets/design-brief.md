# Paseo plugin design brief — <plugin-id>

> Fill every section; write "n/a" rather than deleting. Keep it to one page.

## 1. Purpose

- **Problem / job-to-be-done:** <one sentence>
- **Users & devices:** <desktop / web / iOS / Android — client code runs on all of them>
- **Target daemon(s):** <local, remote via --host, multi-host?>
- **Minimum Paseo version:** `requirements.paseo = ">=0.8.0"` <raise if using newer APIs, e.g. usage sources ≥0.11>

## 2. Contributions

| # | User need | Contribution | Runtime | ID(s) | Context / placement |
| - | --------- | ------------ | ------- | ----- | ------------------- |
| 1 | | e.g. `addWorkspacePanel` | client | `review` | `agent`, locations `workspace, explorer` |
| 2 | | e.g. `addCommandCenterItem` | client | `open-review` | `agent` |
| 3 | | e.g. `server.before("agent.create")` | server | — | codex only |

IDs: `^[a-z][a-z0-9-]*$` (RPC names may use `.` `_`; settings IDs may use `_`).

## 3. Runtime split

- **Client entry needed?** <yes/no — why>
- **Server entry needed?** <yes/no — RPC / hooks / settings read / provider / usage / timeline append>
- **SDK operations used (no RPC needed):** <e.g. paseo.workspaces.create, agents.ref(id).send>

## 4. Contracts (`shared/`)

| Name | Kind | Input | Output | Notes |
| ---- | ---- | ----- | ------ | ----- |
| `review.refresh` | `defineRpc` | `{ agentId: string, scope?: string }` | `{ refreshed: boolean }` | |
| `display` | `defineSettings` (scope host, v1) | — | `{ showMetadata: boolean = true }` | |

## 5. File layout

```text
<plugin-id>/
  paseo-plugin.json
  index.client.tsx
  index.server.ts
  client/...
  server/...
  shared/...
```

## 6. Data, secrets, trust

- **Secrets & where they live:** <server/ only; read from env/keychain/file; never in settings, logs, RPC output>
- **External network calls:** <endpoints; pass `signal`; timeouts>
- **Machine side effects:** <files written, processes spawned — runs as daemon user>
- **Hook policies:** <what is allowed/denied/modified; loop limits for follow-ups>

## 7. UX states

| Surface | Loading | Empty | Error | Compact (mobile) behavior |
| ------- | ------- | ----- | ----- | ------------------------- |
| | | | toast / `statusDanger` text | |

## 8. Lifecycle & cleanup

- Subscriptions / timers / watchers created: <list> → released in entry cleanup
- Dynamic registrations (sidebar rows, header buttons, pills): <how added/updated/removed>

## 9. Distribution

- **Channel:** <directory / GitHub / Git (+ :subdir, --ref) / npm public / npm private>
- **Source identifier users will paste:** `<e.g. npm:@acme/paseo-review@^1.0.0>`
- **`build` preparation:** <none | ["npm","ci","--omit=dev"] for Git with runtime deps>
- **Extra files for npm `files`:** <icon.svg, generated assets>

## 10. Verification plan

- [ ] `node scripts/lint-plugin.mjs <dir>` clean
- [ ] `npm run typecheck`
- [ ] install / reload → `paseo plugin ls` shows `running`
- [ ] every contribution exercised on desktop **and** a compact/mobile width
- [ ] RPC round-trip verified; error path shows a toast
- [ ] reload + disable + remove: no leaked timers/subscriptions; removers idempotent
- [ ] `paseo plugin logs <id>` free of secrets
