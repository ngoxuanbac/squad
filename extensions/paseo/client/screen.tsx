import type { PluginTheme } from "@getpaseo/plugin";
import { useRpc, type PluginScreenProps } from "@getpaseo/plugin/client";
import { Icon, Modal, ScrollView } from "@getpaseo/plugin/client/react-native";
import { useState, type ReactNode } from "react";
import { Text, View } from "react-native";
import { snapshotRpc } from "../shared/rpc";
import {
  agoText,
  clockText,
  eventColour,
  findMember,
  isBusy,
  readView,
  statusColour,
  noticeColour,
  type SquadNotice,
  treeOf,
  type Member,
  type MemberNode,
  type SquadEvent,
  type Team,
  type TeamNode,
  type View as SquadView,
} from "../shared/view";
import { Dot, Notice, PressRow, SectionHead, Tag } from "./parts";
import { BUSY_MS, QUIET_MS, useNow, usePoll } from "./poll";
import { ICON, INDENT, SPACE, text, type Colour } from "./style";
import { TailBody, TailPane } from "./tail";

/** Events shown before "Show all". */
const EVENTS_SHOWN = 12;

/** ps --view, asked for soon while a member is working, rarely otherwise. */
function useSnapshot() {
  const call = useRpc(snapshotRpc);
  return usePoll<SquadView>(
    async () => {
      const r = await call({});
      return r.ok ? readView(r.view) : r;
    },
    "ps",
    (view) => (isBusy(view) ? BUSY_MS : QUIET_MS),
  );
}

interface Ctx {
  theme: PluginTheme;
  now: number;
  selected: string | null;
  select: (id: string) => void;
  /** A team's open state when the user folded or unfolded it. */
  folds: Record<string, boolean>;
  toggle: (id: string, open: boolean) => void;
}

function Stat({ theme, label, value, colour }: { theme: PluginTheme; label: string; value: number; colour?: "statusSuccess" }) {
  return (
    <View style={{ minWidth: 64 }}>
      <Text style={text(theme, "stat", value > 0 ? colour : undefined)}>{value}</Text>
      <Text style={text(theme, "label")}>{label}</Text>
    </View>
  );
}

function Header({ theme, view, now }: { theme: PluginTheme; view: SquadView; now: number }) {
  const s = view.summary;
  const d = view.daemon;
  const up = agoText(d.started_at, now);
  const facts = [d.version ? `squad ${d.version}` : "", d.pid ? `pid ${d.pid}` : "", up ? `started ${up}` : ""].filter(Boolean);
  return (
    <View style={{ gap: SPACE.md }}>
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: SPACE.xl }}>
        <Stat theme={theme} label="Teams" value={s.teams} />
        <Stat theme={theme} label="Sessions" value={s.sessions} />
        <Stat theme={theme} label="Workers" value={s.workers} />
        <Stat theme={theme} label="Working" value={s.working} colour="statusSuccess" />
      </View>
      {facts.length ? <Text style={text(theme, "label")}>{facts.join(" · ")}</Text> : null}
    </View>
  );
}

/** One participant: status, name, tags and, below, its latest task. Selecting it shows its log. */
function MemberRow({ ctx, member, gate, depth }: { ctx: Ctx; member: Member; gate: boolean; depth: number }) {
  const { theme } = ctx;
  const colour = statusColour(member.status);
  const dim = member.status === "gone";
  const ago = agoText(member.last_active, ctx.now);
  const details = [member.role, member.kind, member.model].filter(Boolean).join(" · ");
  return (
    <PressRow
      theme={theme}
      onPress={() => ctx.select(member.id)}
      selected={ctx.selected === member.id}
      label={`${member.name}, ${member.status}. Show its log`}
      style={{ paddingVertical: SPACE.sm + 1, paddingRight: SPACE.md, paddingLeft: depth * INDENT + SPACE.sm, gap: SPACE.xs }}
    >
      <View style={{ flexDirection: "row", alignItems: "center", gap: SPACE.md }}>
        <Dot colour={theme.colors[colour]} />
        <Icon name={member.kind === "worker" ? "Bot" : "User"} size={ICON.sm} color={theme.colors.foregroundMuted} />
        <Text style={[text(theme, "title", dim ? "foregroundMuted" : undefined), { flexShrink: 1 }]} numberOfLines={1}>
          {member.name}
        </Text>
        {gate ? <Tag theme={theme} label="gate" colour="accent" /> : null}
        <Text style={[text(theme, "label"), { flexShrink: 1 }]} numberOfLines={1}>
          {details}
        </Text>
        <View style={{ flex: 1 }} />
        {member.unread > 0 ? (
          <View style={{ flexDirection: "row", alignItems: "center", gap: SPACE.xs }} accessibilityLabel={`${member.unread} unread`}>
            <Icon name="Mail" size={ICON.sm} color={theme.colors.accent} />
            <Text style={text(theme, "label", "accent")}>{member.unread}</Text>
          </View>
        ) : null}
        <Text style={text(theme, "label", colour)}>{member.status}</Text>
        {ago ? <Text style={[text(theme, "label"), { minWidth: 52, textAlign: "right" }]}>{ago}</Text> : null}
      </View>
      {member.task ? (
        <Text style={[text(theme, "meta"), { paddingLeft: 8 + SPACE.md }]} numberOfLines={1}>
          {member.task}
        </Text>
      ) : null}
    </PressRow>
  );
}

function MemberBranch({ ctx, node, gate, depth }: { ctx: Ctx; node: MemberNode; gate: boolean; depth: number }) {
  return (
    <>
      <MemberRow ctx={ctx} member={node.member} gate={gate} depth={depth} />
      {node.taskforces.map((t) => (
        <TeamBlock key={t.team.id} ctx={ctx} node={t} depth={depth + 1} />
      ))}
    </>
  );
}

/** A team's heading: fold, name, template, taskforce caller, status; below it, its root and gate. */
function TeamHead({ ctx, team, open, depth, count }: { ctx: Ctx; team: Team; open: boolean; depth: number; count: number }) {
  const { theme } = ctx;
  const closed = team.status === "closed";
  const created = agoText(team.created_at, ctx.now);
  const meta = [team.root, team.gate ? `gate ${team.gate}` : "", closed && created ? `created ${created}` : ""].filter(Boolean);
  return (
    <PressRow
      theme={theme}
      onPress={() => ctx.toggle(team.id, !open)}
      expanded={open}
      label={`${open ? "Fold" : "Unfold"} team ${team.name}`}
      style={{ paddingVertical: SPACE.sm + 2, paddingRight: SPACE.md, paddingLeft: depth * INDENT, gap: SPACE.xs, marginTop: depth === 0 ? SPACE.sm : 0 }}
    >
      <View style={{ flexDirection: "row", alignItems: "center", gap: SPACE.sm + 2 }}>
        <Icon name={open ? "ChevronDown" : "ChevronRight"} size={ICON.sm} color={theme.colors.foregroundMuted} />
        <Icon name={closed ? "Archive" : team.parent ? "GitBranch" : "Users"} size={ICON.md} color={closed ? theme.colors.foregroundMuted : theme.colors.foreground} />
        <Text style={[text(theme, "title", closed ? "foregroundMuted" : undefined), { flexShrink: 1 }]} numberOfLines={1}>
          {team.name}
        </Text>
        {team.template ? <Tag theme={theme} label={team.template} /> : null}
        {team.parent ? <Text style={text(theme, "label")} numberOfLines={1}>{`taskforce of ${team.parent}`}</Text> : null}
        <View style={{ flex: 1 }} />
        {!open ? <Text style={text(theme, "label")}>{`${count} ${count === 1 ? "member" : "members"}`}</Text> : null}
        <Text style={text(theme, "label", closed ? "foregroundMuted" : "statusSuccess")}>{team.status}</Text>
      </View>
      {meta.length ? (
        <Text style={[text(theme, "label"), { paddingLeft: ICON.sm + ICON.md + 2 * (SPACE.sm + 2) }]} numberOfLines={1} ellipsizeMode="head">
          {meta.join(" · ")}
        </Text>
      ) : null}
    </PressRow>
  );
}

function TeamBlock({ ctx, node, depth }: { ctx: Ctx; node: TeamNode; depth: number }) {
  const team = node.team;
  const open = ctx.folds[team.id] ?? team.status !== "closed";
  return (
    <View>
      <TeamHead ctx={ctx} team={team} open={open} depth={depth} count={team.members.length} />
      {open && node.members.length === 0 ? (
        <Text style={[text(ctx.theme, "meta"), { paddingLeft: (depth + 1) * INDENT + SPACE.sm, paddingVertical: SPACE.xs }]}>No members yet.</Text>
      ) : null}
      {open ? node.members.map((m) => <MemberBranch key={m.member.id || m.member.name} ctx={ctx} node={m} gate={m.member.name === team.gate} depth={depth + 1} />) : null}
    </View>
  );
}

/** One line of a list: a time, a coloured kind, then the text. */
function KindRow({ theme, left, kind, lines, children }: { theme: PluginTheme; left: [string, number]; kind: [string, number, Colour]; lines: number; children: ReactNode }) {
  return (
    <View style={{ flexDirection: "row", gap: SPACE.md, paddingVertical: SPACE.xs, paddingHorizontal: SPACE.sm }}>
      <Text style={[text(theme, "label"), { minWidth: left[1], fontVariant: ["tabular-nums"] }]}>{left[0]}</Text>
      <Text style={[text(theme, "label", kind[2]), { minWidth: kind[1] }]}>{kind[0]}</Text>
      <Text style={[text(theme, "label", "foreground"), { flex: 1 }]} numberOfLines={lines}>
        {children}
      </Text>
    </View>
  );
}

/** The latest notices about the teams' mail: where a gate answered, settled, failed or was lost. */
function Notices({ theme, notices }: { theme: PluginTheme; notices: SquadNotice[] }) {
  if (notices.length === 0) return null;
  return (
    <View>
      <SectionHead theme={theme} icon="Bell" title="Notices" count={notices.length} />
      {notices.map((n, i) => (
        <KindRow key={i} theme={theme} left={[n.age, 36]} kind={[n.kind, 64, noticeColour(n.kind)]} lines={3}>
          {n.where !== "-" ? `${n.where}: ` : ""}
          {n.body}
        </KindRow>
      ))}
    </View>
  );
}

function Events({ theme, events, now }: { theme: PluginTheme; events: SquadEvent[]; now: number }) {
  const [all, setAll] = useState(false);
  if (events.length === 0) return null;
  const newest = [...events].reverse();
  const shown = all ? newest : newest.slice(0, EVENTS_SHOWN);
  return (
    <View>
      <SectionHead theme={theme} icon="History" title="Events" count={events.length} />
      {shown.map((e, i) => (
        <KindRow key={i} theme={theme} left={[clockText(e.at, now), 72]} kind={[e.kind, 52, eventColour(e.kind)]} lines={2}>
          {e.text}
        </KindRow>
      ))}
      {events.length > EVENTS_SHOWN ? (
        <PressRow theme={theme} onPress={() => setAll(!all)} label={all ? "Show fewer events" : "Show all events"} style={{ paddingVertical: SPACE.sm, paddingHorizontal: SPACE.sm }}>
          <Text style={text(theme, "label", "accent")}>{all ? "Show fewer" : `Show all ${events.length}`}</Text>
        </PressRow>
      ) : null}
    </View>
  );
}

/** The teams, solos, closed teams and events of one read. */
function Body({ ctx, view }: { ctx: Ctx; view: SquadView }) {
  const { theme } = ctx;
  const tree = treeOf(view);
  const closedOpen = ctx.folds["\u0000closed"] ?? false;
  const empty = tree.teams.length + tree.solos.length + tree.closed.length === 0;
  return (
    <>
      {empty ? <Notice theme={theme} kind="empty" detail="Open an agent session with squad, or ask one to make a team." /> : null}
      <Notices theme={theme} notices={view.notices} />
      {tree.teams.length ? (
        <View>
          <SectionHead theme={theme} icon="Users" title="Teams" count={tree.teams.length} />
          {tree.teams.map((t) => (
            <TeamBlock key={t.team.id} ctx={ctx} node={t} depth={0} />
          ))}
        </View>
      ) : null}
      {tree.solos.length ? (
        <View>
          <SectionHead theme={theme} icon="User" title="Solo sessions" count={tree.solos.length} />
          {tree.solos.map((s) => (
            <MemberBranch key={s.member.id || s.member.name} ctx={ctx} node={s} gate={false} depth={0} />
          ))}
        </View>
      ) : null}
      {tree.closed.length ? (
        <View>
          <SectionHead theme={theme} icon="Archive" title="Closed teams" count={tree.closed.length} open={closedOpen} onToggle={() => ctx.toggle("\u0000closed", !closedOpen)} />
          {closedOpen ? tree.closed.map((t) => <TeamBlock key={t.id} ctx={ctx} node={{ team: t, members: t.members.map((member) => ({ member, taskforces: [] })) }} depth={0} />) : null}
        </View>
      ) : null}
      <Events theme={theme} events={view.events} now={ctx.now} />
    </>
  );
}

/** The Squad screen: what `squad top` shows, read-only, and the log of the member selected. */
export function SquadScreen({ theme, layout }: PluginScreenProps) {
  const { value: view, error, at } = useSnapshot();
  const now = useNow(10000);
  const [selected, setSelected] = useState<string | null>(null);
  const [folds, setFolds] = useState<Record<string, boolean>>({});
  const ctx: Ctx = {
    theme,
    now,
    selected,
    select: (id) => setSelected(selected === id ? null : id),
    folds,
    toggle: (id, open) => setFolds((f) => ({ ...f, [id]: open })),
  };
  const found = view && selected ? findMember(view, selected) : null;
  const member = found?.member ?? null;
  const teamName = found?.team?.name ?? (found ? "solo" : "");
  const wide = !layout.compact;
  const pad = layout.compact ? SPACE.lg : SPACE.xxl;

  const list = (
    <ScrollView style={{ flex: 1 }} contentContainerStyle={{ paddingHorizontal: pad, paddingTop: pad, paddingBottom: SPACE.xxl }}>
      <View style={{ width: "100%", maxWidth: 1080, alignSelf: "center" }}>
        {view ? <Header theme={theme} view={view} now={now} /> : null}
        {error && view ? (
          <Text style={[text(theme, "label", "statusWarning"), { paddingTop: SPACE.md }]}>{`Showing the read from ${clockText(new Date(at ?? now).toISOString(), now)}: ${error.message}`}</Text>
        ) : null}
        {error && !view ? <Notice theme={theme} kind={error.code} detail={error.message} /> : null}
        {!view && !error ? <Notice theme={theme} kind="loading" /> : null}
        {view ? <Body ctx={ctx} view={view} /> : null}
      </View>
    </ScrollView>
  );

  return (
    <View style={{ flex: 1, flexDirection: wide ? "row" : "column", backgroundColor: theme.colors.surface0 }}>
      <View style={{ flex: 3 }}>{list}</View>
      {selected && wide ? (
        <View style={{ flex: 2, minWidth: 320 }}>
          <TailPane key={selected} theme={theme} id={selected} member={member} team={teamName} onClose={() => setSelected(null)} />
        </View>
      ) : null}
      {selected && !wide ? (
        <Modal title={member?.name ?? "Log"} icon={<Icon name="ScrollText" size={ICON.md} color={theme.colors.foregroundMuted} />} open onOpenChange={(open) => (open ? null : setSelected(null))}>
          <Modal.Content>
            <Text style={text(theme, "label")}>{[teamName, member?.status].filter(Boolean).join(" · ")}</Text>
            <TailBody key={selected} theme={theme} id={selected} working={member?.status === "working"} />
          </Modal.Content>
        </Modal>
      ) : null}
    </View>
  );
}
