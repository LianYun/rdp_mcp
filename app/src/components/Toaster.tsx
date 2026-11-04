import { X } from "lucide-react";
import { useToasts } from "@/stores/toast";
import { cn } from "@/lib/utils";

export function Toaster() {
  const { toasts, dismiss } = useToasts();
  return (
    <div className="pointer-events-none fixed bottom-4 right-4 z-[100] flex w-80 flex-col gap-2" data-testid="toaster">
      {toasts.map((t) => (
        <div
          key={t.id}
          role="status"
          className={cn(
            "pointer-events-auto flex items-start gap-2 rounded-md border bg-card p-3 text-sm shadow-lg",
            t.kind === "error" && "border-destructive/50 text-destructive",
            t.kind === "success" && "border-success/50",
          )}
        >
          <span className="min-w-0 flex-1 break-words">{t.text}</span>
          <button onClick={() => dismiss(t.id)} aria-label="dismiss" className="opacity-60 hover:opacity-100">
            <X className="size-4" />
          </button>
        </div>
      ))}
    </div>
  );
}
