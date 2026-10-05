import type { Role } from "../ledger/types.ts";
import type { Catalog, Harness } from "./catalog.ts";

export function providerId(role: Role, harness: Harness): string {
  return `squad-${harness.id}-${role}`;
}

export function roleOfProvider(provider: string, harness: Harness): Role | null {
  const base = provider.split("/")[0];
  for (const role of ["supervisor", "lead", "peer"] as const) {
    if (base === providerId(role, harness)) return role;
  }
  return null;
}

/** Harness model id of a short model name from the role files. */
export function modelId(harness: Harness, name: string): string {
  return harness.models[name]!.id;
}

/** Short model name of a model id or name Paseo sends, or undefined when the harness does not know it. */
export function modelName(harness: Harness, value: string | undefined): string | undefined {
  if (!value) return undefined;
  if (harness.models[value]) return value;
  return Object.keys(harness.models).find((name) => harness.models[name]!.id === value);
}

/**
 * The three provider entries squad keeps in Paseo's config. `models` replaces the harness's model list, so
 * Paseo only offers the role's models; `disallowedTools` removes the forbidden tools at provider level.
 * `command` starts the HC through squad's launcher, which refuses to run when squad's session_open hook
 * did not grant the session: without the plugin a squad provider never yields an HC with no role.
 */
export function providerEntries(harness: Harness, catalog: Catalog, command?: string[]): Record<string, Record<string, unknown>> {
  return Object.fromEntries(
    (["supervisor", "lead", "peer"] as const).map((role) => [
      providerId(role, harness),
      {
        extends: harness.baseProvider,
        label: `Squad · ${catalog.kinds[role]!.label}`,
        ...(command ? { command } : {}),
        env: command ? { SQUAD_ROLE: role, SQUAD_GRANT: "" } : { SQUAD_ROLE: role },
        models: catalog.kinds[role]!.models.map((name, index) => ({
          id: harness.models[name]!.id,
          label: harness.models[name]!.label,
          ...(index === 0 ? { isDefault: true } : {}),
        })),
        disallowedTools: catalog.forbidden,
        paseoTools: { enabled: false },
      },
    ]),
  );
}

/** Patch that brings Paseo's config in line with the entries, or null when it already matches. */
export function providerPatch(
  config: Record<string, unknown>,
  harness: Harness,
  catalog: Catalog,
  command?: string[],
): Record<string, unknown> | null {
  const current = (config.providers ?? {}) as Record<string, unknown>;
  const wanted = providerEntries(harness, catalog, command);
  const differs = Object.entries(wanted).some(([id, entry]) => JSON.stringify(pick(current[id], entry)) !== JSON.stringify(entry));
  return differs ? { providers: wanted } : null;
}

function pick(value: unknown, shape: Record<string, unknown>): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object") return undefined;
  return Object.fromEntries(Object.keys(shape).map((k) => [k, (value as Record<string, unknown>)[k]]));
}
