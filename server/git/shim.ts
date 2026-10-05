
import fs from "node:fs";
import path from "node:path";

/** The git shim run by Node. `__REAL_GIT__` is replaced with the real git path when written. */
export const SHIM_SOURCE = String.raw`import { spawnSync } from "node:child_process";
import path from "node:path";
const REAL = __REAL_GIT__;
const args = process.argv.slice(2);
const refuse = (why) => {
  process.stderr.write("squad: " + why + "\n");
  process.exit(1);
};
const norm = (p) => path.resolve(p).replaceAll("\\", "/").replace(/\/+$/, "").toLowerCase();
let cwd = process.cwd();
const config = [];
let i = 0;
while (i < args.length && args[i].startsWith("-")) {
  const a = args[i];
  if (a === "-C") { cwd = path.resolve(cwd, args[i + 1] ?? "."); i += 2; continue; }
  if (a === "-c") { config.push(args[i + 1] ?? ""); i += 2; continue; }
  if (a.startsWith("--git-dir") || a.startsWith("--work-tree")) refuse("không được đổi --git-dir hay --work-tree");
  i += 1;
}
let cmd = args[i] ?? "";
let rest = args.slice(i + 1);
const inline = config.find((c) => c.startsWith("alias." + cmd + "="));
const alias = inline ? inline.slice(inline.indexOf("=") + 1) : (cmd ? spawnSync(REAL, ["config", "--get", "alias." + cmd], { cwd, encoding: "utf8" }).stdout?.trim() ?? "" : "");
if (alias) {
  if (alias.startsWith("!")) refuse("alias chạy lệnh shell bị chặn");
  const parts = alias.split(/\s+/).filter(Boolean);
  cmd = parts[0] ?? "";
  rest = [...parts.slice(1), ...rest];
}
const BLOCKED = new Set(["push", "pull", "fetch", "checkout", "switch", "merge", "rebase", "stash", "worktree", "update-ref", "filter-branch", "replace", "symbolic-ref"]);
if (BLOCKED.has(cmd)) refuse("git " + cmd + " bị chặn; nhánh và merge do squad lo. Bỏ thay đổi một file thì dùng git restore.");
if (cmd === "reset" && rest.includes("--hard")) refuse("git reset --hard bị chặn");
if (cmd === "branch" && rest.some((r) => ["-f", "--force", "-D", "-d", "--delete", "-m", "-M", "-c", "-C"].includes(r))) refuse("đổi hay xóa nhánh bị chặn");
const WRITE = new Set(["commit", "add", "rm", "mv", "restore", "reset", "tag", "cherry-pick", "revert", "apply", "am", "clean", "notes", "branch"]);
const own = process.env.SQUAD_WORKTREE;
if (own && WRITE.has(cmd)) {
  const top = spawnSync(REAL, ["rev-parse", "--show-toplevel"], { cwd, encoding: "utf8" }).stdout?.trim() ?? "";
  if (!top || norm(top) !== norm(own)) refuse("chỉ được ghi trong worktree của bạn: " + own);
}
const run = spawnSync(REAL, args, { cwd: process.cwd(), stdio: "inherit" });
process.exit(run.status ?? 1);
`;

/** Finds the real git on PATH, skipping the shim directory. */
export function findRealGit(binDir: string): string {
  const key = Object.keys(process.env).find((k) => k.toUpperCase() === "PATH") ?? "PATH";
  const skip = path.resolve(binDir).toLowerCase();
  const names = process.platform === "win32" ? ["git.exe", "git.cmd"] : ["git"];
  for (const dir of (process.env[key] ?? "").split(path.delimiter)) {
    if (!dir || path.resolve(dir).toLowerCase() === skip) continue;
    for (const name of names) {
      const candidate = path.join(dir, name);
      if (fs.existsSync(candidate) && fs.statSync(candidate).isFile()) return candidate;
    }
  }
  throw new Error("không tìm thấy git trên PATH");
}

/** Writes the shim and the refusing stand-ins for gh and paseo into `binDir`. */
export function writeShims(binDir: string, runtime: string, realGit: string): void {
  fs.mkdirSync(binDir, { recursive: true });
  const shim = path.join(binDir, "git-shim.mjs");
  fs.writeFileSync(shim, SHIM_SOURCE.replace("__REAL_GIT__", JSON.stringify(realGit)));
  const slash = (p: string) => p.replaceAll("\\", "/");
  const sh = (body: string) => `#!/bin/sh\n${body}\n`;
  const write = (name: string, content: string) => fs.writeFileSync(path.join(binDir, name), content, { mode: 0o755 });
  write("git", sh(`ELECTRON_RUN_AS_NODE=1 exec "${slash(runtime)}" "${slash(shim)}" "$@"`));
  write("git.cmd", `@echo off\r\nset ELECTRON_RUN_AS_NODE=1\r\n"${runtime}" "${shim}" %*\r\n`);
  for (const tool of ["gh", "paseo"]) {
    write(tool, sh(`echo "squad: ${tool} bị chặn trong HC squad" >&2\nexit 1`));
    write(`${tool}.cmd`, `@echo off\r\necho squad: ${tool} bị chặn trong HC squad 1>&2\r\nexit /b 1\r\n`);
  }
}

/** Env for an HC's session: the shim directory first on PATH, and the worktree it may write. */
export function hcEnv(env: Record<string, string>, binDir: string, worktree: string | undefined): Record<string, string> {
  const key = Object.keys(env).find((k) => k.toUpperCase() === "PATH") ?? Object.keys(process.env).find((k) => k.toUpperCase() === "PATH") ?? "PATH";
  const base = env[key] ?? process.env[key] ?? "";
  const next: Record<string, string> = { ...env, [key]: `${binDir}${path.delimiter}${base}` };
  if (worktree) next.SQUAD_WORKTREE = worktree;
  return next;
}
