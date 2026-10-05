import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createHash } from "node:crypto";
import { writeAtomic } from "./ledger/store.ts";

export const DEFAULT_PORT = 6810;

/** Plugin data lives outside every repo: SQUAD_STATE, else the OS data directory. */
export function stateDir(): string {
  if (process.env.SQUAD_STATE) return process.env.SQUAD_STATE;
  if (process.platform === "win32") return path.join(process.env.LOCALAPPDATA ?? path.join(os.homedir(), "AppData", "Local"), "squad");
  return path.join(process.env.XDG_DATA_HOME ?? path.join(os.homedir(), ".local", "share"), "squad");
}

export interface StateConfig {
  port: number;
  /** Peers working at once in one workspace, across its squads. */
  maxPeers: number;
  /** CI runs at once on this machine. */
  ciAtOnce: number;
}

export const DEFAULT_MAX_PEERS = 4;

export function readConfig(state: string): StateConfig {
  const file = path.join(state, "config.json");
  const count = (value: unknown, fallback: number) => (typeof value === "number" && Number.isInteger(value) && value > 0 ? value : fallback);
  const defaults: StateConfig = { port: DEFAULT_PORT, maxPeers: DEFAULT_MAX_PEERS, ciAtOnce: Math.max(1, Math.floor(os.cpus().length / 2)) };
  try {
    const parsed = JSON.parse(fs.readFileSync(file, "utf8")) as Partial<StateConfig>;
    return {
      port: count(parsed.port, defaults.port),
      maxPeers: count(parsed.maxPeers, defaults.maxPeers),
      ciAtOnce: count(parsed.ciAtOnce, defaults.ciAtOnce),
    };
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    writeAtomic(file, JSON.stringify(defaults, null, 1) + "\n");
    return defaults;
  }
}

export interface Project {
  repo: string;
  base: string;
  rules: { id: string; text: string }[];
}

export function wsDir(state: string, ws: string): string {
  return path.join(state, "workspaces", ws);
}

export function readProject(state: string, ws: string): Project | null {
  try {
    return JSON.parse(fs.readFileSync(path.join(wsDir(state, ws), "project.json"), "utf8")) as Project;
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
    throw error;
  }
}

export function writeProject(state: string, ws: string, project: Project): void {
  writeAtomic(path.join(wsDir(state, ws), "project.json"), JSON.stringify(project, null, 1) + "\n");
}

/** The project's name in paths and workspace titles, as seatworks makes it: repo folder name and a short hash. */
export function projectSlug(repo: string): string {
  const name =
    path
      .basename(repo)
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-|-$/g, "") || "project";
  return `${name}-${createHash("sha1").update(path.resolve(repo)).digest("hex").slice(0, 6)}`;
}

/** Working copy `slot` of a project: <state>/worktrees/<project slug>/<slot>. */
export function worktreePath(state: string, repo: string, slot: string): string {
  return path.join(state, "worktrees", projectSlug(repo), slot);
}
