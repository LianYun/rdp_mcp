// 服务发现与启停。Tauri 下走 Rust 命令；浏览器（Vite dev / Playwright）下从
// URL 参数、localStorage 或 VITE_ 环境变量取得地址与 token，只能附着、不能启停进程。

import type { Discovery } from "@/api/types";
import { ApiClient } from "@/api/client";
import { invoke, isTauri } from "./tauri";

const LS_KEY = "remoter.dev_service";

interface DevService {
  listen: string;
  token: string;
}

function devService(): DevService | null {
  const q = new URLSearchParams(window.location.search);
  const listen = q.get("listen");
  const token = q.get("token");
  if (listen && token) {
    localStorage.setItem(LS_KEY, JSON.stringify({ listen, token }));
    return { listen, token };
  }
  try {
    const v = JSON.parse(localStorage.getItem(LS_KEY) ?? "null");
    if (v?.listen && v?.token) return v;
  } catch {
    /* ignore */
  }
  const env = import.meta.env;
  if (env.VITE_REMOTER_LISTEN && env.VITE_REMOTER_TOKEN) return { listen: env.VITE_REMOTER_LISTEN, token: env.VITE_REMOTER_TOKEN };
  return null;
}

async function browserDiscover(): Promise<Discovery> {
  const dev = devService();
  if (!dev) return { running: false, data_dir: "" };
  const c = new ApiClient(dev.listen, dev.token);
  try {
    const [h, s] = await Promise.all([c.health(), c.service()]);
    return {
      running: h.status === "ok",
      listen: dev.listen,
      admin_token: dev.token,
      managed_by: s.managed_by,
      pid: s.pid,
      version: s.version,
      config_path: s.config_path,
      data_dir: s.data_dir,
    };
  } catch {
    return { running: false, listen: dev.listen, data_dir: "" };
  }
}

export async function discover(): Promise<Discovery> {
  return isTauri() ? invoke<Discovery>("service_discover") : browserDiscover();
}

export async function startService(): Promise<Discovery> {
  if (!isTauri()) throw new Error("browser mode: cannot start the service");
  return invoke<Discovery>("service_start");
}

export async function stopService(): Promise<Discovery> {
  if (!isTauri()) {
    const d = await browserDiscover();
    if (d.listen && d.admin_token) await new ApiClient(d.listen, d.admin_token).shutdown();
    return { running: false, data_dir: d.data_dir };
  }
  return invoke<Discovery>("service_stop");
}

export async function restartService(): Promise<Discovery> {
  if (!isTauri()) throw new Error("browser mode: cannot restart the service");
  return invoke<Discovery>("service_restart");
}

export async function logsPath(): Promise<string> {
  return isTauri() ? invoke<string>("service_logs_path") : "";
}

export function clientFrom(d: Discovery): ApiClient | null {
  return d.running && d.listen && d.admin_token ? new ApiClient(d.listen, d.admin_token) : null;
}
