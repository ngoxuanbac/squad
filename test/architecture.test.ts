import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { ROOT } from "./helpers.ts";

function sources(dir: string): string[] {
  const full = path.join(ROOT, dir);
  if (!fs.existsSync(full)) return [];
  return fs.readdirSync(full, { recursive: true, encoding: "utf8" })
    .filter((f) => /\.tsx?$/.test(f))
    .map((f) => path.join(full, f));
}

function imports(file: string): string[] {
  const text = fs.readFileSync(file, "utf8");
  return [...text.matchAll(/(?:import|export)[^"']*?from\s+["']([^"']+)["']/g)].map((m) => m[1]!);
}

test("server và shared không import client; shared chỉ import shared và zod", () => {
  for (const file of [...sources("server"), ...sources("shared")]) {
    for (const spec of imports(file)) {
      assert.ok(!spec.includes("/client/") && !spec.startsWith("../client") && !spec.includes("plugin/client"), `${file} import ${spec}`);
    }
  }
  for (const file of sources("shared")) {
    for (const spec of imports(file)) {
      assert.ok(spec === "zod" || spec.startsWith("./"), `${file} import ${spec}`);
    }
  }
});

test("lõi desk và ledger không gọi tên harness hay Paseo", () => {
  for (const file of [...sources("server/desk"), ...sources("server/ledger")]) {
    const text = fs.readFileSync(file, "utf8");
    assert.doesNotMatch(text, /\bclaude\b|\bcodex\b|@getpaseo/i, file);
  }
});
