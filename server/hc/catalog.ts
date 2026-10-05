import type { Role } from "../ledger/types.ts";
import { TOOL_SCHEMAS } from "../../shared/protocol.ts";

/**
 * One kind of HC from roles.json: a role, or a variant of the Peer role (`like: "peer"`); core has none yet.
 * A variant runs on the Peer provider; it has its own prompt, tools and settings.
 */
export interface HcKind {
  name: string;
  role: Role;
  label: string;
  /** Model names from the harness's `models`; the first is the default. */
  models: string[];
  squadTools: string[];
  prompt: string;
  /** The only tools the HC has: `permissions.allow` of its settings file. */
  allow: string[];
  /** Patterns refused inside the allowed tools: `permissions.deny` of its settings file. */
  deny: string[];
  /** Words its prompt must not contain. */
  hidesWords: string[];
}

export interface Catalog {
  kinds: Record<string, HcKind>;
  /** `permissions.deny` of the harness's settings.json, for every hc. */
  commonDeny: string[];
  /** Tools no HC may have: the bare tool names in `commonDeny`. */
  forbidden: string[];
}

export interface Harness {
  id: string;
  baseProvider: string;
  /** Name of the harness executable the HC launcher runs. */
  binary: string;
  modeId: string;
  /** Every tool name the harness has. A misspelt name would be dropped silently by the agent, so it fails the load. */
  knownTools: string[];
  /** CLI flag that lists the only tools an HC has. */
  toolsArg: string;
  /**
   * CLI flags every HC gets: only squad's MCP server, and only the project's settings, so the Human's own
   * hooks, permission rules, CLAUDE.md and plugins stay out of squad HCs. null is a flag without a value.
   */
  hcArgs: Record<string, string | null>;
  /** Short model names roles.json uses, mapped to the harness's model ids. */
  models: Record<string, { id: string; label: string }>;
}

export class CatalogError extends Error {}

const ROLES: Role[] = ["supervisor", "lead", "peer"];
/** Kinds squad needs: the 3 roles. */
const REQUIRED = ROLES;

interface RoleEntry {
  role?: string;
  like?: string;
  label?: string;
  models?: string[];
  squadTools?: string[];
  prompt?: string;
  hidesWords?: string[];
}

interface Settings {
  permissions?: { allow?: string[]; deny?: string[] };
}

function json<T>(content: Record<string, string>, file: string): T {
  const text = content[file];
  if (text === undefined) throw new CatalogError(`thiếu file ${file}`);
  try {
    return JSON.parse(text) as T;
  } catch (error) {
    throw new CatalogError(`${file}: JSON lỗi: ${error instanceof Error ? error.message : String(error)}`);
  }
}

function toolName(entry: string): string {
  return entry.replace(/\(.*$/, "");
}

export function loadHarness(content: Record<string, string>, id: string): Harness {
  return json<Harness>(content, `server/harness/${id}/harness.json`);
}

/** Builds and checks the catalog. A bad file fails the whole load: the plugin must not staff a role it cannot enforce. */
export function loadCatalog(content: Record<string, string>, harness: Harness): Catalog {
  const dir = `server/harness/${harness.id}`;
  const commonDeny = json<Settings>(content, `${dir}/settings.json`).permissions?.deny ?? [];
  const forbidden = commonDeny.filter((rule) => !rule.includes("("));
  const unknownTools = (file: string, names: string[]) => {
    const unknown = names.map(toolName).filter((tool) => !harness.knownTools.includes(tool));
    if (!unknown.length) return;
    const hints = unknown.map((tool) => {
      const near = harness.knownTools.find((known) => known.toLowerCase() === tool.toLowerCase());
      return near ? `${tool} (ý là ${near}?)` : tool;
    });
    throw new CatalogError(`${file}: harness ${harness.id} không có tool ${hints.join(", ")}`);
  };
  unknownTools(`${dir}/settings.json`, commonDeny);

  const entries = json<{ roles?: RoleEntry[] }>(content, "roles.json").roles ?? [];
  const byName = new Map<string, RoleEntry>();
  for (const entry of entries) {
    if (!entry.role || !/^[a-z][a-z0-9-]*$/.test(entry.role)) throw new CatalogError(`roles.json: role không hợp lệ: ${entry.role}`);
    if (byName.has(entry.role)) throw new CatalogError(`roles.json: ${entry.role} khai hai lần`);
    byName.set(entry.role, entry);
  }
  for (const name of REQUIRED) {
    if (!byName.has(name)) throw new CatalogError(`roles.json: thiếu ${name}`);
  }
  for (const role of ROLES) {
    if (byName.get(role)!.like) throw new CatalogError(`roles.json: ${role} là vai trò gốc, không có like`);
  }

  const kinds: Record<string, HcKind> = {};
  for (const entry of entries) {
    const name = entry.role!;
    const where = `roles.json ${name}`;
    const isRole = (ROLES as string[]).includes(name);
    if (!isRole && entry.like !== "peer") throw new CatalogError(`${where}: chỉ có 3 vai trò; loại HC khác phải là like: "peer"`);
    const role = (isRole ? name : "peer") as Role;
    const base = byName.get(role)!;
    const models = entry.models ?? base.models ?? [];
    if (!models.length) throw new CatalogError(`${where}: thiếu models`);
    const unknownModels = models.filter((m) => !harness.models[m]);
    if (unknownModels.length) throw new CatalogError(`${where}: harness ${harness.id} không có model ${unknownModels.join(", ")}`);
    const outside = models.filter((m) => !(base.models ?? []).includes(m));
    if (outside.length) throw new CatalogError(`${where}: model ${outside.join(", ")} ngoài models của ${role}, provider của ${role} không có`);
    const squadTools = entry.squadTools ?? [];
    const badTools = squadTools.filter((t) => !(t in TOOL_SCHEMAS));
    if (badTools.length) throw new CatalogError(`${where}: không có tool squad ${badTools.join(", ")}`);
    if (!entry.prompt) throw new CatalogError(`${where}: thiếu prompt`);
    const prompt = content[entry.prompt]?.trim();
    if (prompt === undefined) throw new CatalogError(`${where}: không có file ${entry.prompt}`);
    const hidesWords = entry.hidesWords ?? (isRole ? [] : base.hidesWords ?? []);
    const leaked = hidesWords.filter((word) => new RegExp(`\\b${word}\\b`, "i").test(prompt));
    if (leaked.length) throw new CatalogError(`${entry.prompt}: không được nhắc tới ${leaked.join(", ")}`);

    const settingsFile = `${dir}/settings/${name}.settings.json`;
    const permissions = json<Settings>(content, settingsFile).permissions ?? {};
    const allow = permissions.allow ?? [];
    const deny = permissions.deny ?? [];
    unknownTools(settingsFile, [...allow, ...deny]);
    const patterned = allow.filter((tool) => tool.includes("("));
    if (patterned.length) throw new CatalogError(`${settingsFile}: allow chỉ ghi tên tool, mẫu chỉ dùng trong deny: ${patterned.join(", ")}`);
    const banned = allow.filter((tool) => forbidden.includes(tool));
    if (banned.length) throw new CatalogError(`${settingsFile}: tool bị cấm trong allow: ${banned.join(", ")}`);

    kinds[name] = {
      name,
      role,
      label: entry.label ?? name,
      models,
      squadTools,
      prompt,
      allow,
      deny,
      hidesWords,
    };
  }
  return { kinds, commonDeny, forbidden };
}

/** The kind of an HC: its role. */
export function kindOf(catalog: Catalog, hc: { role: Role }): HcKind {
  return catalog.kinds[hc.role]!;
}

/** The model an HC runs on: the asked name if its kind allows it, else the kind's first. */
export function pickModel(kind: HcKind, asked: string | undefined): string {
  return asked && kind.models.includes(asked) ? asked : kind.models[0]!;
}

/** What desk passes as `config.options` when it creates the agent. */
export function agentOptions(catalog: Catalog, harness: Harness, kind: HcKind, paths: { state: string; home: string }): Record<string, unknown> {
  const common = catalog.commonDeny.map((rule) => rule.replaceAll("{state}", toSlash(paths.state)).replaceAll("{home}", toSlash(paths.home)));
  const deny = [...new Set([...common, ...kind.deny])];
  return {
    allowedTools: kind.allow,
    extraArgs: { [harness.toolsArg]: kind.allow.join(","), ...harness.hcArgs },
    settings: { permissions: { deny } },
  };
}

function toSlash(value: string): string {
  return value.replaceAll("\\", "/");
}
