// What `squad ps --view` and `squad tail --view` print (the Go types in internal/view), read and checked here: the
// version check, defaults for missing fields, the team tree (taskforces under their caller) and small
// pure helpers for drawing. No imports, so the app, the daemon side and `node --test` all load it as is.

/** The `ps --view` version this plugin reads. Squad bumps it on any change that is not purely additive. */
export const VIEW_VERSION = 1;
/** The `tail --view` version this plugin reads. */
export const TAIL_VERSION = 1;

export interface Daemon {
  version: string;
  pid: number;
  started_at: string;
}

export interface Summary {
  teams: number;
  sessions: number;
  workers: number;
  working: number;
}

export interface Member {
  id: string;
  name: string;
  /** Empty for a solo. */
  role: string;
  /** session | worker. */
  kind: string;
  /** working | idle | permission | parked | stopped | gone; a newer squad may add more (drawn muted). */
  status: string;
  model: string;
  /** Empty for a solo. */
  reports_to: string;
  task: string;
  unread: number;
  cwd: string;
  last_active: string;
}

export interface Team {
  id: string;
  name: string;
  template: string;
  root: string;
  /** open | closed. */
  status: string;
  /** The member name that talks for the team. */
  gate: string;
  /** A taskforce's caller (a participant name); empty for a team the human made. */
  parent: string;
  created_at: string;
  members: Member[];
}

export interface SquadEvent {
  at: string;
  kind: string;
  team: string;
  text: string;
}

/** One of squad's notices about a team's mail: reply, settled, failed or gate_lost. */
export interface SquadNotice {
  at: string;
  age: string;
  /** The team, else -. */
  where: string;
  kind: string;
  body: string;
}

export interface View {
  version: number;
  daemon: Daemon;
  summary: Summary;
  /** Open teams first, then closed ones (newest first). */
  teams: Team[];
  solos: Member[];
  /** The latest ones, newest last. */
  events: SquadEvent[];
  /** The latest notices, newest first. */
  notices: SquadNotice[];
}

export interface TailLine {
  kind: string;
  text: string;
  rest?: string;
}

export type Refusal = { ok: false; code: "older" | "newer" | "failed"; error: string };
export type Read<T> = { ok: true; value: T } | Refusal;

type Doc = Record<string, unknown>;

const isDoc = (v: unknown): v is Doc => typeof v === "object" && v !== null && !Array.isArray(v);
const str = (v: unknown): string => (typeof v === "string" ? v : "");
const num = (v: unknown): number => (typeof v === "number" && Number.isFinite(v) ? v : 0);
const list = (v: unknown): Doc[] => (Array.isArray(v) ? v.filter(isDoc) : []);

/** Refuses a document of another version, saying which side to update. */
function checkVersion(doc: unknown, what: string, want: number): Refusal | null {
  if (!isDoc(doc)) return { ok: false, code: "failed", error: `squad ${what} --view did not print a JSON object.` };
  const version = doc.version;
  if (typeof version !== "number") {
    return { ok: false, code: "older", error: `This squad's ${what} --view has no version; this plugin reads version ${want}. Update squad.` };
  }
  if (version < want) {
    return { ok: false, code: "older", error: `squad speaks ${what} --view version ${version}; this plugin reads ${want}. Update squad.` };
  }
  if (version > want) {
    return {
      ok: false,
      code: "newer",
      error: `squad speaks ${what} --view version ${version}; this plugin reads ${want}. Reinstall the plugin from this squad, then reload the Paseo app.`,
    };
  }
  return null;
}

export function readMember(m: Doc): Member {
  return {
    id: str(m.id),
    name: str(m.name),
    role: str(m.role),
    kind: str(m.kind),
    status: str(m.status),
    model: str(m.model),
    reports_to: str(m.reports_to),
    task: str(m.task),
    unread: num(m.unread),
    cwd: str(m.cwd),
    last_active: str(m.last_active),
  };
}

function readTeam(t: Doc): Team {
  return {
    id: str(t.id),
    name: str(t.name),
    template: str(t.template),
    root: str(t.root),
    status: str(t.status),
    gate: str(t.gate),
    parent: str(t.parent),
    created_at: str(t.created_at),
    members: list(t.members).map(readMember),
  };
}

/** `squad ps --view` as parsed JSON: the view when it is the version this plugin reads, with every field present. */
export function readView(doc: unknown): Read<View> {
  const refused = checkVersion(doc, "ps", VIEW_VERSION);
  if (refused) return refused;
  const d = doc as Doc;
  const daemon = isDoc(d.daemon) ? d.daemon : {};
  const summary = isDoc(d.summary) ? d.summary : {};
  return {
    ok: true,
    value: {
      version: num(d.version),
      daemon: { version: str(daemon.version), pid: num(daemon.pid), started_at: str(daemon.started_at) },
      summary: { teams: num(summary.teams), sessions: num(summary.sessions), workers: num(summary.workers), working: num(summary.working) },
      teams: list(d.teams).map(readTeam),
      solos: list(d.solos).map(readMember),
      events: list(d.events).map((e) => ({ at: str(e.at), kind: str(e.kind), team: str(e.team), text: str(e.text) })),
      notices: list(d.notices).map((n) => ({ at: str(n.at), age: str(n.age), where: str(n.where), kind: str(n.kind), body: str(n.body) })),
    },
  };
}

/** `squad tail --view` as parsed JSON: its lines when it is the version this plugin reads. */
export function readTail(doc: unknown): Read<TailLine[]> {
  const refused = checkVersion(doc, "tail", TAIL_VERSION);
  if (refused) return refused;
  const lines = list((doc as Doc).lines).map((l): TailLine => {
    const rest = str(l.rest);
    return rest ? { kind: str(l.kind), text: str(l.text), rest } : { kind: str(l.kind), text: str(l.text) };
  });
  return { ok: true, value: lines };
}

/** `text` as JSON, or a refusal saying the command printed something else. */
export function parseJson(text: string, what: string): Read<unknown> {
  try {
    return { ok: true, value: JSON.parse(text) };
  } catch {
    return { ok: false, code: "failed", error: `squad ${what} --view did not print JSON.` };
  }
}

// ---- The tree drawn on screen ----------------------------------------------------------------

export interface MemberNode {
  member: Member;
  /** Open taskforces this participant called. */
  taskforces: TeamNode[];
}

export interface TeamNode {
  team: Team;
  members: MemberNode[];
}

export interface Tree {
  /** Open teams that no listed participant called, each with its taskforces under their callers. */
  teams: TeamNode[];
  /** Solo sessions, with the taskforces they called. */
  solos: MemberNode[];
  /** Closed teams, flat, in ps --view's order. */
  closed: Team[];
}

/**
 * Open teams as a tree: a taskforce (an open team with `parent`) hangs under the member or solo of
 * that name; one whose caller is not listed (or would make a loop) stays at the top level.
 */
export function treeOf(view: View): Tree {
  const open = view.teams.filter((t) => t.status !== "closed");
  const closed = view.teams.filter((t) => t.status === "closed");
  // Which open team holds each participant name (solos are under "").
  const home = new Map<string, string>();
  for (const t of open) for (const m of t.members) if (!home.has(m.name)) home.set(m.name, t.id);
  for (const s of view.solos) if (!home.has(s.name)) home.set(s.name, "");

  /** Whether hanging `team` under its parent reaches `team` again going up (a loop). */
  const loops = (team: Team): boolean => {
    const seen = new Set<string>([team.id]);
    let at: string | undefined = home.get(team.parent);
    while (at) {
      if (at === team.id) return true;
      if (seen.has(at)) return false; // a loop above this team: those teams stay at the top level
      seen.add(at);
      const up = open.find((t) => t.id === at);
      at = up?.parent ? home.get(up.parent) : undefined;
    }
    return false;
  };
  const nested = new Set(open.filter((t) => t.parent && home.has(t.parent) && !loops(t)).map((t) => t.id));
  const calledBy = (name: string) => open.filter((t) => nested.has(t.id) && t.parent === name);

  const memberNode = (member: Member, depth: number): MemberNode => ({
    member,
    taskforces: depth > 8 ? [] : calledBy(member.name).map((t) => teamNode(t, depth + 1)),
  });
  const teamNode = (team: Team, depth: number): TeamNode => ({ team, members: team.members.map((m) => memberNode(m, depth)) });

  return {
    teams: open.filter((t) => !nested.has(t.id)).map((t) => teamNode(t, 0)),
    solos: view.solos.map((s) => memberNode(s, 0)),
    closed,
  };
}

/** The member or solo with this id, and its team (null for a solo). */
export function findMember(view: View, id: string): { member: Member; team: Team | null } | null {
  for (const team of view.teams) {
    const member = team.members.find((m) => m.id === id);
    if (member) return { member, team };
  }
  const solo = view.solos.find((s) => s.id === id);
  return solo ? { member: solo, team: null } : null;
}

/** Whether anything is in a turn, so the screen asks again soon. */
export const isBusy = (view: View | null): boolean => (view?.summary.working ?? 0) > 0;

// ---- Words and times --------------------------------------------------------------------------

/** "just now", "21m ago", "3h ago", "2d ago"; "" when the time is unknown. */
export function agoText(iso: string, now: number): string {
  const ms = Date.parse(iso);
  if (!iso || Number.isNaN(ms)) return "";
  const s = Math.max(0, Math.round((now - ms) / 1000));
  if (s < 45) return "just now";
  const min = Math.round(s / 60);
  if (min < 60) return `${min}m ago`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

/** "14:02" today, "Oct 9 14:02" on another day, local time; "" when unknown. */
export function clockText(iso: string, now: number): string {
  const ms = Date.parse(iso);
  if (!iso || Number.isNaN(ms)) return "";
  const t = new Date(ms);
  const two = (n: number) => String(n).padStart(2, "0");
  const hm = `${two(t.getHours())}:${two(t.getMinutes())}`;
  if (t.toDateString() === new Date(now).toDateString()) return hm;
  const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  return `${months[t.getMonth()]} ${t.getDate()} ${hm}`;
}

/** The colour a status draws in, as a theme colour name. */
export function statusColour(status: string): "statusSuccess" | "accent" | "statusWarning" | "foregroundMuted" {
  switch (status) {
    case "working":
      return "statusSuccess";
    case "idle":
      return "accent";
    case "stopped":
    case "permission":
    case "parked":
      return "statusWarning";
    default:
      return "foregroundMuted";
  }
}

/** The colour a notice kind draws in: the gate needs its person (failed, gate_lost) or is done. */
export function noticeColour(kind: string): "statusDanger" | "statusSuccess" | "accent" {
  switch (kind) {
    case "failed":
    case "gate_lost":
      return "statusDanger";
    case "settled":
      return "statusSuccess";
    default:
      return "accent";
  }
}

/** The colour an event kind draws in. */
export function eventColour(kind: string): "statusDanger" | "statusWarning" | "statusSuccess" | "foregroundMuted" {
  switch (kind) {
    case "deny":
      return "statusDanger";
    case "stop":
    case "exit":
    case "held":
    case "parked":
    case "gate_moved":
      return "statusWarning";
    case "spawn":
    case "resume":
      return "statusSuccess";
    default:
      return "foregroundMuted";
  }
}
