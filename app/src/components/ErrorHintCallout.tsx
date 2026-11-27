import { useState } from "react";
import { Loader2, RotateCw, Settings2, ShieldAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { restartService } from "@/lib/service";
import { isTauri, openLocalNetworkSettings } from "@/lib/tauri";
import { cn } from "@/lib/utils";
import { useLive } from "@/stores/live";
import { toast } from "@/stores/toast";
import type { ErrorHint } from "@/api/types";

/**
 * 连接失败时的原因提示与处理引导。目前只有 `local_network`：macOS 15+ 上目标在本机所在子网，
 * 而启动服务的程序（通常是 Remoter）没有“本地网络”权限，系统直接拦截了连接。
 * compact：卡片里只显示一行（卡片整体是链接，不放按钮）。
 */
export function ErrorHintCallout({ hint, compact, className }: { hint?: ErrorHint; compact?: boolean; className?: string }) {
  const t = useT();
  const managedBy = useLive((s) => s.discovery?.managed_by);
  const busy = useLive((s) => s.busy);
  const [restarting, setRestarting] = useState(false);
  if (hint !== "local_network") return null;

  if (compact) {
    return (
      <div className={cn("flex items-center gap-1 text-xs text-warning", className)} data-testid="hint-local-network">
        <ShieldAlert className="size-3.5 shrink-0" />
        <span className="truncate">{t("hint.local_network.short")}</span>
      </div>
    );
  }

  // 由 Remoter 启动的服务，权限记在 Remoter 名下；CLI / launchd 启动时要授权给启动它的程序（如终端）。
  const ours = managedBy === "ui-sidecar";
  const tauri = isTauri();

  const openSettings = async () => {
    try {
      await openLocalNetworkSettings();
    } catch (e) {
      toast.error(e);
    }
  };
  const restart = async () => {
    setRestarting(true);
    useLive.getState().setBusy(true);
    try {
      await restartService();
      toast.success(t("hint.local_network.restarted"));
    } catch (e) {
      toast.error(e);
    } finally {
      useLive.getState().setBusy(false);
      setRestarting(false);
      await useLive.getState().refresh();
    }
  };

  return (
    <div className={cn("space-y-2 rounded-md border border-warning/40 bg-warning/10 p-3 text-sm", className)} data-testid="hint-local-network">
      <div className="flex items-center gap-2 font-medium text-warning">
        <ShieldAlert className="size-4 shrink-0" />
        {t("hint.local_network.title")}
      </div>
      <p className="text-xs text-muted-foreground">{t("hint.local_network.why")}</p>
      <ol className="list-decimal space-y-1 pl-5 text-xs">
        <li>{ours ? t("hint.local_network.step_allow") : t("hint.local_network.step_allow_other")}</li>
        <li>{t("hint.local_network.step_missing")}</li>
        <li>{t("hint.local_network.step_retry")}</li>
      </ol>
      {tauri && (
        <div className="flex flex-wrap gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={openSettings} data-testid="hint-open-settings">
            <Settings2 /> {t("hint.local_network.open_settings")}
          </Button>
          {ours && (
            <Button size="sm" variant="outline" onClick={restart} disabled={busy || restarting} data-testid="hint-restart">
              {restarting ? <Loader2 className="animate-spin" /> : <RotateCw />} {t("hint.local_network.restart")}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
