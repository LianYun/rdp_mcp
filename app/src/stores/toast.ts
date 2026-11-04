import { create } from "zustand";

export interface Toast {
  id: number;
  kind: "info" | "success" | "error";
  text: string;
}

interface ToastStore {
  toasts: Toast[];
  push: (kind: Toast["kind"], text: string) => void;
  dismiss: (id: number) => void;
}

let seq = 0;

export const useToasts = create<ToastStore>((set, get) => ({
  toasts: [],
  push: (kind, text) => {
    const id = ++seq;
    set((s) => ({ toasts: [...s.toasts, { id, kind, text }].slice(-5) }));
    setTimeout(() => get().dismiss(id), kind === "error" ? 8000 : 3500);
  },
  dismiss: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
}));

export const toast = {
  info: (text: string) => useToasts.getState().push("info", text),
  success: (text: string) => useToasts.getState().push("success", text),
  error: (e: unknown) => useToasts.getState().push("error", e instanceof Error ? e.message : String(e)),
};
