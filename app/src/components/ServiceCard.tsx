import { useEffect, useState } from "react";
import { FolderOpen, Play, RotateCw, Square } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { useT } from "@/i18n";
import { formatDuration } from "@/lib/utils";
import { restartService, startService, stopService } from "@/lib/service";
import { openLogsDir } from "@/lib/tauri";
import { useLive } from "@/stores/live";
import { toast } from "@/stores/toast";
import type { ServiceInfo } from "@/api/types";

export function ServiceCard() {
  const t = useT();
  const { status, discovery, client, busy, lastError } = useLive();
  const [info, setInfo] = useState<ServiceInfo | null>(null);
  const [confirmStop, setConfirmStop] = useState(false);
  const [now, setNow] = useState(Date.now());

  useEffect(() => {
    let alive = true;
    if (client && status === "running") client.service().then((i) => alive && setInfo(i)).catch(() => {});
    else setInfo(null);
    return () => {
      alive = false;
    };
  }, [client, status, discovery?.pid]);

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);

  const run = async (fn: () => Promise<unknown>) => {
    useLive.getState().setBusy(true);
    try {
      await fn();
    } catch (e) {
      toast.error(e);
    } finally {
      useLive.getState().setBusy(false);
      await useLive.getState().refresh();
    }
  };

  const running = status === "running";
  const variant = running ? "success" : status === "error" ? "destructive" : "secondary";
  const uptime = info ? formatDuration((now - Date.parse(info.started_at)) / 1000) : "-";
  const managed = discovery?.managed_by ?? info?.managed_by;

  return (
    <Card data-testid="service-card">
      <CardHeader>
        <CardTitle>{t("service.title")}</CardTitle>
        <Badge variant={variant} data-testid="service-status">
          {t(`service.status.${status}`)}
        </Badge>
      </CardHeader>
      <CardContent className="space-y-3">
        <dl className="grid grid-cols-[96px_1fr] gap-x-3 gap-y-1 text-sm">
          <dt className="text-muted-foreground">{t("service.version")}</dt>
          <dd data-testid="service-version">{info?.version ?? discovery?.version ?? "-"}</dd>
          <dt className="text-muted-foreground">PID</dt>
          <dd>{discovery?.pid ?? "-"}</dd>
          <dt className="text-muted-foreground">{t("service.uptime")}</dt>
          <dd>{running ? uptime : "-"}</dd>
          <dt className="text-muted-foreground">{t("service.managed_by")}</dt>
          <dd data-testid="service-managed-by">{managed ? t(`managed.${managed}`) : "-"}</dd>
          <dt className="text-muted-foreground">{t("service.listen")}</dt>
          <dd className="mono">{discovery?.listen ?? "-"}</dd>
        </dl>
        {status === "error" && lastError && <p className="text-sm text-destructive">{lastError}</p>}
        {discovery?.manager && !running && <p className="text-xs text-muted-foreground">{t("service.external_manager", { m: discovery.manager })}</p>}
        <div className="flex flex-wrap gap-2">
          {!running && (
            <Button disabled={busy} onClick={() => run(startService)} data-testid="service-start">
              <Play /> {t("service.start")}
            </Button>
          )}
          {running && (
            <>
              <Button variant="outline" disabled={busy} onClick={() => run(restartService)} data-testid="service-restart">
                <RotateCw /> {t("service.restart")}
              </Button>
              <Button variant="outline" disabled={busy} onClick={() => setConfirmStop(true)} data-testid="service-stop">
                <Square /> {t("service.stop")}
              </Button>
            </>
          )}
          <Button variant="ghost" onClick={() => void openLogsDir().catch(toast.error)}>
            <FolderOpen /> {t("service.open_logs")}
          </Button>
        </div>
      </CardContent>
      <ConfirmDialog
        open={confirmStop}
        onOpenChange={setConfirmStop}
        title={t("service.stop_confirm_title")}
        description={t("service.stop_confirm_desc")}
        destructive
        confirmLabel={t("service.stop")}
        onConfirm={() => run(stopService)}
      />
    </Card>
  );
}
