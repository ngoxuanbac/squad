import { test } from "node:test";
import assert from "node:assert/strict";
import { Slots } from "../server/runtime.ts";

test("Slots: không quá số chỗ cùng lúc, việc chờ được chạy khi có chỗ, lỗi vẫn trả chỗ", async () => {
  const slots = new Slots(2);
  let running = 0;
  let peak = 0;
  const job = (fail = false) =>
    slots.run(async () => {
      running += 1;
      peak = Math.max(peak, running);
      await new Promise((r) => setTimeout(r, 10));
      running -= 1;
      if (fail) throw new Error("x");
      return 1;
    });
  const results = await Promise.allSettled([job(), job(true), job(), job(), job()]);
  assert.equal(peak, 2);
  assert.equal(results.filter((r) => r.status === "fulfilled").length, 4);
  assert.equal(await job(), 1);
});
