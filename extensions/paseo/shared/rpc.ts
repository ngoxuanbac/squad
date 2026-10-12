// The plugin's two RPCs and its one setting, shared by the daemon side and the app. Both RPCs only
// read: they run the squad CLI with --no-start, so looking never starts a squad daemon.
import { defineRpc, defineSettings } from "@getpaseo/plugin";
import { z } from "zod";

/**
 * Why squad gave nothing: not found at that path (missing), daemon not running (down), a squad
 * older (no --view, or an older view version) or newer (a newer view version) than this plugin, or
 * another failure.
 */
const Failure = z.object({ ok: z.literal(false), code: z.enum(["missing", "down", "older", "newer", "failed"] as const), error: z.string() });

/**
 * `squad ps --view --no-start`, its version checked on the daemon side and passed through as squad
 * wrote it; the app reads it with shared/view.ts readView.
 */
export const snapshotRpc = defineRpc({
  name: "squad.snapshot",
  input: z.object({}),
  output: z.union([z.object({ ok: z.literal(true), view: z.record(z.string(), z.unknown()) }), Failure]),
});

/** `squad tail <id> -n <lines> --view --no-start`: the last lines of one participant's log. */
export const tailRpc = defineRpc({
  name: "squad.tail",
  input: z.object({ id: z.string().min(1), lines: z.number().int().min(1).max(500).default(40) }),
  // `kind` stays a plain string: a kind a newer squad adds draws as plain text instead of failing.
  output: z.union([z.object({ ok: z.literal(true), lines: z.array(z.object({ kind: z.string(), text: z.string(), rest: z.string().optional() })) }), Failure]),
});

/** A squad binary the user chose; empty uses the one that installed the plugin, else `squad` on PATH. */
export const settings = defineSettings({
  id: "squad",
  scope: "host",
  version: 1,
  schema: z.object({ path: z.string().default("") }),
});
