import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { readTail, readView } from "../shared/view.ts";

// The golden files are what squad's own ps --view and tail --view print (e2e/TestViewContract writes
// them from a real daemon). The plugin must read every field squad prints, and squad must print
// every field the plugin reads: read back, a document is the same, field for field.
const golden = (name: string): Record<string, unknown> =>
  JSON.parse(readFileSync(new URL(`../../../e2e/testdata/${name}`, import.meta.url), "utf8"));

test("reads squad's ps --view as it is", () => {
  const doc = golden("ps.view.json");
  const r = readView(doc);
  assert.ok(r.ok, r.ok ? "" : r.error);
  assert.deepEqual(r.value, doc);
});

test("reads squad's tail --view as it is", () => {
  const doc = golden("tail.view.json");
  const r = readTail(doc);
  assert.ok(r.ok, r.ok ? "" : r.error);
  assert.deepEqual(r.value, doc.lines);
});
