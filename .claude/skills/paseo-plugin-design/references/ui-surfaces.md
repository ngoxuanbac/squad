# UI surfaces (client)

Contents: Screens · Sidebar items · Workspace panels · Snapshot fields · Command Center items ·
Slash commands · Header buttons · Composer pills · Button descriptor and menus · Choosing a surface

All registrations live in `index.client.tsx` (or a `client/` function it calls). Components get
`theme`, `host`, `layout` — style with them (see ui-kit-and-theming.md).

## Screens

```tsx
client.addScreen({
  id: "bot",
  title: (params) => botName(params.botId) ?? "Bot", // or a plain string
  Component: BotScreen,
});
```

`PluginScreenProps`:

| Field | Meaning |
| --- | --- |
| `theme` | `PluginTheme` color tokens |
| `host` | `{ id, label }` of selected host |
| `layout` | `{ compact, platform: "ios" \| "android" \| "web" }` |
| `params` | `Record<string,string>` the screen was opened with; `{}` if none |
| `navigation?` | `openAgent({ agentId, serverId? })`, `openWorkspace({ workspaceId, serverId? })`, `openBrowser({ url, workspaceId, serverId? })` (Electron only) |

- Paseo owns route, header, close, host picker, error boundary, query client. Plugin owns the body.
- Title function reruns when params change. Empty/invalid title at registration throws; a title
  function that throws/returns "" shows the screen ID.
- `openScreen({ screenId, params })`: params live in the URL query (survive reload, back/forward,
  links). Values must be strings, else it throws.

## Sidebar items

```tsx
import { SidebarRow, SidebarSeparator } from "@getpaseo/plugin/client/ui";

function MainItem({ currentScreen, openScreen }: PluginSidebarItemProps) {
  return (
    <SidebarRow
      icon="Blocks"
      active={currentScreen?.screenId === "main"}
      onPress={() => openScreen({ screenId: "main" })}
    />
  );
}
client.addSidebarHeaderItem({ id: "main", title: "My plugin", Component: MainItem });
client.addSidebarFooterItem({ id: "sync", title: "Sync", Component: SyncItem }); // between "Add project" and fixed icon row
```

`PluginSidebarItemProps`: `theme`, `host`, `layout`, `currentScreen` (`{screenId, params}` of this
plugin's open screen on the item's host, or `null`), `openScreen(input)` (unknown IDs throw),
`openPopover(Content)` (anchored on wide, bottom sheet on compact; `Content` gets `theme`, `host`,
`layout`, `close()`, `openScreen()`; opening a screen closes it).

- `SidebarRow`: required `onPress`; optional `icon` (Lucide name or `{size,color}` component),
  `label`, `active`, `trailing` (presses independently), `id` (tells rows apart in test IDs).
- `title` labels the item in Settings → Sidebar (reorder/hide) and is the default row label.
- A `Component` may return a fragment/array/`null` → one item, many dynamic rows; moves/hides as a block.
- `addSidebar*Item` also works after the entry returns (runtime add/remove via the returned remover).
- A throwing item renders nothing; the rest of the sidebar keeps working.
- Multi-host: an item renders from the host of the screen shown; otherwise last-picked host, else first.
- `addSidebarItem` is deprecated; register a screen under the same `id` to keep old links working.

## Workspace panels

Open as workspace tabs beside agents, terminals, files.

```tsx
client.addWorkspacePanel({
  id: "review",
  title: "Review",
  icon: "Scan",
  context: "agent",                  // or "workspace"
  locations: ["workspace", "explorer"], // default ["workspace"]
  Component: ReviewPanel,
});
```

- `context: "workspace"` → `PluginWorkspacePanelProps`: `theme`, `host`, `layout`, `workspaceId`, `navigation?`.
- `context: "agent"` → `PluginAgentPanelProps`: same + `agentId`.
- Read cached state synchronously; selector is **required**, result compared shallowly:

```tsx
const ws = useWorkspace(workspaceId, ({ name, directory }) => ({ name, directory }));
const agent = useAgent(agentId, ({ id, title }) => ({ id, title }));
```

  Select every rendered field in one call; never select the whole snapshot. Both return `null` when
  unavailable. Snapshots are deeply frozen. Don't RPC to discover the current workspace/agent;
  fetch vendor enrichment after render.
- Paseo owns focus, split, close, persistence, query state, providers, error boundary. Restored
  tabs whose plugin/panel/context is gone show "unavailable" instead of crashing.

## Snapshot fields

Workspace: `id`, `projectId`, `projectDisplayName`, `projectRootPath`, `directory`,
`projectKind` (`git|non_git|directory`), `kind` (`directory|local_checkout|checkout|worktree`),
`name`, `title|null`, `status` (`needs_input|failed|running|attention|done`), `statusEnteredAt`,
`archivingAt`, `diffStat {additions, deletions}|null`.

Agent: `id`, `workspaceId`, `provider`, `status` (`initializing|idle|running|error|closed`),
`createdAt`, `updatedAt`, `lastActivityAt`, `title|null`, `cwd`, `model|null`, `currentModeId|null`,
`thinkingOptionId|null`, `requiresAttention`, `attentionReason` (`finished|error|permission|null`),
`parentAgentId|null`, `labels: Record<string,string>`.

## Command Center items

Opened with ⌘K / Ctrl+K.

```tsx
client.addCommandCenterItem({
  id: "open-review",
  title: "Open review",
  icon: "Scan",
  keywords: ["inspect"],
  context: "agent", // "global" | "workspace" | "agent"
  async onSelect({ paseo, rpc, workspace, agent, openPanel, openScreen }) {
    await rpc(refreshReview, { agentId: agent.id });
    openPanel("review"); // { location: "explorer" } to target Explorer
  },
});
```

| Callback field | Context | Meaning |
| --- | --- | --- |
| `context` | all | discriminator |
| `paseo` | all | selected host's `PaseoApi` |
| `rpc(contract, input)` | all | typed call to this installation's daemon handler |
| `openScreen(input)` | all | open own screen |
| `openSettings(id)` | all | open own settings screen |
| `workspace` | workspace, agent | synchronous snapshot |
| `agent` | agent | synchronous snapshot |
| `openPanel(id, options?)` | workspace, agent | open a registered panel in current context |

- Visibility: global → always on selected host; workspace → only with an active cached workspace;
  agent → only when focused tab is an agent or agent-context plugin panel of that workspace.
  Missing context hides the item (Paseo never calls the plugin to discover it).
- Agent callbacks may open agent or workspace panels; workspace callbacks only workspace panels.
  Unknown IDs fail visibly.

## Slash commands

```ts
client.addSlashCommand({
  name: "review",              // without slash
  description: "Run the review bot",
  argumentHint: "[scope]",
  context: "agent",            // "workspace" | "agent"
  async onSubmit({ args, agent, rpc, openPanel }) {
    await rpc(refreshReview, { agentId: agent.id, scope: args });
    openPanel("review");
  },
});
```

- Text is never sent to the agent. `args` = remainder, trimmed only at ends; parse it yourself.
- Paseo owns autocomplete, input clearing, error toast; it does **not** await `onSubmit` or show
  pending state — use a composer pill or panel for progress.
- Precedence: built-in client commands (and aliases) > plugin commands > provider commands; lower
  collisions are omitted; between plugins, first in stable catalog order wins.
- Commands don't run while the composer has attachments.

## Header buttons

```tsx
const review = client.addHeaderButton({
  id: "review",
  workspaceId,
  button: {
    title: "Open review",
    icon: "Scan",
    label: "Review", // omit for icon-only
    behavior: { kind: "action", onPress() { client.openPanel("review", { workspaceId }); } },
  },
});
review.update({ label: "Review · 3" });
review.update({ visible: false });
review.remove();
```

- Placed before built-in actions on the workspace header's right side. Menus/popovers show a chevron
  on wide layouts; compact shows icons only. Excess buttons move into a shared overflow menu.

## Composer pills

```tsx
const pill = client.addComposerPill({
  id: "review", workspaceId, agentId,
  button: { title: "Open review", icon: "Scan", label: "Review",
    behavior: { kind: "action", onPress() { client.openPanel("review", { workspaceId, agentId }); } } },
});
```

- One agent's composer track (beside Tasks and Subagents). Always shows icon + `label` (or `title`);
  never a chevron.
- For pills that follow the agent directory, use an owned list subscription
  (`paseo.observeEvents` / SDK events) that replaces registrations on each snapshot and aborts the
  observation in entry cleanup — see the `local-plugin` example.

## Button descriptor and menus

| Field | Req | Meaning |
| --- | --- | --- |
| `title` | Yes | Accessible label, tooltip, sheet title |
| `icon` | Yes | Lucide name or `ComponentType<PluginButtonIconProps>` |
| `label` | No | Display text |
| `visible` | No | default `true`; `false` removes trigger and its space |
| `disabled` | No | default `false` |
| `behavior` | Yes | see below |

```ts
type PluginButtonBehavior =
  | { kind: "action"; onPress(): void | Promise<void> }   // busy until settled, blocks repeats, toast on failure
  | { kind: "menu"; items: readonly PluginButtonMenuEntry[] }
  | { kind: "popover"; Content: React.ComponentType<PluginButtonContentProps> };
```

- Menu entries: `{ kind: "item", id, title, icon?, visible?, disabled?, behavior }` or
  `{ kind: "separator", id }`. Leading/trailing/double separators are removed. Items can nest menus
  (flyouts on wide, paged on compact). Choosing an action closes the menu.
- `PluginButtonIconProps`: `theme`, `host`, `layout`, `size`, `color`, target context; may use hooks.
- `PluginButtonContentProps`: `theme`, `host`, `layout`, target context, `close()`; body only, can
  use `usePaseo`, `useRpc`, `useWorkspace`, `useAgent`, React Query.
- Target context: `{ context: "workspace", workspaceId }` (header) or
  `{ context: "agent", workspaceId, agentId }` (pill).
- `update(patch)` keeps identity/order; pass a complete new `behavior` when changing it; invalid
  updates throw and leave the button unchanged. Mutating the original descriptor does nothing.
- Hiding/disabling/changing behavior closes an open surface; hiding keeps position. Duplicate ID in
  the same target throws. `remove()` is idempotent.

## Choosing a surface

| Situation | Prefer |
| --- | --- |
| Rich, standalone, navigable UI | Screen + sidebar item |
| Tied to a workspace/agent, used alongside the agent | Workspace/agent panel |
| One-shot action, discoverable via search | Command Center item |
| Action typed while chatting, with arguments | Slash command |
| Always-visible status/count for a workspace | Header button with `update()` |
| Always-visible status for one agent | Composer pill |
| Quick details without leaving context | Popover behavior / sidebar `openPopover` |
