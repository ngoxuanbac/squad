import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { findRealGit, writeShims } from "../server/git/shim.ts";
import { git, tempRepo } from "./fakes.ts";
import { tempDir } from "./helpers.ts";

const bin = tempDir("bin");
writeShims(bin, process.execPath, findRealGit(bin));
const shim = path.join(bin, "git-shim.mjs");

function run(cwd: string, args: string[], worktree?: string) {
  const env: NodeJS.ProcessEnv = { ...process.env, GIT_AUTHOR_NAME: "t", GIT_AUTHOR_EMAIL: "t@t", GIT_COMMITTER_NAME: "t", GIT_COMMITTER_EMAIL: "t@t" };
  if (worktree) env.SQUAD_WORKTREE = worktree;
  else delete env.SQUAD_WORKTREE;
  const r = spawnSync(process.execPath, [shim, ...args], { cwd, env, encoding: "utf8" });
  return { code: r.status, err: r.stderr };
}

test("shim chặn lệnh nhánh, merge, đẩy và stash, kể cả qua alias", () => {
  const repo = tempRepo();
  for (const args of [["push"], ["pull"], ["fetch"], ["checkout", "-b", "x"], ["switch", "main"], ["merge", "main"], ["rebase", "main"], ["stash"], ["worktree", "list"], ["update-ref", "refs/heads/x", "HEAD"], ["reset", "--hard"], ["branch", "-D", "main"]]) {
    const r = run(repo, args, repo);
    assert.equal(r.code, 1, args.join(" "));
    assert.match(r.err, /squad:/);
  }
  assert.equal(run(repo, ["-c", "alias.p=push", "p"], repo).code, 1);
  git(repo, "config", "alias.pp", "push");
  assert.equal(run(repo, ["pp"], repo).code, 1);
  git(repo, "config", "alias.boom", "!echo hi");
  assert.match(run(repo, ["boom"], repo).err, /alias chạy lệnh shell/);
  assert.match(run(repo, ["--git-dir=x", "status"], repo).err, /git-dir/);
});

test("shim cho đọc mọi nơi, chỉ cho ghi trong worktree của HC", () => {
  const repo = tempRepo();
  const other = tempRepo();
  assert.equal(run(repo, ["status"], other).code, 0);
  assert.equal(run(repo, ["log", "--oneline"], other).code, 0);
  fs.writeFileSync(path.join(repo, "b.txt"), "b\n");
  const outside = run(repo, ["add", "b.txt"], other);
  assert.equal(outside.code, 1);
  assert.match(outside.err, /chỉ được ghi trong worktree/);
  assert.equal(run(repo, ["add", "b.txt"], repo).code, 0);
  assert.equal(run(repo, ["commit", "-qm", "b"], repo).code, 0);
  assert.equal(run(other, ["-C", repo, "commit", "--allow-empty", "-qm", "c"], other).code, 1);
});

test("shim có bản sh và cmd; gh và paseo bị thay bằng script báo lỗi", () => {
  for (const name of ["git", "git.cmd", "gh", "gh.cmd", "paseo", "paseo.cmd"]) assert.ok(fs.existsSync(path.join(bin, name)), name);
  assert.match(fs.readFileSync(path.join(bin, "git"), "utf8"), /^#!\/bin\/sh\nELECTRON_RUN_AS_NODE=1 exec /);
  assert.match(fs.readFileSync(path.join(bin, "gh"), "utf8"), /bị chặn/);
});
