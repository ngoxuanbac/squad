import type { PluginTheme } from "@getpaseo/plugin";
import { useRpc } from "@getpaseo/plugin/client";
import { Icon, ScrollView } from "@getpaseo/plugin/client/react-native";
import { useRef } from "react";
import { Pressable, Text, View, type ScrollView as NativeScrollView } from "react-native";
import { tailRpc } from "../shared/rpc";
import { statusColour, type Member, type TailLine } from "../shared/view";
import { Dot, Notice } from "./parts";
import { usePoll } from "./poll";
import { ICON, SPACE, text, type Colour } from "./style";

/** How many lines of the log are shown. */
export const TAIL_LINES = 60;
/** How often the tail is asked for while its member is in a turn, and otherwise. */
const TAIL_BUSY_MS = 2500;
const TAIL_QUIET_MS = 10000;

/** The last lines of one member's log, polled while shown. */
export function useTail(id: string, working: boolean) {
  const call = useRpc(tailRpc);
  return usePoll<TailLine[]>(
    async () => {
      const r = await call({ id, lines: TAIL_LINES });
      // The daemon side already checked the tail --view version.
      return r.ok ? { ok: true as const, value: r.lines } : r;
    },
    `tail:${id}`,
    () => (working ? TAIL_BUSY_MS : TAIL_QUIET_MS),
  );
}

const KIND_COLOUR: Record<string, Colour> = {
  mail: "accent",
  user: "foreground",
  assistant: "foreground",
  thinking: "foregroundMuted",
  tool: "accent",
  tool_result: "foregroundMuted",
  result: "statusSuccess",
  system: "foregroundMuted",
  error: "statusDanger",
};
const KIND_MARK: Record<string, string> = { mail: "✉ ", user: "› ", thinking: "… ", tool: "▸ ", tool_result: "  ", result: "✓ ", error: "✗ " };

function Line({ theme, line }: { theme: PluginTheme; line: TailLine }) {
  const colour = KIND_COLOUR[line.kind] ?? "foreground";
  return (
    <Text style={[text(theme, "code", colour), line.kind === "thinking" ? { fontStyle: "italic" } : null]} selectable>
      {KIND_MARK[line.kind] ?? ""}
      {line.text}
      {line.rest ? <Text style={text(theme, "code", "foregroundMuted")}>{`  ${line.rest}`}</Text> : null}
    </Text>
  );
}

/** The lines, or why there are none. */
export function TailBody({ theme, id, working }: { theme: PluginTheme; id: string; working: boolean }) {
  const { value, error } = useTail(id, working);
  if (error && !value) return <Notice theme={theme} kind={error.code} detail={error.message} />;
  if (!value) return <Notice theme={theme} kind="loading" />;
  return (
    <View style={{ gap: SPACE.xs }}>
      {error ? <Text style={text(theme, "label", "statusWarning")}>{`Showing the last read: ${error.message}`}</Text> : null}
      {value.length === 0 ? <Text style={text(theme, "meta")}>Nothing in this log yet.</Text> : null}
      {value.map((line, i) => (
        <Line key={i} theme={theme} line={line} />
      ))}
    </View>
  );
}

/** The selected member's heading: status, name, team, and a close button. */
function TailHead({ theme, member, team, onClose }: { theme: PluginTheme; member: Member | null; team: string; onClose: () => void }) {
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: SPACE.md, paddingHorizontal: SPACE.xl, paddingVertical: SPACE.md, borderBottomWidth: 1, borderBottomColor: theme.colors.border }}>
      <Icon name="ScrollText" size={ICON.md} color={theme.colors.foregroundMuted} />
      {member ? <Dot colour={theme.colors[statusColour(member.status)]} /> : null}
      <Text style={[text(theme, "title"), { flexShrink: 1 }]} numberOfLines={1}>
        {member?.name ?? "Log"}
      </Text>
      <Text style={[text(theme, "label"), { flex: 1 }]} numberOfLines={1}>
        {[team, member?.status].filter(Boolean).join(" · ")}
      </Text>
      <Pressable onPress={onClose} accessibilityRole="button" accessibilityLabel="Close log" hitSlop={SPACE.md}>
        <Icon name="X" size={ICON.md} color={theme.colors.foregroundMuted} />
      </Pressable>
    </View>
  );
}

/** The wide layout's side pane: heading, then the log scrolled to its end. */
export function TailPane({ theme, id, member, team, onClose }: { theme: PluginTheme; id: string; member: Member | null; team: string; onClose: () => void }) {
  const scroll = useRef<NativeScrollView>(null);
  return (
    <View style={{ flex: 1, borderLeftWidth: 1, borderLeftColor: theme.colors.border }}>
      <TailHead theme={theme} member={member} team={team} onClose={onClose} />
      <ScrollView ref={scroll} style={{ flex: 1 }} contentContainerStyle={{ padding: SPACE.xl }} onContentSizeChange={() => scroll.current?.scrollToEnd({ animated: false })}>
        <TailBody theme={theme} id={id} working={member?.status === "working"} />
      </ScrollView>
    </View>
  );
}
