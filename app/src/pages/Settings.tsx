import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, Input, Select, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useT, type Lang } from "@/i18n";
import { restartService } from "@/lib/service";
import { autostartEnabled, getAppInfo, isTauri, setAutostart, type AppInfo, type AppSettings } from "@/lib/tauri";
import { buildPatch, DEFAULT_FIELDS, readAll, SERVER_FIELDS, validateField, type FieldSpec } from "@/lib/settingsPatch";
import { useLive } from "@/stores/live";
import { usePrefs, type Theme } from "@/stores/prefs";
import { toast } from "@/stores/toast";

function FieldInput({ spec, value, onChange }: { spec: FieldSpec; value: string; onChange: (v: string) => void }) {
  const t = useT();
  const bad = !validateField(spec, value);
  const id = `s-${spec.key}`;
  return (
    <Field label={t(spec.label) + (spec.restart ? ` (${t("settings.server.restart_tag")})` : "")} error={bad ? t("form.err.number") : undefined}>
      {spec.kind === "bool" ? (
        <Select value={value} onChange={(e) => onChange(e.target.value)} data-testid={id}>
          <option value="">{t("form.default")}</option>
          <option value="true">{t("common.yes")}</option>
          <option value="false">{t("common.no")}</option>
        </Select>
      ) : spec.kind === "select" ? (
        <Select value={value} onChange={(e) => onChange(e.target.value)} data-testid={id}>
          <option value="">{t("form.default")}</option>
          {spec.options!.map((o) => (
            <option key={o} value={o}>
              {o}
            </option>
          ))}
        </Select>
      ) : (
        <Input value={value} placeholder={spec.placeholder} onChange={(e) => onChange(e.target.value)} data-testid={id} />
      )}
    </Field>
  );
}

function ConfigSection({ kind }: { kind: "server" | "defaults" }) {
  const t = useT();
  const qc = useQueryClient();
  const client = useLive((s) => s.client);
  const running = useLive((s) => s.status === "running");
  const specs = kind === "server" ? SERVER_FIELDS : DEFAULT_FIELDS;
  const cfg = useQuery({ queryKey: ["config", client?.listen, "settings"], queryFn: () => client!.config(), enabled: !!client && running });
  const [before, setBefore] = useState<Record<string, string>>({});
  const [vals, setVals] = useState<Record<string, string>>({});
  const [needRestart, setNeedRestart] = useState(false);
  const [busy, setBusy] = useState(false);

  const beforeRef = useRef(before);
  beforeRef.current = before;

  // 服务端配置变化（含我们自己保存后的回读）时更新“基线”；只有表单没有未保存修改时才覆盖输入，
  // 避免后台刷新冲掉正在编辑的内容。
  useEffect(() => {
    if (!cfg.data) return;
    const r = readAll(specs, kind === "server" ? cfg.data.server : cfg.data.defaults);
    const same = (a: Record<string, string>, b: Record<string, string>) => specs.every((s) => (a[s.key] ?? "") === (b[s.key] ?? ""));
    setVals((cur) => (same(cur, beforeRef.current) ? r : cur));
    setBefore(r);
  }, [cfg.data, kind, specs]);

  if (!running) return <p className="text-sm text-muted-foreground">{t("settings.service_down")}</p>;

  const patch = buildPatch(specs, before, vals);
  const valid = specs.every((s) => validateField(s, vals[s.key] ?? ""));
  const save = async () => {
    setBusy(true);
    try {
      const r = kind === "server" ? await client!.putServer(patch) : await client!.putDefaults(patch);
      setNeedRestart(r.restart_required);
      toast.success(r.restart_required ? t("settings.restart_required") : t("settings.saved"));
      await qc.invalidateQueries({ queryKey: ["config"] });
    } catch (e) {
      toast.error(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardContent className="space-y-4">
        <div className="grid gap-3 md:grid-cols-2" data-testid={`settings-${kind}`}>
          {specs.map((s) => (
            <FieldInput key={s.key} spec={s} value={vals[s.key] ?? ""} onChange={(v) => setVals((p) => ({ ...p, [s.key]: v }))} />
          ))}
          {kind === "server" && (
            <Field label={t("settings.server.mcp_token")} hint={t("settings.server.mcp_token_hint")}>
              <Input disabled value={cfg.data?.server.mcp_token_set ? "••••••••" : t("mcp.no_token")} />
            </Field>
          )}
        </div>
        <div className="flex items-center gap-2">
          <Button onClick={save} disabled={busy || !valid || Object.keys(patch).length === 0} data-testid="settings-save">
            {t("common.save")}
          </Button>
          {needRestart && (
            <Button
              variant="outline"
              onClick={async () => {
                try {
                  await restartService();
                  setNeedRestart(false);
                } catch (e) {
                  toast.error(e);
                }
              }}
              disabled={!isTauri()}
            >
              {t("settings.restart_now")}
            </Button>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

function AppSection() {
  const t = useT();
  const { app, theme, setTheme, updateApp } = usePrefs();
  const [info, setInfo] = useState<AppInfo | null>(null);
  const [auto, setAuto] = useState(false);

  useEffect(() => {
    void getAppInfo().then(setInfo);
    void autostartEnabled().then(setAuto).catch(() => {});
  }, []);
  if (!app) return null;

  const toggle = (key: "auto_start_service" | "keep_service_running" | "close_to_tray") => (v: boolean) =>
    void updateApp({ [key]: v } as Partial<AppSettings>).catch(toast.error);
  const Row = ({ label, children }: { label: string; children: React.ReactNode }) => (
    <div className="flex items-center justify-between gap-4 py-2">
      <span className="text-sm">{label}</span>
      {children}
    </div>
  );

  return (
    <Card>
      <CardContent className="divide-y" data-testid="settings-app">
        <Row label={t("settings.app.autostart")}>
          <Switch
            checked={auto}
            disabled={!isTauri()}
            onCheckedChange={async (v) => {
              try {
                await setAutostart(v);
                setAuto(v);
              } catch (e) {
                toast.error(e);
              }
            }}
            data-testid="app-autostart"
          />
        </Row>
        <Row label={t("settings.app.auto_start_service")}>
          <Switch checked={app.auto_start_service} onCheckedChange={toggle("auto_start_service")} data-testid="app-auto-start-service" />
        </Row>
        <Row label={t("settings.app.keep_service")}>
          <Switch checked={app.keep_service_running} onCheckedChange={toggle("keep_service_running")} data-testid="app-keep-service" />
        </Row>
        <Row label={t("settings.app.close_to_tray")}>
          <Switch checked={app.close_to_tray} onCheckedChange={toggle("close_to_tray")} data-testid="app-close-to-tray" />
        </Row>
        <Row label={t("settings.app.theme")}>
          <Select className="w-40" value={theme} onChange={(e) => setTheme(e.target.value as Theme)} data-testid="app-theme">
            <option value="system">{t("settings.app.theme.system")}</option>
            <option value="light">{t("settings.app.theme.light")}</option>
            <option value="dark">{t("settings.app.theme.dark")}</option>
          </Select>
        </Row>
        <Row label={t("settings.app.language")}>
          <Select className="w-40" value={app.language} onChange={(e) => void updateApp({ language: e.target.value as Lang })} data-testid="app-language">
            <option value="zh">中文</option>
            <option value="en">English</option>
          </Select>
        </Row>
        <div className="space-y-1 pt-3 text-xs text-muted-foreground">
          <div>
            {t("settings.app.version")}: {info?.version ?? "-"}
          </div>
          {info?.data_dir && (
            <div className="mono">
              {t("settings.app.data_dir")}: {info.data_dir}
            </div>
          )}
          {info && isTauri() && !info.sidecar_found && <div className="text-destructive">{t("settings.app.sidecar_missing")}</div>}
          {!isTauri() && <div>{t("settings.app.browser_mode")}</div>}
        </div>
      </CardContent>
    </Card>
  );
}

function AdvancedSection() {
  const t = useT();
  const client = useLive((s) => s.client);
  const running = useLive((s) => s.status === "running");
  const [yaml, setYaml] = useState("");
  const [orig, setOrig] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const load = async () => {
    if (!client) return;
    try {
      const y = await client.configRaw();
      setYaml(y);
      setOrig(y);
      setErr("");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };
  useEffect(() => {
    if (running) void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, running]);

  if (!running) return <p className="text-sm text-muted-foreground">{t("settings.service_down")}</p>;

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.raw.title")}</CardTitle>
        <span className="text-xs text-muted-foreground">{t("settings.raw.hint")}</span>
      </CardHeader>
      <CardContent className="space-y-3">
        <Textarea className="mono min-h-[360px] text-xs" spellCheck={false} value={yaml} onChange={(e) => setYaml(e.target.value)} data-testid="raw-yaml" />
        {err && (
          <pre className="whitespace-pre-wrap rounded border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive" data-testid="raw-error">
            {err}
          </pre>
        )}
        <div className="flex gap-2">
          <Button
            disabled={busy || yaml === orig}
            data-testid="raw-save"
            onClick={async () => {
              setBusy(true);
              try {
                await client!.putConfigRaw(yaml);
                setOrig(yaml);
                setErr("");
                toast.success(t("settings.raw.saved"));
              } catch (e) {
                setErr(e instanceof Error ? e.message : String(e));
              } finally {
                setBusy(false);
              }
            }}
          >
            {t("settings.raw.save")}
          </Button>
          <Button variant="outline" onClick={load}>
            {t("settings.raw.reload")}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

export default function Settings() {
  const t = useT();
  return (
    <div className="space-y-4 p-6" data-testid="page-settings">
      <h1 className="text-xl font-semibold">{t("settings.title")}</h1>
      <Tabs defaultValue="server">
        <TabsList>
          <TabsTrigger value="server">{t("settings.tab.server")}</TabsTrigger>
          <TabsTrigger value="defaults">{t("settings.tab.defaults")}</TabsTrigger>
          <TabsTrigger value="app">{t("settings.tab.app")}</TabsTrigger>
          <TabsTrigger value="advanced">{t("settings.tab.advanced")}</TabsTrigger>
        </TabsList>
        <TabsContent value="server">
          <ConfigSection kind="server" />
        </TabsContent>
        <TabsContent value="defaults">
          <ConfigSection kind="defaults" />
        </TabsContent>
        <TabsContent value="app">
          <AppSection />
        </TabsContent>
        <TabsContent value="advanced">
          <AdvancedSection />
        </TabsContent>
      </Tabs>
    </div>
  );
}
