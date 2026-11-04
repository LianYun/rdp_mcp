import { useMemo, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Camera, ClipboardPaste, Hand, Keyboard, Link2, Link2Off, Loader2, Pause, RotateCw, Undo2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input, Select, Textarea } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { LiveView, type LiveViewHandle } from "@/components/LiveView";
import { ControlBadge, StateBadge } from "@/components/StatusDot";
import { useT } from "@/i18n";
import { formFromConfig, toMachineDef } from "@/lib/machineForm";
import { saveBytes } from "@/lib/tauri";
import { formatClock } from "@/lib/utils";
import { mergeActivity } from "@/stores/reduce";
import { useLive } from "@/stores/live";
import { toast } from "@/stores/toast";
import type { AuditRecord, ControlMode } from "@/api/types";

const PRESETS = ["1280x720", "1366x768", "1600x900", "1920x1080", "2560x1440"];

export default function MachineDetail() {
  const t = useT();
  const { name = "" } = useParams();
  const qc = useQueryClient();
  const client = useLive((s) => s.client);
  const machine = useLive((s) => s.machines.find((m) => m.name === name));
  const live = useLive((s) => s.activity);
  const running = useLive((s) => s.status === "running");
  const viewRef = useRef<LiveViewHandle>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [clipOpen, setClipOpen] = useState(false);
  const [clipText, setClipText] = useState("");
  const [imeText, setImeText] = useState("");
  const [resize, setResize] = useState<string | null>(null);

  const audit = useQuery({
    queryKey: ["audit", name, client?.listen],
    queryFn: () => client!.audit({ machine: name, limit: 30 }),
    enabled: !!client && running,
  });
  const calls = useMemo(() => {
    let list: AuditRecord[] = audit.data ?? [];
    for (const r of [...live].reverse()) if (r.machine === name) list = mergeActivity(list, r);
    return list.filter((r) => r.machine === name).slice(0, 30);
  }, [audit.data, live, name]);

  if (!machine || !client) {
    return (
      <div className="space-y-3 p-6">
        <Link to="/machines" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:underline">
          <ArrowLeft className="size-4" /> {t("detail.back")}
        </Link>
        <p className="text-sm text-muted-foreground" data-testid="detail-not-found">
          {running ? t("detail.not_found", { name }) : t("overview.service_down")}
        </p>
      </div>
    );
  }

  const act = async (key: string, fn: () => Promise<unknown>) => {
    setBusy(key);
    try {
      await fn();
    } catch (e) {
      toast.error(e);
    } finally {
      setBusy(null);
    }
  };

  const setMode = (mode: ControlMode) => act("mode", () => client.setControl(name, mode));
  const connected = machine.state === "connected";
  const transitional = machine.state === "connecting" || machine.state === "reconnecting";
  const takeover = machine.control_mode === "human";
  const curRes = machine.width && machine.height ? `${machine.width}x${machine.height}` : "";
  const resOptions = curRes && !PRESETS.includes(curRes) ? [curRes, ...PRESETS] : PRESETS;

  const applyResolution = async (res: string) => {
    const [w, h] = res.split("x").map(Number);
    const cfg = await client.config();
    const mc = cfg.machines?.find((m) => m.name === name);
    if (!mc) throw new Error(t("detail.not_found", { name }));
    // PUT 为整体替换：用现有配置重建，只改宽高；密码字段省略 = 保留原设置。
    await client.updateMachine(name, toMachineDef({ ...formFromConfig(mc), width: String(w), height: String(h) }, { editing: name }));
    await client.reconnect(name);
    await qc.invalidateQueries({ queryKey: ["config"] });
  };

  return (
    <div className="flex h-full flex-col gap-3 p-4" data-testid="page-machine-detail">
      <div className="flex flex-wrap items-center gap-3">
        <Link to="/machines" aria-label={t("detail.back")} className="text-muted-foreground hover:text-foreground">
          <ArrowLeft className="size-5" />
        </Link>
        <h1 className="text-lg font-semibold" data-testid="detail-name">
          {machine.name}
        </h1>
        <StateBadge state={machine.state} />
        <ControlBadge mode={machine.control_mode} />
        <span className="mono text-xs text-muted-foreground">
          {machine.username}@{machine.host}:{machine.port}
        </span>
        {machine.last_error && machine.state === "failed" && <span className="max-w-md truncate text-xs text-destructive">{machine.last_error}</span>}
      </div>

      <div className="flex flex-wrap items-center gap-2" data-testid="control-bar">
        {connected || transitional ? (
          <>
            <Button variant="outline" size="sm" disabled={!!busy || transitional} onClick={() => act("disconnect", () => client.disconnect(name))} data-testid="btn-disconnect">
              <Link2Off /> {t("detail.disconnect")}
            </Button>
            <Button variant="outline" size="sm" disabled={!!busy || transitional} onClick={() => act("reconnect", () => client.reconnect(name))} data-testid="btn-reconnect">
              <RotateCw /> {t("detail.reconnect")}
            </Button>
          </>
        ) : (
          <Button size="sm" disabled={!!busy} onClick={() => act("connect", () => client.connect(name))} data-testid="btn-connect">
            {busy === "connect" ? <Loader2 className="animate-spin" /> : <Link2 />} {t("detail.connect")}
          </Button>
        )}
        {(busy === "connect" || busy === "reconnect" || transitional) && <Loader2 className="size-4 animate-spin text-muted-foreground" />}

        <span className="mx-1 h-5 w-px bg-border" />

        {takeover ? (
          <Button size="sm" variant="secondary" disabled={!!busy} onClick={() => setMode("agent")} data-testid="btn-handback">
            <Undo2 /> {t("detail.handback")}
          </Button>
        ) : (
          <Button size="sm" disabled={!!busy} onClick={() => setMode("human")} data-testid="btn-takeover">
            <Hand /> {t("detail.takeover")}
          </Button>
        )}
        <Button size="sm" variant="outline" disabled={!!busy || machine.control_mode === "paused"} onClick={() => setMode("paused")} data-testid="btn-pause">
          <Pause /> {t("detail.pause")}
        </Button>

        <span className="mx-1 h-5 w-px bg-border" />

        <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
          {t("detail.resolution")}
          <Select
            className="h-8 w-32 text-xs"
            value={curRes}
            disabled={!!busy}
            onChange={(e) => setResize(e.target.value)}
            data-testid="resolution-select"
          >
            {!curRes && <option value="">-</option>}
            {resOptions.map((r) => (
              <option key={r} value={r}>
                {r.replace("x", "×")}
              </option>
            ))}
          </Select>
        </label>
        <Button size="sm" variant="outline" disabled={!takeover || !connected} onClick={() => viewRef.current?.sendCtrlAltDel()} data-testid="btn-cad" title={takeover ? "" : t("detail.clipboard_need_takeover")}>
          <Keyboard /> {t("detail.ctrl_alt_del")}
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={!connected || !!busy}
          data-testid="btn-screenshot"
          onClick={() =>
            act("shot", async () => {
              const { blob } = await client.screenshot(name, { format: "png" });
              const stamp = new Date().toISOString().replace(/[:.]/g, "-").slice(0, 19);
              if (await saveBytes(`${name}-${stamp}.png`, blob)) toast.success(t("detail.screenshot_saved"));
            })
          }
        >
          <Camera /> {t("detail.save_screenshot")}
        </Button>
        <Button size="sm" variant="outline" disabled={!takeover || !connected} onClick={() => setClipOpen(true)} data-testid="btn-clipboard" title={takeover ? "" : t("detail.clipboard_need_takeover")}>
          <ClipboardPaste /> {t("detail.send_clipboard")}
        </Button>
      </div>

      <div className="flex min-h-0 flex-1 gap-3">
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <div className="min-h-0 flex-1">
            <LiveView ref={viewRef} client={client} machine={machine} onServerError={(m) => toast.error(m)} />
          </div>
          <Input
            value={imeText}
            disabled={!takeover || !connected}
            placeholder={t("detail.ime_placeholder")}
            onChange={(e) => setImeText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.nativeEvent.isComposing && imeText) {
                viewRef.current?.sendText(imeText);
                setImeText("");
              }
            }}
            data-testid="ime-input"
          />
        </div>
        <aside className="hidden w-72 shrink-0 flex-col overflow-hidden rounded-lg border lg:flex" data-testid="recent-calls">
          <div className="border-b px-3 py-2 text-sm font-semibold">{t("detail.recent_calls")}</div>
          <ul className="min-h-0 flex-1 divide-y overflow-y-auto text-xs">
            {calls.length === 0 && <li className="px-3 py-4 text-muted-foreground">{t("detail.no_calls")}</li>}
            {calls.map((r) => (
              <li key={r.id} className="space-y-0.5 px-3 py-2">
                <div className="flex items-center justify-between gap-2">
                  <span className="mono font-medium">{r.tool}</span>
                  <span className="text-muted-foreground">{formatClock(r.time)}</span>
                </div>
                {r.summary && <div className="truncate text-muted-foreground">{r.summary}</div>}
                <div className={r.ok === false ? "text-destructive" : "text-muted-foreground"}>
                  {r.phase === "start" ? t("activity.running") : r.ok === false ? t("activity.fail") : t("activity.ok")}
                  {r.duration_ms !== undefined && ` · ${r.duration_ms} ms`}
                </div>
              </li>
            ))}
          </ul>
        </aside>
      </div>

      <Dialog open={clipOpen} onOpenChange={setClipOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("detail.clipboard_title")}</DialogTitle>
          </DialogHeader>
          <Textarea value={clipText} onChange={(e) => setClipText(e.target.value)} placeholder={t("detail.clipboard_placeholder")} className="min-h-[120px]" data-testid="clipboard-text" />
          <DialogFooter>
            <Button variant="outline" onClick={() => setClipOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              disabled={!clipText}
              data-testid="clipboard-send"
              onClick={() => {
                viewRef.current?.sendClipboard(clipText);
                toast.success(t("detail.clipboard_sent"));
                setClipOpen(false);
              }}
            >
              {t("detail.send_clipboard")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={!!resize}
        onOpenChange={(o) => !o && setResize(null)}
        title={t("detail.resolution")}
        description={`${resize?.replace("x", "×") ?? ""} — ${t("detail.reconnect")}`}
        onConfirm={() => act("resize", () => applyResolution(resize!))}
      />
    </div>
  );
}
