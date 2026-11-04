// 启动 e2e 所需的真实后端：FreeRDP 示例服务器 + remoter-mcp 服务。
// 只使用 /tmp/rdptest-ui、端口 13390 / 18766；只终止自己启动的进程（PID 写入 state.json）。
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { REPO, ROOT, STATE } from "./env";

function waitFor<T>(what: string, fn: () => T | undefined, timeoutMs = 30_000): Promise<T> {
  const start = Date.now();
  return new Promise((resolve, reject) => {
    const tick = () => {
      let v: T | undefined;
      try {
        v = fn();
      } catch {
        /* retry */
      }
      if (v !== undefined) return resolve(v);
      if (Date.now() - start > timeoutMs) return reject(new Error(`timed out waiting for ${what}`));
      setTimeout(tick, 200);
    };
    tick();
  });
}

function logTo(name: string): number {
  return fs.openSync(path.join(ROOT, name), "a");
}

export default async function globalSetup() {
  fs.rmSync(path.join(ROOT, "data"), { recursive: true, force: true });
  for (const d of ["cfg", "data", "bin", "shots", "cert"]) fs.mkdirSync(path.join(ROOT, d), { recursive: true });

  // 1) 构建本次测试用的服务与 worker（输出到 /tmp/rdptest-ui/bin，不碰 build/）
  const svc = path.join(ROOT, "bin/remoter-mcp");
  const worker = process.env.E2E_WORKER || path.join(REPO, "build/bin/remoter-worker");
  if (!process.env.E2E_SKIP_BUILD) {
    execFileSync("go", ["build", "-o", svc, "./cmd/remoter-mcp"], { cwd: REPO, stdio: "inherit" });
  }
  if (!fs.existsSync(worker)) throw new Error(`remoter-worker not found at ${worker} (run make worker)`);

  // 2) 证书与示例服务器
  const cert = path.join(ROOT, "cert");
  if (!fs.existsSync(path.join(cert, "server.crt"))) {
    execFileSync("winpr-makecert", ["-rdp", "-silent", "-path", cert, "-n", "server"], { stdio: "inherit" });
  }
  const sample: ChildProcess = spawn(
    "sfreerdp-server",
    ["--port=13390", `--cert=${cert}/server.crt`, `--key=${cert}/server.key`],
    { stdio: ["ignore", logTo("sample.log"), logTo("sample.log")] },
  );

  // 3) 配置并启动服务
  const cfgPath = path.join(ROOT, "cfg/configs.yaml");
  fs.writeFileSync(
    cfgPath,
    `# e2e 测试配置
server:
  listen: 127.0.0.1:18766
  data_dir: ${path.join(ROOT, "data")}
machines:
  - name: sample
    description: "FreeRDP 示例服务器"
    host: 127.0.0.1
    port: 13390
    username: test
    password: test
    security: tls
    enable_drive: false
    tags: [e2e]
`,
  );
  const service: ChildProcess = spawn(svc, ["serve", "--config", cfgPath, "--worker", worker, "--managed-by", "cli"], {
    stdio: ["ignore", logTo("service.out"), logTo("service.out")],
  });

  const info = await waitFor("service.json", () => {
    const p = path.join(ROOT, "data/run/service.json");
    if (!fs.existsSync(p)) return undefined;
    return JSON.parse(fs.readFileSync(p, "utf8")) as { listen: string; admin_token: string; pid: number };
  });
  fs.writeFileSync(
    STATE,
    JSON.stringify({ sample: sample.pid, service: info.pid ?? service.pid, listen: info.listen, token: info.admin_token }),
  );
  sample.unref();
  service.unref();
}
