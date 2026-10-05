import fs from "node:fs";
import { writeAtomic } from "../ledger/store.ts";

export interface Letter {
  id: string;
  to: string;
  topic: string;
  text: string;
  next: string[];
  at: string;
}

interface Box {
  /** Messages waiting for their HC to be idle. */
  waiting: Letter[];
}

/** Messages waiting for their HC to be idle, kept on disk so a restart loses none. */
export class Mailbox {
  private readonly file: string;
  private box: Box;
  private seq = 0;

  constructor(file: string) {
    this.file = file;
    try {
      this.box = { waiting: (JSON.parse(fs.readFileSync(file, "utf8")) as Box).waiting ?? [] };
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
      this.box = { waiting: [] };
    }
  }

  add(letter: Omit<Letter, "id" | "at">): void {
    this.seq += 1;
    this.box.waiting.push({ ...letter, id: `${Date.now()}-${this.seq}`, at: new Date().toISOString() });
    this.save();
  }

  /** Removes and returns every letter waiting for `to`, oldest first. */
  take(to: string): Letter[] {
    const mine = this.box.waiting.filter((l) => l.to === to);
    if (!mine.length) return [];
    this.box.waiting = this.box.waiting.filter((l) => l.to !== to);
    this.save();
    return mine;
  }

  /** Puts letters back at the front, after a delivery that failed. */
  restore(letters: Letter[]): void {
    this.box.waiting = [...letters, ...this.box.waiting];
    this.save();
  }

  recipients(): string[] {
    return [...new Set(this.box.waiting.map((l) => l.to))];
  }

  /** Forgets an HC that is gone and its waiting letters. */
  drop(hc: string): void {
    this.box.waiting = this.box.waiting.filter((l) => l.to !== hc);
    this.save();
  }

  private save(): void {
    writeAtomic(this.file, JSON.stringify(this.box));
  }
}

/** Batches letters into one numbered message; every letter ends with the steps it allows. */
export function compose(letters: Letter[]): string {
  const total = letters.length;
  const index = letters.map((l, i) => `${i + 1} ${l.topic}`).join(" · ");
  const parts = letters.map((l, i) => [`--- ${i + 1}/${total} ---`, l.text, l.next.length ? `Next: ${l.next.join(" · ")}` : ""].filter(Boolean).join("\n"));
  return [`${total} message: ${index}`, ...parts].join("\n");
}
