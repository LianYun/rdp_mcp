import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Pencil, Plus, Trash2, Upload } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input, Select } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { MachineFormDialog } from "@/components/MachineForm";
import { ControlBadge, StateBadge } from "@/components/StatusDot";
import { useT } from "@/i18n";
import { formFromConfig, emptyForm, toMachineDef, type MachineForm } from "@/lib/machineForm";
import { machineNameFromFile, parseRdpFile } from "@/lib/rdpfile";
import { pickTextFile } from "@/lib/tauri";
import { useLive } from "@/stores/live";
import { toast } from "@/stores/toast";
import type { MachineConfig } from "@/api/types";

export default function Machines() {
  const t = useT();
  const nav = useNavigate();
  const qc = useQueryClient();
  const client = useLive((s) => s.client);
  const machines = useLive((s) => s.machines);
  const running = useLive((s) => s.status === "running");
  const [q, setQ] = useState("");
  const [tag, setTag] = useState("");
  const [form, setForm] = useState<{ initial: MachineForm; editing?: string } | null>(null);
  const [del, setDel] = useState<string | null>(null);

  // 完整配置（含 enabled、分辨率、安全等）；config_changed 后由 SSE 触发的 machines 变化刷新。
  const cfg = useQuery({
    queryKey: ["config", client?.listen, machines.map((m) => m.name + m.host + m.port + m.enabled + m.username).join("|")],
    queryFn: () => client!.config(),
    enabled: !!client && running,
  });
  const cfgByName = useMemo(() => {
    const m = new Map<string, MachineConfig>();
    for (const c of cfg.data?.machines ?? []) m.set(c.name, c);
    return m;
  }, [cfg.data]);

  const allTags = useMemo(() => [...new Set(machines.flatMap((m) => m.tags ?? []))].sort(), [machines]);
  const filtered = machines.filter((m) => {
    const needle = q.trim().toLowerCase();
    if (needle && !`${m.name} ${m.host} ${m.description ?? ""} ${m.username}`.toLowerCase().includes(needle)) return false;
    if (tag && !(m.tags ?? []).includes(tag)) return false;
    return true;
  });

  const openEdit = (name: string) => {
    const c = cfgByName.get(name);
    if (!c) return toast.error(t("common.loading"));
    setForm({ initial: formFromConfig(c), editing: name });
  };

  const toggleEnabled = async (name: string, enabled: boolean) => {
    const c = cfgByName.get(name);
    if (!c || !client) return;
    try {
      // PUT 是整体替换：用当前配置重建 body，仅改 enabled；密码字段省略 = 保留原有设置。
      const f = formFromConfig(c);
      await client.updateMachine(name, toMachineDef({ ...f, enabled }, { editing: name }));
      await qc.invalidateQueries({ queryKey: ["config"] });
    } catch (e) {
      toast.error(e);
    }
  };

  const importRdp = async () => {
    const f = await pickTextFile(["rdp"]);
    if (!f) return;
    const r = parseRdpFile(f.text);
    if (!r.host) return toast.error(t("machines.import_failed"));
    const init = emptyForm();
    setForm({
      initial: {
        ...init,
        name: machineNameFromFile(f.name, r.host),
        host: r.host,
        port: r.port ? String(r.port) : "",
        username: r.username ?? "",
        domain: r.domain ?? "",
        width: r.width ? String(r.width) : "",
        height: r.height ? String(r.height) : "",
        security: r.security ?? "",
      },
    });
  };

  return (
    <div className="space-y-4 p-6" data-testid="page-machines">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-xl font-semibold">{t("machines.title")}</h1>
        <div className="flex gap-2">
          <Button variant="outline" onClick={importRdp} disabled={!running} data-testid="import-rdp">
            <Upload /> {t("machines.import")}
          </Button>
          <Button onClick={() => setForm({ initial: emptyForm() })} disabled={!running} data-testid="add-machine">
            <Plus /> {t("machines.add")}
          </Button>
        </div>
      </div>
      <div className="flex gap-2">
        <Input className="max-w-xs" placeholder={t("machines.search")} value={q} onChange={(e) => setQ(e.target.value)} data-testid="machine-search" />
        <Select className="w-44" value={tag} onChange={(e) => setTag(e.target.value)} data-testid="tag-filter">
          <option value="">{t("machines.all_tags")}</option>
          {allTags.map((x) => (
            <option key={x} value={x}>
              {x}
            </option>
          ))}
        </Select>
      </div>

      <div className="overflow-hidden rounded-lg border">
        <table className="w-full text-sm" data-testid="machine-table">
          <thead className="bg-muted/60 text-left text-xs text-muted-foreground">
            <tr>
              {["name", "address", "tags", "state", "control", "enabled", "actions"].map((c) => (
                <th key={c} className="px-3 py-2 font-medium">
                  {t(`machines.col.${c}`)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {filtered.length === 0 && (
              <tr>
                <td colSpan={7} className="px-3 py-8 text-center text-muted-foreground">
                  {t("machines.empty")}
                </td>
              </tr>
            )}
            {filtered.map((m) => (
              <tr key={m.name} className="border-t hover:bg-accent/40" data-testid={`row-${m.name}`}>
                <td className="px-3 py-2">
                  <button className="font-medium hover:underline" onClick={() => nav(`/machines/${encodeURIComponent(m.name)}`)}>
                    {m.name}
                  </button>
                  {m.description && <div className="max-w-[220px] truncate text-xs text-muted-foreground">{m.description}</div>}
                </td>
                <td className="mono px-3 py-2 text-xs">
                  {m.username}@{m.host}:{m.port}
                </td>
                <td className="px-3 py-2">
                  <div className="flex flex-wrap gap-1">
                    {(m.tags ?? []).map((x) => (
                      <Badge key={x} variant="secondary">
                        {x}
                      </Badge>
                    ))}
                  </div>
                </td>
                <td className="px-3 py-2">
                  <StateBadge state={m.state} />
                </td>
                <td className="px-3 py-2">
                  <ControlBadge mode={m.control_mode} />
                </td>
                <td className="px-3 py-2">
                  <Switch checked={m.enabled} onCheckedChange={(v) => toggleEnabled(m.name, v)} aria-label={`enabled-${m.name}`} data-testid={`enabled-${m.name}`} />
                </td>
                <td className="px-3 py-2">
                  <div className="flex gap-1">
                    <Button variant="ghost" size="icon" aria-label={t("common.edit")} onClick={() => openEdit(m.name)} data-testid={`edit-${m.name}`}>
                      <Pencil />
                    </Button>
                    <Button variant="ghost" size="icon" aria-label={t("common.delete")} onClick={() => setDel(m.name)} data-testid={`delete-${m.name}`}>
                      <Trash2 />
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {form && (
        <MachineFormDialog
          initial={form.initial}
          editing={form.editing}
          onClose={() => setForm(null)}
          onSaved={() => qc.invalidateQueries({ queryKey: ["config"] })}
        />
      )}
      <ConfirmDialog
        open={!!del}
        onOpenChange={(o) => !o && setDel(null)}
        title={t("machines.delete_title", { name: del ?? "" })}
        description={t("machines.delete_desc")}
        extra={{ label: t("machines.delete_secret"), defaultChecked: true }}
        destructive
        confirmLabel={t("common.delete")}
        onConfirm={async (delSecret) => {
          try {
            await client!.deleteMachine(del!, delSecret);
            toast.success(t("machines.deleted", { name: del! }));
            await qc.invalidateQueries({ queryKey: ["config"] });
          } catch (e) {
            toast.error(e);
          }
        }}
      />
      {!running && <p className="text-sm text-muted-foreground">{t("overview.service_down")}</p>}
    </div>
  );
}
