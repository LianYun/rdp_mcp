import { OctagonX, Play } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { useLive } from "@/stores/live";
import { toast } from "@/stores/toast";

/** 全局紧急停止：一键生效，不二次确认（docs/ui.md §5）；有暂停的机器时显示“全部恢复”。 */
export function EmergencyStop() {
  const t = useT();
  const client = useLive((s) => s.client);
  const running = useLive((s) => s.status === "running");
  const paused = useLive((s) => s.machines.filter((m) => m.control_mode === "paused").length);
  return (
    <div className="flex items-center gap-2">
      {paused > 0 && (
        <Button
          variant="outline"
          disabled={!client || !running}
          data-testid="resume-all"
          onClick={async () => {
            try {
              const r = await client!.resumeAll();
              toast.success(t("emergency.resumed", { n: r.resumed.length }));
            } catch (e) {
              toast.error(e);
            }
          }}
        >
          <Play /> {t("emergency.resume", { n: paused })}
        </Button>
      )}
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
    </div>
  );
}
