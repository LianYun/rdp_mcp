import { create } from "zustand";
import { zh } from "./zh";
import { en } from "./en";

export type Lang = "zh" | "en";
const dicts: Record<Lang, Record<string, string>> = { zh, en };

interface LangState {
  lang: Lang;
  setLang: (l: Lang) => void;
}

export const useLang = create<LangState>((set) => ({
  lang: "zh",
  setLang: (lang) => {
    document.documentElement.lang = lang === "zh" ? "zh-CN" : "en";
    set({ lang });
  },
}));

/** 翻译：缺失时回退到中文，再回退到 key。支持 `{name}` 占位符。 */
export function translate(lang: Lang, key: string, params?: Record<string, string | number>): string {
  let s = dicts[lang][key] ?? zh[key] ?? key;
  if (params) for (const [k, v] of Object.entries(params)) s = s.split(`{${k}}`).join(String(v));
  return s;
}

export function useT() {
  const lang = useLang((s) => s.lang);
  return (key: string, params?: Record<string, string | number>) => translate(lang, key, params);
}

/** 非 React 场景（通知等）。 */
export function t(key: string, params?: Record<string, string | number>): string {
  return translate(useLang.getState().lang, key, params);
}
