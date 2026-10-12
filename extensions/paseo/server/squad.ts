// Runs the squad CLI: execFile (no shell), a timeout, no console window, and always --no-start so a
// read never starts a squad daemon. Only node: imports, so `node --test` loads it as is.
//
// Windows: execFile without a shell goes through libuv, which tries the name as given and then with
// ".com" and ".exe" appended, both for a bare name searched on PATH and for a full path. So "squad"
// finds squad.exe on PATH and "C:\tools\squad" finds C:\tools\squad.exe. A .cmd/.bat shim cannot run
// without a shell (Node refuses it with EINVAL); that is reported with a hint to point at squad.exe.
import { execFile } from "node:child_process";

export type Failure = { ok: false; code: "missing" | "down" | "older" | "failed"; error: string };
export type Result = { ok: true; out: string } | Failure;

export const TIMEOUT_MS = 8000;

/** What squad prints on stderr when its daemon is down and --no-start keeps it from starting one. */
export const DOWN_TEXT = "the squad daemon is not running";

/** The binary to run: the user's setting, else the squad that installed the plugin, else PATH. */
export function pickBin(setting: string, installed: string): string {
  return setting.trim() || installed.trim() || "squad";
}

/** The first lines of what squad said, short enough for a notice. */
function firstLines(text: string): string {
  const lines = text.split(/\r?\n/).filter((l) => l.trim() !== "");
  const head = lines.slice(0, 4).join("\n");
  return head.length > 600 ? head.slice(0, 600) + "…" : head;
}

/** Why a run gave nothing, from the error, squad's stderr and the binary it ran. */
export function failureOf(bin: string, error: NodeJS.ErrnoException & { killed?: boolean }, stderr: string, timeoutMs: number): Failure {
  const said = stderr.trim();
  if (error.code === "ENOENT" || error.code === "EACCES") {
    return { ok: false, code: "missing", error: `squad was not found at "${bin}". Install squad, or set its full path in the Squad settings.` };
  }
  if (error.code === "EINVAL" && /\.(cmd|bat)$/i.test(bin)) {
    return { ok: false, code: "failed", error: `"${bin}" is a script; the plugin runs squad without a shell. Set the Squad setting to squad.exe itself.` };
  }
  if (said.includes(DOWN_TEXT)) {
    return { ok: false, code: "down", error: "Squad daemon is not running. Run `squad ps` to start it." };
  }
  const flag = /flag provided but not defined: -+(no-start|view|n)\b/.exec(said)?.[1];
  if (flag) {
    return { ok: false, code: "older", error: `The squad at "${bin}" is older than this plugin (it has no --${flag}). Update squad, or point the Squad setting at a newer one.` };
  }
  if (error.killed || error.code === "ETIMEDOUT") {
    return { ok: false, code: "failed", error: `squad did not answer within ${Math.round(timeoutMs / 1000)}s.` };
  }
  return { ok: false, code: "failed", error: firstLines(said) || error.message };
}

/** Runs `bin args... --no-start` and gives its stdout, or why it gave nothing. */
export function runSquad(bin: string, args: string[], timeoutMs = TIMEOUT_MS): Promise<Result> {
  return new Promise((resolve) => {
    try {
      execFile(
        bin,
        [...args, "--no-start"],
        { timeout: timeoutMs, maxBuffer: 16 << 20, windowsHide: true, env: { ...process.env, NO_COLOR: "1" } },
        (error, stdout, stderr) => {
          if (!error) return resolve({ ok: true, out: String(stdout) });
          resolve(failureOf(bin, error as NodeJS.ErrnoException & { killed?: boolean }, String(stderr), timeoutMs));
        },
      );
    } catch (error) {
      // execFile throws synchronously for some bad commands (e.g. EINVAL for a .cmd on Windows).
      resolve(failureOf(bin, error as NodeJS.ErrnoException, "", timeoutMs));
    }
  });
}
