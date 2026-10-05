import { test } from "node:test";
import assert from "node:assert/strict";
import path from "node:path";
import { Mailbox } from "../server/outbox/mailbox.ts";
import { tempDir } from "./helpers.ts";

test("Mailbox: message chờ được giữ qua lần nạp lại, lấy ra theo người nhận, trả lại khi giao lỗi, bỏ khi HC đóng", () => {
  const file = path.join(tempDir("mailbox"), "mailbox.json");
  const box = new Mailbox(file);
  box.add({ to: "s1-lead", topic: "ask:s1-a1", text: "hỏi", next: [] });
  box.add({ to: "s1-t1", topic: "held:s1-t1-d1", text: "giữ ý", next: [] });
  const again = new Mailbox(file);
  assert.deepEqual(again.recipients().sort(), ["s1-lead", "s1-t1"]);
  const taken = again.take("s1-lead");
  assert.equal(taken.length, 1);
  again.restore(taken);
  assert.equal(new Mailbox(file).take("s1-lead")[0]!.topic, "ask:s1-a1");
  again.drop("s1-t1");
  assert.deepEqual(new Mailbox(file).recipients(), ["s1-lead"]);
});
