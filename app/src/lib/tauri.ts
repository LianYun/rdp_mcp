// Tauri 桥接。同一份前端既能在 Tauri 里运行，也能在普通浏览器（Vite dev + Playwright）里运行。

export function isTauri(): boolean {
  return typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
}

export async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const m = await import("@tauri-apps/api/core");
  return m.invoke<T>(cmd, args);
}

export interface AppSettings {
  auto_start_service: boolean;
  keep_service_running: boolean;
  close_to_tray: boolean;
  language: string;
}

export interface AppInfo {
  version: string;
  os: string;
  data_dir: string;
  data_dir_overridden: boolean;
  sidecar_found: boolean;
}

export const defaultAppSettings: AppSettings = {
  auto_start_service: true,
  keep_service_running: true,
  close_to_tray: true,
  language: "zh",
};

/** 浏览器模式下把应用设置存在 localStorage。 */
const LS_KEY = "rdp_mcp.app_settings";

export async function getAppSettings(): Promise<AppSettings> {
  if (isTauri()) return invoke<AppSettings>("app_settings_get");
  try {
    return { ...defaultAppSettings, ...JSON.parse(localStorage.getItem(LS_KEY) ?? "{}") };
  } catch {
    return defaultAppSettings;
  }
}

export async function setAppSettings(v: AppSettings): Promise<void> {
  if (isTauri()) return invoke<void>("app_settings_set", { value: v });
  localStorage.setItem(LS_KEY, JSON.stringify(v));
}

export async function getAppInfo(): Promise<AppInfo> {
  if (isTauri()) return invoke<AppInfo>("app_info");
  return { version: "dev", os: "browser", data_dir: "", data_dir_overridden: false, sidecar_found: false };
}

export async function autostartEnabled(): Promise<boolean> {
  if (!isTauri()) return false;
  const m = await import("@tauri-apps/plugin-autostart");
  return m.isEnabled();
}

export async function setAutostart(on: boolean): Promise<void> {
  if (!isTauri()) return;
  const m = await import("@tauri-apps/plugin-autostart");
  if (on) await m.enable();
  else await m.disable();
}

export async function copyText(text: string): Promise<void> {
  if (isTauri()) {
    const m = await import("@tauri-apps/plugin-clipboard-manager");
    await m.writeText(text);
    return;
  }
  await navigator.clipboard.writeText(text);
}

/** 在 Finder / 文件管理器中打开服务日志目录（浏览器模式下无效果）。 */
export async function openLogsDir(): Promise<void> {
  if (!isTauri()) return;
  await invoke<void>("service_open_logs");
}

/** 保存二进制/文本文件：Tauri 下弹出保存对话框；浏览器下触发下载。返回是否已保存。 */
export async function saveBytes(defaultName: string, data: Blob): Promise<boolean> {
  if (isTauri()) {
    const dlg = await import("@tauri-apps/plugin-dialog");
    const path = await dlg.save({ defaultPath: defaultName });
    if (!path) return false;
    const core = await import("@tauri-apps/api/core");
    // 原始请求体 + `path` 头，对应 Rust 侧 save_file。
    await core.invoke("save_file", new Uint8Array(await data.arrayBuffer()), { headers: { path: encodeURIComponent(path) } });
    return true;
  }
  const url = URL.createObjectURL(data);
  const a = document.createElement("a");
  a.href = url;
  a.download = defaultName;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
  return true;
}

export async function pickTextFile(extensions: string[]): Promise<{ name: string; text: string } | null> {
  const input = document.createElement("input");
  input.type = "file";
  input.accept = extensions.map((e) => `.${e}`).join(",");
  return new Promise((resolve) => {
    input.onchange = async () => {
      const f = input.files?.[0];
      if (!f) return resolve(null);
      resolve({ name: f.name, text: await f.text() });
    };
    input.oncancel = () => resolve(null);
    input.click();
  });
}

export async function notify(title: string, body: string): Promise<void> {
  if (!isTauri()) return;
  const m = await import("@tauri-apps/plugin-notification");
  if (await m.isPermissionGranted()) m.sendNotification({ title, body });
}
