import { create } from "zustand";
import { getAppSettings, setAppSettings, type AppSettings } from "@/lib/tauri";
import { useLang, type Lang } from "@/i18n";

export type Theme = "light" | "dark" | "system";

const THEME_KEY = "rdp_mcp.theme";

function systemDark(): boolean {
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false;
}

export function applyTheme(theme: Theme) {
  const dark = theme === "dark" || (theme === "system" && systemDark());
  document.documentElement.classList.toggle("dark", dark);
}

interface Prefs {
  theme: Theme;
  app: AppSettings | null;
  setTheme: (t: Theme) => void;
  load: () => Promise<void>;
  updateApp: (patch: Partial<AppSettings>) => Promise<void>;
}

export const usePrefs = create<Prefs>((set, get) => ({
  theme: (localStorage.getItem(THEME_KEY) as Theme) || "system",
  app: null,

  setTheme: (theme) => {
    localStorage.setItem(THEME_KEY, theme);
    applyTheme(theme);
    set({ theme });
  },

  load: async () => {
    const app = await getAppSettings();
    set({ app });
    useLang.getState().setLang(app.language === "en" ? "en" : "zh");
    applyTheme(get().theme);
  },

  updateApp: async (patch) => {
    const cur = get().app;
    if (!cur) return;
    const next = { ...cur, ...patch };
    await setAppSettings(next);
    set({ app: next });
    if (patch.language) useLang.getState().setLang(patch.language as Lang);
  },
}));

/** 跟随系统主题变化。 */
export function watchSystemTheme(): () => void {
  const mq = window.matchMedia?.("(prefers-color-scheme: dark)");
  if (!mq) return () => {};
  const h = () => {
    if (usePrefs.getState().theme === "system") applyTheme("system");
  };
  mq.addEventListener("change", h);
  return () => mq.removeEventListener("change", h);
}
