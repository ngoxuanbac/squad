import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { squadPath } from "../server/installed.ts";
import { DOWN_TEXT, pickBin, runSquad } from "../server/squad.ts";

// A fake squad: node itself, running a script given with -e (runSquad appends --no-start after its "--").
const node = process.execPath;
const fake = (script: string) => ["-e", script, "--"];

test("installed.ts is the one line the Go installer rewrites", () => {
  assert.equal(squadPath, "", "the source tree ships no machine path");
  assert.equal(readFileSync(new URL("../server/installed.ts", import.meta.url), "utf8").replace(/\r?\n$/, ""), 'export const squadPath = "";');
});

test("the binary is the user's setting, else the installing squad, else squad on PATH", () => {
  assert.equal(pickBin("  C:\\tools\\squad.exe ", "C:\\squad\\squad.exe"), "C:\\tools\\squad.exe");
  assert.equal(pickBin("", "C:\\squad\\squad.exe"), "C:\\squad\\squad.exe");
  assert.equal(pickBin(" ", ""), "squad");
});

test("always passes --no-start, last", async () => {
  const r = await runSquad(node, fake("console.log(JSON.stringify(process.argv.slice(1)))"));
  assert.ok(r.ok);
  assert.deepEqual(JSON.parse(r.out), ["--no-start"]);
});

test("a stopped daemon is down", async () => {
  const r = await runSquad(node, fake(`console.error("squad: ${DOWN_TEXT} (run squad ps to start it)"); process.exit(1)`));
  assert.equal(r.ok || r.code, "down");
  assert.match(r.ok ? "" : r.error, /Squad daemon is not running/);
});

test("an old squad without --view is older", async () => {
  const r = await runSquad(node, fake(`console.error("flag provided but not defined: -view\\nUsage of ps:"); process.exit(2)`));
  assert.equal(r.ok || r.code, "older");
  assert.match(r.ok ? "" : r.error, /no --view/);
});

test("a binary that is not there is missing", async () => {
  const r = await runSquad(join(tmpdir(), "no-such-dir-squad", "squad"), ["ps"]);
  assert.equal(r.ok || r.code, "missing");
});

test("another failure says what squad said; a hang times out", async () => {
  const other = await runSquad(node, fake(`console.error("squad: no participant named x"); process.exit(1)`));
  assert.deepEqual([other.ok || other.code, other.ok ? "" : other.error], ["failed", "squad: no participant named x"]);
  const slow = await runSquad(node, fake("setTimeout(() => {}, 5000)"), 300);
  assert.equal(slow.ok || slow.code, "failed");
  assert.match(slow.ok ? "" : slow.error, /did not answer/);
});
