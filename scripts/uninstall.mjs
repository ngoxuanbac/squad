// Removes the providers squad wrote into Paseo's config, after `paseo plugin remove squad`.
// Leftover providers are harmless, the HC launcher refuses to start without the plugin, but they still
// show "Squad · Supervisor" in Paseo. Run only while Paseo is fully closed: the daemon rewrites this file.
//   node scripts/uninstall.mjs
// Squad's own data (ledgers, kept working copies) stays in %LOCALAPPDATA%\squad; delete it yourself when done.
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";

const file = path.join(process.env.PASEO_HOME ?? path.join(os.homedir(), ".paseo"), "config.json");

const running = await new Promise((resolve) => {
  const socket = net.connect(6767, "127.0.0.1");
  socket.once("connect", () => (socket.destroy(), resolve(true)));
  socket.once("error", () => resolve(false));
});
if (running) {
  console.error("Paseo daemon vẫn đang chạy (cổng 6767). Thoát hẳn Paseo rồi chạy lại script này.");
  process.exit(1);
}

const config = JSON.parse(fs.readFileSync(file, "utf8"));
const providers = config.agents?.providers ?? {};
const ours = Object.keys(providers).filter((id) => /^squad-[a-z0-9-]+-(supervisor|lead|peer)$/.test(id));
if (!ours.length) {
  console.log("Không còn provider squad nào trong config Paseo.");
  process.exit(0);
}
for (const id of ours) delete providers[id];
fs.copyFileSync(file, `${file}.before-squad-uninstall`);
fs.writeFileSync(file, JSON.stringify(config, null, 2) + "\n");
console.log(`Đã xóa ${ours.join(", ")}. Bản cũ lưu ở ${file}.before-squad-uninstall. Mở lại Paseo.`);
