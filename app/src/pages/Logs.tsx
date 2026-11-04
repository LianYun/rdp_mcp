import { useEffect, useMemo, useRef, useState } from "react";
import { FolderOpen } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/input";
import { useT } from "@/i18n";
import { LOG_LEVELS, parseLogLine } from "@/lib/logs";
import { openLogsDir } from "@/lib/tauri";
import { cn } from "@/lib/utils";
import { useLive } from "@/stores/live";
import { toast } from "@/stores/toast";

const levelClass: Record<string, string> = {
  DEBUG: "text-muted-foreground",
  INFO: "",
  WARN: "text-warning",
  ERROR: "text-destructive",
};

export default function Logs() {
  const t = useT();
  const client = useLive((s) => s.client);
  const running = useLive((s) => s.status === "running");
  const [level, setLevel] = useState<string>("info");
  const [follow, setFollow] = useState(true);
  const boxRef = useRef<HTMLDivElement>(null);

  const q = useQuery({
    queryKey: ["logs", client?.listen, level],
    queryFn: () => client!.logs({ level, limit: 500 }),
    enabled: !!client && running,
    refetchInterval: 2000,
  });
  const lines = useMemo(() => (q.data?.lines ?? []).map(parseLogLine), [q.data]);

  useEffect(() => {
    if (follow && boxRef.current) boxRef.current.scrollTop = boxRef.current.scrollHeight;
  }, [lines, follow]);

  return (
    <div className="flex h-full flex-col gap-3 p-6" data-testid="page-logs">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-xl font-semibold">{t("logs.title")}</h1>
        <div className="flex items-center gap-3">
          <label className="flex items-center gap-2 text-sm">
            {t("logs.level")}
            <Select className="w-28" value={level} onChange={(e) => setLevel(e.target.value)} data-testid="log-level">
              {LOG_LEVELS.map((l) => (
                <option key={l} value={l}>
                  {l}
                </option>
              ))}
            </Select>
          </label>
          <label className="flex items-center gap-1.5 text-sm">
            <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} />
            {t("logs.follow")}
          </label>
          <Button variant="outline" onClick={() => void openLogsDir().catch(toast.error)}>
            <FolderOpen /> {t("logs.open_dir")}
          </Button>
        </div>
      </div>
      {q.data?.file && (
        <p className="mono text-xs text-muted-foreground">
          {t("logs.file")}: {q.data.file}
        </p>
      )}
      <div ref={boxRef} className="mono min-h-0 flex-1 overflow-auto rounded-lg border bg-card p-3 text-xs leading-5" data-testid="log-view">
        {lines.length === 0 && <div className="text-muted-foreground">{running ? t("logs.empty") : t("overview.service_down")}</div>}
        {lines.map((l, i) => (
          <div key={i} className={cn("whitespace-pre-wrap break-all", levelClass[l.level])} data-level={l.level}>
            {l.raw}
          </div>
        ))}
      </div>
    </div>
  );
}
