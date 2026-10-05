import type { Hc, Ledger, Squad } from "../ledger/types.ts";
import { nextId } from "./context.ts";

/*
 * Every name squad makes, in one place. Ids count by level and carry their parent's id: squad s1 per repo; task
 * s1-t2, question s1-a1, constraint s1-c1 per squad; debate s1-t2-d1, evidence s1-t2-e1 per
 * task. Titles read id, duty, work. Names are stored in the ledger where they are made, so a later
 * change of these rules leaves running squads on the branches and folders they already have.
 */

/** The workspace's one Supervisor HC, and its agent's title. */
export const SUPERVISOR = "supervisor";

/** A new id under `parent`: "t" under s1 gives s1-t1, "d" under s1-t2 gives s1-t2-d1. */
export function newId(ledger: Ledger, parent: string, kind: "t" | "a" | "d" | "e"): string {
  return nextId(ledger, `${parent}-${kind}`);
}

/** The next squad number no branch in the repo already uses, so two workspaces on one repo never share one. */
export function newSquadId(ledger: Ledger, branches: (glob: string) => string[]): string {
  let id = nextId(ledger, "S");
  while (branches(`squad/${id}-*`).length || branches(`task/${id}-*`).length) id = nextId(ledger, "S");
  return id;
}

export function squadNames(id: string, slug: string) {
  const name = `${id}-${slug}`;
  return { name, branch: `squad/${name}`, lead: `${id}-lead`, leadDir: `${name}/lead` };
}

export function taskNames(squad: Squad, id: string, title: string) {
  const name = `${id}-${title}`;
  return { branch: `task/${name}`, dir: `${squad.name}/${name}` };
}

export const constraintId = (squad: string, n: number) => `${squad}-c${n}`;

/**
 * An id in full and lowercase. Inside its squad an HC may write ids short: "t2", "T2-D1" or "lead" from an HC of
 * s1 mean "s1-t2", "s1-t2-d1" and "s1-lead". The Supervisor, outside any squad, writes them in full.
 */
export function fullId(raw: string, squad: string | undefined): string {
  const id = raw.trim().toLowerCase();
  return squad && id !== SUPERVISOR && !/^s\d+(-|$)/.test(id) ? `${squad}-${id}` : id;
}

/**
 * The title of an HC's agent, also used for its workspace so the two always match: id, duty, then what it works
 * on. "s1 lead pricing", "s1-t2 peer format".
 */
export function hcTitle(ledger: Ledger, hc: Hc): string {
  const squad = hc.squad ? ledger.squads[hc.squad] : undefined;
  const task = hc.task ? ledger.tasks[hc.task] : undefined;
  if (hc.role === "lead") return squad ? `${squad.id} lead ${squad.slug}` : hc.id;
  return task ? `${task.id} peer ${task.title}` : hc.id;
}

/** The git identity of a Peer's commits, so they read "squad s1-t2" instead of the Human's name. */
export function commitAuthor(hc: string): { name: string; email: string } {
  return { name: `squad ${hc}`, email: `squad.${hc}@squad.local` };
}
