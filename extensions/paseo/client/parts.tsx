import type { PluginTheme } from "@getpaseo/plugin";
import { Icon } from "@getpaseo/plugin/client/react-native";
import type { ReactNode } from "react";
import { Pressable, Text, View, type PressableStateCallbackType, type StyleProp, type ViewStyle } from "react-native";
import { ICON, RADIUS, SPACE, text, type Colour } from "./style";

/** What the screen says instead of the view: one icon, a title, one line of why. */
const NOTICES: Record<string, { icon: string; title: string; warn?: boolean }> = {
  missing: { icon: "FileQuestion", title: "squad is not installed", warn: true },
  down: { icon: "ServerOff", title: "Squad daemon is not running", warn: true },
  older: { icon: "ArrowDownToLine", title: "This squad is older than the plugin", warn: true },
  newer: { icon: "ArrowUpFromLine", title: "This squad is newer than the plugin", warn: true },
  failed: { icon: "TriangleAlert", title: "squad did not answer", warn: true },
  empty: { icon: "Inbox", title: "No teams or sessions" },
  loading: { icon: "Loader", title: "Reading squad…" },
};

export function Notice({ theme, kind, detail }: { theme: PluginTheme; kind: string; detail?: string }) {
  const n = NOTICES[kind] ?? NOTICES.failed;
  const colour = n.warn ? theme.colors.statusWarning : theme.colors.foregroundMuted;
  return (
    <View style={{ padding: SPACE.xxl, gap: SPACE.md, alignItems: "flex-start" }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: SPACE.md }}>
        <Icon name={n.icon} size={ICON.md} color={colour} />
        <Text accessibilityRole="header" style={[text(theme, "title"), { flexShrink: 1 }]}>
          {n.title}
        </Text>
      </View>
      {detail ? <Text style={text(theme, "meta")}>{detail}</Text> : null}
    </View>
  );
}

/** A small bordered word: a template, a role, "gate". */
export function Tag({ theme, label, colour }: { theme: PluginTheme; label: string; colour?: Colour }) {
  return (
    <View style={{ borderWidth: 1, borderColor: theme.colors.border, borderRadius: RADIUS, paddingHorizontal: SPACE.sm + 1 }}>
      <Text style={text(theme, "label", colour)} numberOfLines={1}>
        {label}
      </Text>
    </View>
  );
}

export function Dot({ colour }: { colour: string }) {
  return <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: colour }} />;
}

/** A section's heading, optionally a fold. */
export function SectionHead({ theme, icon, title, count, open, onToggle }: { theme: PluginTheme; icon: string; title: string; count?: number; open?: boolean; onToggle?: () => void }) {
  const body = (
    <>
      {onToggle ? <Icon name={open ? "ChevronDown" : "ChevronRight"} size={ICON.sm} color={theme.colors.foregroundMuted} /> : null}
      <Icon name={icon} size={ICON.sm} color={theme.colors.foregroundMuted} />
      <Text accessibilityRole="header" style={text(theme, "section")}>
        {title}
      </Text>
      {count !== undefined ? <Text style={text(theme, "label")}>{count}</Text> : null}
    </>
  );
  const style: ViewStyle = { flexDirection: "row", alignItems: "center", gap: SPACE.sm + 2, paddingTop: SPACE.xl, paddingBottom: SPACE.sm };
  return onToggle ? (
    <PressRow theme={theme} onPress={onToggle} expanded={open} label={`${open ? "Fold" : "Unfold"} ${title}`} style={style}>
      {body}
    </PressRow>
  ) : (
    <View style={style}>{body}</View>
  );
}

/** A pressable row tinted on hover, press and selection. */
export function PressRow({
  theme,
  onPress,
  selected,
  expanded,
  label,
  style,
  children,
}: {
  theme: PluginTheme;
  onPress: () => void;
  selected?: boolean;
  expanded?: boolean;
  label?: string;
  style?: StyleProp<ViewStyle>;
  children: ReactNode;
}) {
  const tint = (state: PressableStateCallbackType) => {
    const hovered = (state as PressableStateCallbackType & { hovered?: boolean }).hovered === true;
    if (selected) return theme.colors.surface2;
    return state.pressed ? theme.colors.surface2 : hovered ? theme.colors.surface1 : "transparent";
  };
  return (
    <Pressable
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityState={{ selected, expanded }}
      style={(state) => [{ borderRadius: RADIUS }, style, { backgroundColor: tint(state) }]}
    >
      {children}
    </Pressable>
  );
}
