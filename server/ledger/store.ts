import fs from "node:fs";
import path from "node:path";
import { emptyLedger, type Ledger } from "./types.ts";

export class LedgerBroken extends Error {
  readonly file: string;
  constructor(file: string, cause: unknown) {
    super(`ledger hỏng ở ${file}: ${cause instanceof Error ? cause.message : String(cause)}. Squad ngừng nhận lệnh tới khi Human sửa hoặc dời file này.`);
    this.file = file;
  }
}

/** A callback that may not be async: read, decide and write happen with no await in between. */
type Sync<T> = T extends Promise<unknown> ? never : T;

export class LedgerStore {
  private readonly file: string;
  private broken: LedgerBroken | undefined;

  constructor(file: string) {
    this.file = file;
  }

  /** Reads the ledger. A missing file is an empty ledger; an unreadable one stops the desk and is never overwritten. */
  read(): Ledger {
    if (this.broken) throw this.broken;
    let raw: string;
    try {
      raw = fs.readFileSync(this.file, "utf8");
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === "ENOENT") return emptyLedger();
      throw (this.broken = new LedgerBroken(this.file, error));
    }
    try {
      const parsed = JSON.parse(raw) as Ledger;
      if (parsed?.version !== 1 || typeof parsed.seq !== "number") throw new Error("thiếu version hoặc seq");
      return migrate(parsed);
    } catch (error) {
      throw (this.broken = new LedgerBroken(this.file, error));
    }
  }

  /** Runs `decide` on a copy of the ledger and writes the copy only if `decide` returns without throwing. */
  transact<T>(decide: (ledger: Ledger) => Sync<T>): T {
    const draft = structuredClone(this.read());
    const result = decide(draft);
    writeAtomic(this.file, JSON.stringify(draft, null, 1) + "\n");
    return result;
  }
}

export function writeAtomic(file: string, content: string): void {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const temp = `${file}.${process.pid}.${Date.now()}.tmp`;
  fs.writeFileSync(temp, content, { mode: 0o600 });
  fs.renameSync(temp, file);
}

function migrate(ledger: Ledger): Ledger {
  for (const key of ["hcs", "squads", "tasks", "debates", "asks"] as const) {
    ledger[key] ??= {};
  }
  return ledger;
}
