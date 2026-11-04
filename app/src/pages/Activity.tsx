import { Fragment, useMemo, useState } from "react";
import { ChevronDown, ChevronRight, Download } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/input";
import { useT } from "@/i18n";
import { formatClock } from "@/lib/utils";
import { saveBytes } from "@/lib/tauri";
import { useLive } from "@/stores/live";
import { mergeActivity } from "@/stores/reduce";
import { toast } from "@/stores/toast";
import type { AuditRecord } from "@/api/types";

export type ResultFilter = "" | "ok" | "fail" | "running";

export function filterRecords(list: AuditRecord[], f: { machine: string; tool: string; result: ResultFilter }): AuditRecord[] {
  return list.filter((r) => {
    if (f.machine && r.machine !== f.machine) return false;
    if (f.tool && r.tool !== f.tool) return false;
    if (f.result === "running") return r.phase === "start";
    if (f.result === "ok") return r.phase === "end" && r.ok !== false;
    if (f.result === "fail") return r.phase === "end" && r.ok === false;
    return true;
  });
}

export function toJsonl(list: AuditRecord[]): string {
  return list.map((r) => JSON.stringify(r)).join("\n") + (list.length ? "\n" : "");
}

function argsSummary(a: unknown): string {
  if (a === undefined || a === null) return "";
  const s = typeof a === "string" ? a : JSON.stringify(a);
  return s.length > 90 ? s.slice(0, 90) + "…" : s;
}

export default function Activity() {
  const t = useT();
  const client = useLive((s) => s.client);
  const running = useLive((s) => s.status === "running");
  const live = useLive((s) => s.activity);
  const machines = useLive((s) => s.machines);
  const [machine, setMachine] = useState("");
  const [tool, setTool] = useState("");
  const [result, setResult] = useState<ResultFilter>("");
  const [open, setOpen] = useState<string | null>(null);

  const audit = useQuery({
    queryKey: ["audit-all", client?.listen],
    queryFn: () => client!.audit({ limit: 300 }),
    enabled: !!client && running,
  });

  const all = useMemo(() => {
    let list: AuditRecord[] = audit.data ?? [];
    for (const r of [...live].reverse()) list = mergeActivity(list, r);
    return list;
  }, [audit.data, live]);
  const tools = useMemo(() => [...new Set(all.map((r) => r.tool))].sort(), [all]);
  const rows = useMemo(() => filterRecords(all, { machine, tool, result }), [all, machine, tool, result]);

  const exportJsonl = async () => {
    try {
      const stamp = new Date().toISOString().slice(0, 10);
      if (await saveBytes(`remoter-activity-${stamp}.jsonl`, new Blob([toJsonl(rows)], { type: "application/x-ndjson" }))) toast.success(t("settings.saved"));
    } catch (e) {
      toast.error(e);
    }
  };

  return (
    <div className="space-y-4 p-6" data-testid="page-activity">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-xl font-semibold">{t("activity.title")}</h1>
        <Button variant="outline" onClick={exportJsonl} disabled={rows.length === 0} data-testid="export-jsonl">
          <Download /> {t("activity.export")}
        </Button>
      </div>
      <div className="flex flex-wrap gap-2">
        <Select className="w-44" value={machine} onChange={(e) => setMachine(e.target.value)} data-testid="filter-machine">
          <option value="">{t("activity.all_machines")}</option>
          {machines.map((m) => (
            <option key={m.name} value={m.name}>
              {m.name}
            </option>
          ))}
        </Select>
        <Select className="w-44" value={tool} onChange={(e) => setTool(e.target.value)} data-testid="filter-tool">
          <option value="">{t("activity.all_tools")}</option>
          {tools.map((x) => (
            <option key={x} value={x}>
              {x}
            </option>
          ))}
        </Select>
        <Select className="w-40" value={result} onChange={(e) => setResult(e.target.value as ResultFilter)} data-testid="filter-result">
          <option value="">{t("activity.all_results")}</option>
          <option value="ok">{t("activity.ok")}</option>
          <option value="fail">{t("activity.fail")}</option>
          <option value="running">{t("activity.running")}</option>
        </Select>
      </div>

      <div className="overflow-hidden rounded-lg border">
        <table className="w-full text-sm" data-testid="activity-table">
          <thead className="bg-muted/60 text-left text-xs text-muted-foreground">
            <tr>
              <th className="w-8" />
              {["time", "machine", "tool", "args", "duration", "result"].map((c) => (
                <th key={c} className="px-3 py-2 font-medium">
                  {t(`activity.col.${c}`)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 && (
              <tr>
                <td colSpan={7} className="px-3 py-8 text-center text-muted-foreground">
                  {t("activity.empty")}
                </td>
              </tr>
            )}
            {rows.map((r) => {
              const isOpen = open === r.id;
              return (
                <Fragment key={r.id}>
                  <tr className="cursor-pointer border-t hover:bg-accent/40" onClick={() => setOpen(isOpen ? null : r.id)} data-testid="activity-row">
                    <td className="pl-2">{isOpen ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}</td>
                    <td className="whitespace-nowrap px-3 py-2 text-xs">{formatClock(r.time)}</td>
                    <td className="px-3 py-2">{r.machine ?? "-"}</td>
                    <td className="mono px-3 py-2">{r.tool}</td>
                    <td className="mono max-w-[280px] truncate px-3 py-2 text-xs text-muted-foreground">{argsSummary(r.args)}</td>
                    <td className="whitespace-nowrap px-3 py-2 text-xs">{r.duration_ms !== undefined ? `${r.duration_ms} ms` : "-"}</td>
                    <td className="px-3 py-2">
                      {r.phase === "start" ? (
                        <Badge variant="warning">{t("activity.running")}</Badge>
                      ) : r.ok === false ? (
                        <Badge variant="destructive">{t("activity.fail")}</Badge>
                      ) : (
                        <Badge variant="success">{t("activity.ok")}</Badge>
                      )}
                    </td>
                  </tr>
                  {isOpen && (
                    <tr className="border-t bg-muted/30" data-testid="activity-detail">
                      <td />
                      <td colSpan={6} className="space-y-2 px-3 py-3">
                        {r.summary && <p className="text-sm">{r.summary}</p>}
                        {r.args !== undefined && <pre className="mono max-h-48 overflow-auto rounded bg-muted p-2 text-xs">{JSON.stringify(r.args, null, 2)}</pre>}
                        {r.thumb && client && <img src={client.thumbUrl(r.thumb)} alt="thumb" className="max-h-48 rounded border" data-testid="activity-thumb" />}
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
