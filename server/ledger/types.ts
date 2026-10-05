import type { BriefT, ChallengeT, EvidenceT, SourceT } from "../../shared/protocol.ts";

export type Role = "supervisor" | "lead" | "peer";

export interface Hc {
  id: string;
  role: Role;
  ws: string;
  squad?: string;
  task?: string;
  state: "open" | "filled" | "closed";
  agentId?: string;
  secretHash?: string;
  /** Paseo workspace made for this HC's worktree, archived with the hc. */
  workspace?: string;
  /** First message for a pending HC, kept so a restart can still create it. */
  kickoff?: string;
}

export interface Constraint {
  id: string;
  text: string;
  source: SourceT;
}

export interface Squad {
  /** "s1": squads count per repo. */
  id: string;
  ws: string;
  slug: string;
  /** "s1-pricing": the squad's branch "squad/s1-pricing" and the folder of its working copies. */
  name: string;
  branch: string;
  goal: string;
  acceptance: string[];
  constraints: Constraint[];
  model?: string;
  state: "open" | "reporting" | "ready" | "closed";
  /** The Lead was refused for lack of Peers and waits for one to free up. */
  waitingForHc?: boolean;
  supervisor: string;
  lead: string;
  /** Read-only worktree of the Lead, detached at the squad branch head and moved after every merge. */
  leadWorktree: string;
  report?: { summary: string; approved: string[]; open: string[]; risks: string[] };
}

export type TaskState =
  | "proposing"
  | "debating"
  | "running"
  | "ci_running"
  | "ci_done"
  | "approved"
  | "merged";

export interface StoredEvidence {
  id: string;
  hash: string;
  by: string;
  item: EvidenceT;
}

export interface Proposal {
  approach: string;
  steps: string[];
  risks: string[];
  assumptions: string[];
  questions: string[];
  hash: string;
}

export interface CiResult {
  command: string;
  claimed?: { exit: number; output: string };
  actual: { exit: number; output: string };
  mismatch: boolean;
}

export interface Task {
  id: string;
  squad: string;
  hc: string;
  title: string;
  briefs: BriefT[];
  base: string;
  branch: string;
  worktree: string;
  state: TaskState;
  proposal?: Proposal;
  handoff?: {
    sha: string;
    outcome: "complete" | "partial";
    summary: string;
    claimed: { command: string; exit: number; output: string }[];
    skipped: string[];
    unknowns: string[];
    discovered: string[];
    outOfScope: string[];
  };
  ci?: { sha: string; results: CiResult[]; failed: boolean };
  /** Brief changes the Lead made with amend_task, outside any debate. Older ledgers lack it. */
  amends?: { version: number; reason: string; at: string }[];
  verdicts: { action: "approve" | "request_changes"; sha: string; at: string; overFailed?: string }[];
}

export type DebateState =
  | "open"
  | "held"
  | "rebutted"
  | "deadlocked"
  | "decided"
  | "conceded"
  | "withdrawn"
  | "escalated";

export interface DebateRound {
  by: "lead" | "peer";
  action: "challenge" | "hold" | "rebut" | "concede" | "decide" | "withdraw" | "escalate" | "answer";
  evidence: string[];
  text: string;
  at: string;
}

export interface Debate {
  id: string;
  task: string;
  challenge: ChallengeT;
  state: DebateState;
  holds: number;
  evidence: StoredEvidence[];
  rounds: DebateRound[];
  decision?: { side: "lead" | "peer" | "supervisor"; reason: string };
  ask?: string;
}

export interface Ask {
  id: string;
  from: string;
  to: string;
  question: string;
  bestGuess?: string;
  /** `dropped`: the HC that asked is gone, so nobody would read an answer. */
  state: "open" | "answered" | "dropped";
  answer?: string;
  debate?: string;
  concern?: { task: string; index: number };
}

export interface Ledger {
  version: 1;
  seq: number;
  counters?: Record<string, number>;
  hcs: Record<string, Hc>;
  squads: Record<string, Squad>;
  tasks: Record<string, Task>;
  debates: Record<string, Debate>;
  asks: Record<string, Ask>;
}

export function emptyLedger(): Ledger {
  return { version: 1, seq: 0, hcs: {}, squads: {}, tasks: {}, debates: {}, asks: {} };
}
