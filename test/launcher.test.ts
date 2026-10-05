import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { writeLauncher } from "../server/hc/launcher.ts";
import { tempDir } from "./helpers.ts";

/** A launcher plus a fake harness that echoes its stdin, reports leaked env, and exits 7. */
function fixture() {
  const dir = tempDir("launcher");
  const launcher = writeLauncher(dir);
  const fake = path.join(dir, "fake-claude.mjs");
  fs.writeFileSync(
    fake,
    `let input = "";
process.stdin.on("data", (c) => (input += c));
process.stdin.on("end", () => {
  process.stdout.write(JSON.stringify({ input, args: process.argv.slice(2), grant: process.env.SQUAD_GRANT ?? null, electron: process.env.ELECTRON_RUN_AS_NODE ?? null }));
  process.exit(7);
});
`,
  );
  // The tests may themselves run inside a Paseo agent; start from an env without its marks.
  const base: Record<string, string | undefined> = { ...process.env };
  delete base.PASEO_AGENT_ID;
  delete base.SQUAD_GRANT;
  const run = (env: Record<string, string>) =>
    spawnSync(process.execPath, [launcher, process.execPath, fake, "--model", "x"], {
      input: "hello",
      encoding: "utf8",
      env: { ...base, ELECTRON_RUN_AS_NODE: "1", ...env },
    });
  return { run };
}

test("launcher: agent Paseo không có dấu của squad thì không khởi động", () => {
  const { run } = fixture();
  const result = run({ PASEO_AGENT_ID: "agent-1" });
  assert.equal(result.status, 2);
  assert.match(result.stderr, /plugin squad không chạy/);
  assert.equal(result.stdout, "");
});

test("launcher: có dấu thì chạy binary thật, chuyển stdin, args và mã thoát, không lộ dấu và cờ Electron", () => {
  const { run } = fixture();
  const result = run({ PASEO_AGENT_ID: "agent-1", SQUAD_GRANT: "g".repeat(32) });
  assert.equal(result.status, 7, result.stderr);
  const seen = JSON.parse(result.stdout);
  assert.equal(seen.input, "hello");
  assert.deepEqual(seen.args, ["--model", "x"]);
  assert.equal(seen.grant, null);
  assert.equal(seen.electron, null);
});

test("launcher: Paseo dò binary (không có PASEO_AGENT_ID) thì cho qua", () => {
  const { run } = fixture();
  const result = run({});
  assert.equal(result.status, 7, result.stderr);
});
