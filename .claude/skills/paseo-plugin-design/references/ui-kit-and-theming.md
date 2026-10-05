# Host UI kit, theme tokens, contributed themes, links

Contents: Theme tokens and layout · Host UI (Modal, scrolling, copy/paste, toasts, icons) ·
Contribute a theme · External links and workspace browser

## Theme tokens and layout

Every surface receives `theme` (`PluginTheme`) and `layout`. Colors and spacing must come from
them; rebuild styles when `theme` or `layout.compact` changes.

```tsx
const styles = useMemo(() => ({
  screen: { flex: 1, padding: layout.compact ? 16 : 24, backgroundColor: theme.colors.surface0 },
  title: { color: theme.colors.foreground },
  detail: { color: theme.colors.foregroundMuted },
}), [theme, layout.compact]);
```

| Token | Use |
| --- | --- |
| `foreground` | Every primary `Text` |
| `foregroundMuted` | Labels, secondary copy |
| `surface0` | Root view / panel background |
| `surface1` | Raised surfaces (cards) |
| `surface2` | Controls (inputs, secondary buttons) |
| `border` | Borders, dividers |
| `accent` / `accentForeground` | Primary action fill / text on it |
| `statusSuccess` / `statusWarning` / `statusDanger` | Feedback; `statusDanger` for errors/destructive |
| `layout.compact` | `true` on mobile & narrow windows → tighter padding, stacking |
| `layout.platform` | `ios` / `android` / `web` |

Never hardcode `#000`/`#fff` or rely on RN's default text color — unstyled `Text` breaks on theme switch.

## Host UI

Import from `@getpaseo/plugin/client/react-native`.

### Modal

Bottom sheet on compact, centered dialog otherwise. Plugin owns `open`.

```tsx
<Modal title="Edit issue" icon={<Icon name="Pencil" size={18} color={theme.colors.foreground} />}
       open={open} onOpenChange={setOpen}>
  <Modal.Content>{/* padded (24) + gap 16 + scrollable by default */}</Modal.Content>
</Modal>
```

| `Modal` prop | Req | |
| --- | --- | --- |
| `title` | Yes | header + label |
| `icon` | No | before title |
| `open` / `onOpenChange` | Yes | dismissal calls `onOpenChange(false)`; you must set `open=false` |

| `Modal.Content` prop | Default | |
| --- | --- | --- |
| `style` | — | whole body viewport (backgrounds) |
| `contentContainerStyle` | padding 24, gap 16 | `padding: 0, gap: 0` for edge-to-edge rows |
| `scrollable` | `true` | `false` → bounded body; bring your own list with `flex: 1, minHeight: 0` |

- Don't wrap in another padded container unless you want a second inset.
- Dismissed by close button, backdrop, back action, Escape (web), sheet gesture.
- Children keep plugin context: `usePaseo`, `useRpc`, `useWorkspace`, `useAgent` work.

### Scrolling

- Use SDK `ScrollView` / `FlatList` (sheet-gesture aware). Never import bottom-sheet libraries.
- **One vertical scroll owner**: default body, or your list with `scrollable={false}`. A
  fixed-height vertical list inside the scrolling body fights the sheet on Android.
- Horizontal tabs: `<ScrollView horizontal style={{ flexGrow: 0 }}>` inside default content is fine.
- Expand the sheet before `scrollToEnd` etc.; offsets are locked below max height.

```tsx
<Modal.Content scrollable={false} style={{ backgroundColor: theme.colors.surface1 }}
               contentContainerStyle={{ padding: 0, gap: 0 }}>
  <FlatList style={{ flex: 1, minHeight: 0 }} data={items} keyExtractor={(i) => i.id}
            renderItem={({ item }) => <Text style={{ padding: 16, color: theme.colors.foreground }}>{item.title}</Text>} />
</Modal.Content>
```

### Copy and paste

- `await copyText(text)` from a user action, then toast; it rejects when clipboard is denied.
- `<Text selectable>` for long-press/OS copy.
- In modal forms use SDK `TextInput` (registers focus with the sheet so the keyboard lifts the form).
- No DOM clipboard code; don't use RN's deprecated `Clipboard`.

```tsx
const toast = useToast();
try { await copyText(result); toast.show("Copied", { variant: "success" }); }
catch { toast.error("Could not copy. Select the text and use Copy."); }
```

### Toasts

`useToast()` → `show(message, { variant?: "default"|"info"|"success"|"warning"|"error", durationMs?: 2200 })`
and `error(message)` (3200 ms). A new toast replaces the visible one; empty messages are ignored.

### Icons

`<Icon name="Pencil" size={18} color={theme.colors.foreground} />` — Lucide names; unknown names
render nothing. Contribution `icon` fields take the same names (https://lucide.dev/icons/).

## Contribute a theme

Data only, no component. Appears in Settings → Appearance.

```ts
client.addTheme({
  id: "mocha", name: "Catppuccin Mocha", appearance: "dark", // or "light"
  colors: { background: "#1e1e2e", foreground: "#cdd6f4", raised: "#313244", control: "#45475a",
            border: "#45475a", accent: "#cba6f7", mutedForeground: "#a6adc8", ring: "#6c7086" },
});
```

| Color | Becomes |
| --- | --- |
| `background` | App, workspace, terminal background |
| `foreground` | Primary text, terminal fg/cursor |
| `raised` | Cards, popovers, hovered rows |
| `control` | Inputs, secondary fills, light-theme sidebar |
| `border` | Borders, highest raised tint |
| `accent` | Buttons/selection/focus (optional; defaults to `foreground`) |
| `mutedForeground` | Secondary text |
| `ring` | Focus rings, scrollbars, terminal bright black |

- Hex strings only; anything else fails to load. Paseo derives the full token set (status, diff,
  syntax, terminal, shadows) from `appearance`.
- One contributed theme active at a time; if the plugin goes away, Paseo falls back to default.
- Old clients report `client.addTheme is not a function` → update the client / raise `requirements`.

## External links and workspace browser

```tsx
import { ExternalLink } from "@getpaseo/plugin/client/ui";
<ExternalLink href="https://paseo.sh/docs">Open documentation</ExternalLink>

import { openExternalUrl } from "@getpaseo/plugin/client";
await openExternalUrl("https://paseo.sh/docs"); // call directly from a user interaction
```

`ExternalLink` props: `href` (absolute http(s)), `children`, optional `accessibilityLabel`, `testID`,
`onError`.

| Platform | External links | `navigation.openBrowser` |
| --- | --- | --- |
| Electron | System browser | Available (local workspace browser tab) |
| Browser web | New tab `noopener,noreferrer` | `undefined` |
| iOS / Android | OS URL handler | `undefined` |

Always feature-check:

```tsx
const openBrowser = navigation?.openBrowser;
if (!openBrowser) return <ExternalLink href={url}>Open documentation</ExternalLink>;
return <Pressable onPress={() => openBrowser({ url, workspaceId /*, serverId */ })}>…</Pressable>;
```

- `openBrowser` never falls back to external. Page runs on the local desktop even for remote
  workspaces (`localhost` = the desktop).
- Errors: non-http(s) → `openExternalUrl` silently ignores; `openBrowser` throws
  `Only absolute HTTP(S) URLs are supported.`; empty workspace → `workspaceId is required.`;
  unknown host/workspace → `Workspace is unavailable on the requested host.`
