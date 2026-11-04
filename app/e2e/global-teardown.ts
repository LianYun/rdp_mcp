import fs from "node:fs";
import { STATE } from "./global-setup";

function alive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

async function stop(pid: number | undefined) {
  if (!pid || !alive(pid)) return;
  process.kill(pid, "SIGTERM");
  for (let i = 0; i < 40 && alive(pid); i++) await new Promise((r) => setTimeout(r, 200));
  if (alive(pid)) process.kill(pid, "SIGKILL");
}

// 只终止 globalSetup 记录的 PID。
export default async function globalTeardown() {
  if (!fs.existsSync(STATE)) return;
  const s = JSON.parse(fs.readFileSync(STATE, "utf8")) as { sample?: number; service?: number };
  await stop(s.service);
  await stop(s.sample);
  fs.rmSync(STATE, { force: true });
}
