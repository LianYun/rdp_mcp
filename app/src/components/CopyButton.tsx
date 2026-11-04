import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";
import { copyText } from "@/lib/tauri";
import { useT } from "@/i18n";
import { toast } from "@/stores/toast";

export function CopyButton({ text, label, size = "sm" }: { text: string; label?: string; size?: "sm" | "icon" }) {
  const t = useT();
  const [done, setDone] = useState(false);
  return (
    <Button
      variant="outline"
      size={size}
      type="button"
      aria-label={label ?? t("common.copy")}
      onClick={async () => {
        try {
          await copyText(text);
          setDone(true);
          setTimeout(() => setDone(false), 1500);
        } catch (e) {
          toast.error(e);
        }
      }}
    >
      {done ? <Check /> : <Copy />}
      {size !== "icon" && (done ? t("common.copied") : (label ?? t("common.copy")))}
    </Button>
  );
}
