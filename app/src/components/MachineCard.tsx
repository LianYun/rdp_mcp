import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Monitor } from "lucide-react";
import { Card } from "@/components/ui/card";
import { ControlBadge, StateBadge } from "@/components/StatusDot";
import { useT } from "@/i18n";
import { timeAgo } from "@/lib/utils";
import { useLive } from "@/stores/live";
import type { MachineInfo } from "@/api/types";

/** 概览页的机器卡片：缩略图（已连接时每 10 秒刷新）、状态、控制模式、最近一次操作。 */
export function MachineCard({ m }: { m: MachineInfo }) {
  const t = useT();
  const client = useLive((s) => s.client);
  const [thumb, setThumb] = useState<string | null>(null);

  useEffect(() => {
    if (!client || m.state !== "connected") {
      setThumb(null);
      return;
    }
    let alive = true;
    let url: string | null = null;
    const load = async () => {
      try {
        // 管理端截图不改变模型的坐标系（见 admin-api.md）
        const { blob } = await client.screenshot(m.name, { maxWidth: 480, format: "jpeg" });
        if (!alive) return;
        const next = URL.createObjectURL(blob);
        if (url) URL.revokeObjectURL(url);
        url = next;
        setThumb(next);
      } catch {
        /* 忽略：缩略图只是装饰 */
      }
    };
    void load();
    const id = setInterval(load, 10000);
    return () => {
      alive = false;
      clearInterval(id);
      if (url) URL.revokeObjectURL(url);
    };
  }, [client, m.name, m.state]);

  return (
    <Link to={`/machines/${encodeURIComponent(m.name)}`} data-testid={`machine-card-${m.name}`} className="block">
      <Card className="overflow-hidden transition-shadow hover:shadow-md">
        <div className="flex aspect-video items-center justify-center bg-muted">
          {thumb ? <img src={thumb} alt={m.name} className="size-full object-cover" /> : <Monitor className="size-8 text-muted-foreground/50" />}
        </div>
        <div className="space-y-1.5 p-3">
          <div className="flex items-center justify-between gap-2">
            <span className="truncate font-medium">{m.name}</span>
            <StateBadge state={m.state} />
          </div>
          <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
            <span className="truncate mono">
              {m.host}:{m.port}
            </span>
            <ControlBadge mode={m.control_mode} />
          </div>
          <div className="text-xs text-muted-foreground">
            {t("machine.last_activity")}: {m.last_activity ? t("common.ago", { d: timeAgo(m.last_activity) }) : "-"}
          </div>
          {m.last_error && m.state === "failed" && <div className="truncate text-xs text-destructive">{m.last_error}</div>}
        </div>
      </Card>
    </Link>
  );
}
