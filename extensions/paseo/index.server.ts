import type { PluginServerContext } from "@getpaseo/plugin/server";
import { squadPath } from "./server/installed";
import { pickBin, runSquad } from "./server/squad";
import { settings, snapshotRpc, tailRpc } from "./shared/rpc";
import { parseJson, readTail, readView } from "./shared/view";

export default function contribute(server: PluginServerContext) {
  const stored = server.registerSettings(settings);
  const bin = async () => {
    const state = await stored.read();
    return pickBin(state.status === "ready" ? state.values.path : "", squadPath);
  };

  server.handle(snapshotRpc, async () => {
    const r = await runSquad(await bin(), ["ps", "--view"]);
    if (!r.ok) return r;
    const doc = parseJson(r.out, "ps");
    if (!doc.ok) return doc;
    const view = readView(doc.value);
    if (!view.ok) return view;
    return { ok: true as const, view: doc.value as Record<string, unknown> };
  });

  server.handle(tailRpc, async ({ id, lines }) => {
    const r = await runSquad(await bin(), ["tail", id, "-n", String(lines), "--view"]);
    if (!r.ok) return r;
    const doc = parseJson(r.out, "tail");
    if (!doc.ok) return doc;
    const tail = readTail(doc.value);
    return tail.ok ? { ok: true as const, lines: tail.value } : tail;
  });

  return () => {};
}
