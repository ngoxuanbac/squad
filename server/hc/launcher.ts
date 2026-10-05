import fs from "node:fs";
import os from "node:os";
import path from "node:path";

/**
 * The HC launcher Paseo runs instead of the harness binary. A Paseo agent started without squad's
 * session_open hook, because the plugin is stopped or removed, would run with no role, so it refuses.
 * Paseo probing the binary has no PASEO_AGENT_ID and passes through.
 * The child gets pipes, not the launcher's own handles: when Paseo kills the launcher on Windows, the
 * child's stdin closes and the harness exits instead of running on as an orphan.
 */
export const LAUNCHER_SOURCE = String.raw`import { spawn } from "node:child_process";
const [real, ...args] = process.argv.slice(2);
const env = { ...process.env };
delete env.ELECTRON_RUN_AS_NODE;
const granted = !!env.SQUAD_GRANT;
delete env.SQUAD_GRANT;
if (env.PASEO_AGENT_ID && !granted) {
  process.stderr.write("squad: plugin squad không chạy nên HC squad này không khởi động, vì nó sẽ chạy thiếu vai trò. Bật lại plugin squad, hoặc dùng provider Claude thường.\n");
  process.exit(2);
}
if (!real) {
  process.stderr.write("squad: launcher không biết binary thật để chạy\n");
  process.exit(2);
}
const child = spawn(real, args, { env, stdio: ["pipe", "pipe", "pipe"], windowsHide: true });
process.stdin.pipe(child.stdin);
child.stdout.pipe(process.stdout);
child.stderr.pipe(process.stderr);
for (const stream of [child.stdin, process.stdout, process.stderr]) stream.on("error", () => {});
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => child.kill(signal));
child.on("error", (error) => {
  process.stderr.write("squad: không chạy được " + real + ": " + error.message + "\n");
  process.exit(127);
});
child.on("close", (code, signal) => process.exit(code ?? (signal ? 1 : 0)));
`;

export const LAUNCHER_FILE = "hc-launcher.mjs";

/** Writes the launcher into `binDir` and returns its path. */
export function writeLauncher(binDir: string): string {
  fs.mkdirSync(binDir, { recursive: true });
  const file = path.join(binDir, LAUNCHER_FILE);
  fs.writeFileSync(file, LAUNCHER_SOURCE);
  return file;
}

/** Finds the harness binary as a real executable on PATH, then in the native installer's ~/.local/bin. */
export function findBinary(name: string): string | undefined {
  const key = Object.keys(process.env).find((k) => k.toUpperCase() === "PATH") ?? "PATH";
  const file = process.platform === "win32" ? `${name}.exe` : name;
  const dirs = [...(process.env[key] ?? "").split(path.delimiter), path.join(os.homedir(), ".local", "bin")];
  for (const dir of dirs) {
    if (!dir) continue;
    const candidate = path.join(dir, file);
    if (fs.existsSync(candidate) && fs.statSync(candidate).isFile()) return candidate;
  }
  return undefined;
}

/**
 * Provider `command` for squad HCs; Paseo appends the harness arguments after these. The first word must
 * be "node": Paseo then runs its own Node with ELECTRON_RUN_AS_NODE set. Given Paseo.exe's path instead, it
 * strips that variable from the env of external commands and Paseo.exe starts as the Paseo CLI.
 */
export function launchCommand(launcher: string, binary: string): string[] {
  return ["node", launcher, binary];
}
