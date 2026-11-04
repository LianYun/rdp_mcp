import { OctagonX } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { useLive } from "@/stores/live";
import { toast } from "@/stores/toast";

/** 全局紧急停止：一键生效，不二次确认（docs/ui.md §5）。 */
export function EmergencyStop() {
  const t = useT();
  const client = useLive((s) => s.client);
  const running = useLive((s) => s.status === "running");
  return (
    <Button
      variant="destructive"
      disabled={!client || !running}
      data-testid="emergency-stop"
      onClick={async () => {
        try {
          await client!.emergencyStop();
          toast.success(t("emergency.done"));
        } catch (e) {
          toast.error(e);
        }
      }}
    >
      <OctagonX /> {t("emergency.button")}
    </Button>
  );
}
