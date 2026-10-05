import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { LedgerBroken, LedgerStore } from "../server/ledger/store.ts";
import { tempDir } from "./helpers.ts";

function tempFile(): string {
  const dir = tempDir("ledger");
  return path.join(dir, "ledger.json");
}

test("file chưa có là ledger rỗng; transact ghi xuống đĩa", () => {
  const file = tempFile();
  const store = new LedgerStore(file);
  assert.equal(store.read().seq, 0);
  store.transact((l) => {
    l.seq = 5;
  });
  assert.equal(JSON.parse(fs.readFileSync(file, "utf8")).seq, 5);
  assert.equal(new LedgerStore(file).read().seq, 5);
});

test("quyết định bị từ chối thì không ghi gì", () => {
  const file = tempFile();
  const store = new LedgerStore(file);
  store.transact((l) => {
    l.seq = 1;
  });
  assert.throws(() =>
    store.transact((l) => {
      l.seq = 99;
      throw new Error("refused");
    }),
  );
  assert.equal(store.read().seq, 1);
});

test("ledger hỏng: không bao giờ bị ghi đè, mọi lệnh sau đều dừng", () => {
  const file = tempFile();
  fs.writeFileSync(file, "{ not json");
  const store = new LedgerStore(file);
  assert.throws(() => store.read(), LedgerBroken);
  assert.throws(() => store.transact((l) => void (l.seq = 1)), LedgerBroken);
  assert.equal(fs.readFileSync(file, "utf8"), "{ not json");
});

test("ghi nguyên tử không để lại file tạm", () => {
  const file = tempFile();
  const store = new LedgerStore(file);
  store.transact((l) => void (l.seq = 2));
  assert.deepEqual(fs.readdirSync(path.dirname(file)), ["ledger.json"]);
});
